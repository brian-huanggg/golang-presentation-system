package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
)

const insertAdminRe = "INSERT INTO `user` .* ON DUPLICATE KEY UPDATE id = id"

func writeAdmins(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "admins.json")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func adminJSON(t *testing.T, ids ...string) string {
	t.Helper()
	var items []string
	for _, id := range ids {
		items = append(items, `{"employeeId":"`+id+`","fullName":"管理員 `+id+`","email":"a`+id+`@example.com","passwordHash":"`+storedHash(t)+`"}`)
	}
	return "[" + strings.Join(items, ",") + "]"
}

func newSeedDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock, *recorder) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	return sqlx.NewDb(raw, "mysql"), mock, &recorder{}
}

// ADM-004: new Admins are created with must_change_password and one audit record each.
func TestADM_004_SeedCreatesMissingAdminsAndAudits(t *testing.T) {
	db, mock, rec := newSeedDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(insertAdminRe).WithArgs("000001", "管理員 000001", "a000001@example.com", storedHash(t)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(insertAdminRe).WithArgs("000002", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()

	created, err := SeedAdmins(context.Background(), db, rec, writeAdmins(t, adminJSON(t, "000001", "000002")), quiet())

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, []string{"000001", "000002"}, created)
	require.Len(t, rec.inTx, 2)
	assert.Equal(t, actionProvision, rec.inTx[0].Action)
	assert.Equal(t, audit.ResultSuccess, rec.inTx[0].Result)
	raw, err := json.Marshal(rec.inTx[0])
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "argon2", "AUD-006: no hash in the audit record")
}

// ADM-004: an existing Admin is left alone; running again is a no-op (idempotent).
func TestADM_004_SeedIsIdempotentAndNeverOverwrites(t *testing.T) {
	db, mock, rec := newSeedDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(insertAdminRe).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT role FROM `user` WHERE employee_id = \\? FOR UPDATE").WithArgs("000001").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("ADMIN"))
	mock.ExpectCommit()

	created, err := SeedAdmins(context.Background(), db, rec, writeAdmins(t, adminJSON(t, "000001")), quiet())

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Empty(t, created)
	assert.Empty(t, rec.inTx, "nothing created, nothing audited")
}

// ADM-004 / R-01 / R-04: a listed ID that is a Participant stops startup with a clear
// error, and everything seeded before it is rolled back.
func TestADM_004_SeedFailsWhenIDIsAlreadyAParticipant(t *testing.T) {
	db, mock, rec := newSeedDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(insertAdminRe).WithArgs("000001", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(insertAdminRe).WithArgs("123456", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT role FROM `user`").WithArgs("123456").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("PARTICIPANT"))
	mock.ExpectRollback()

	_, err := SeedAdmins(context.Background(), db, rec, writeAdmins(t, adminJSON(t, "000001", "123456")), quiet())

	require.ErrorIs(t, err, ErrAdminConflict)
	assert.Contains(t, err.Error(), "123456")
	assert.Contains(t, err.Error(), "participant")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestADM_004_SeedRejectsBadFilesBeforeTouchingTheDatabase(t *testing.T) {
	good := storedHash(t)
	entry := func(id, name, email, hash string) string {
		return `[{"employeeId":"` + id + `","fullName":"` + name + `","email":"` + email + `","passwordHash":"` + hash + `"}]`
	}
	cases := map[string]string{
		"not json":           `nope`,
		"not an array":       `{"employeeId":"000001"}`,
		"trailing data":      `[] []`,
		"plaintext password": `[{"employeeId":"000001","fullName":"A","password":"S3cret-pass!","passwordHash":"` + good + `"}]`,
		"bad id":             entry("12345", "A", "", good),
		"blank name":         entry("000001", "  ", "", good),
		"bad email":          entry("000001", "A", "nope", good),
		"plaintext as hash":  entry("000001", "A", "", "S3cret-pass!"),
		"other algorithm":    entry("000001", "A", "", "$2a$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ012345"),
		"huge memory":        entry("000001", "A", "", "$argon2id$v=19$m=4194304,t=2,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5"),
		"duplicate id":       `[` + strings.Trim(entry("000001", "A", "", good), "[]") + `,` + strings.Trim(entry("000001", "B", "", good), "[]") + `]`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			db, mock, rec := newSeedDB(t) // no expectations: any query fails the test
			_, err := SeedAdmins(context.Background(), db, rec, writeAdmins(t, content), quiet())
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "S3cret", "the error must not echo a secret")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestADM_004_SeedFailsWhenFileIsMissingAndAcceptsEmptyList(t *testing.T) {
	db, mock, rec := newSeedDB(t)
	_, err := SeedAdmins(context.Background(), db, rec, filepath.Join(t.TempDir(), "absent.json"), quiet())
	require.Error(t, err)

	mock.ExpectBegin()
	mock.ExpectCommit()
	created, err := SeedAdmins(context.Background(), db, rec, writeAdmins(t, `[]`), quiet())
	require.NoError(t, err)
	assert.Empty(t, created)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- AUTH-008: the forced password change -------------------------------

func (e *env) loginAdminMustChange(t *testing.T) (*http.Cookie, string) {
	t.Helper()
	e.expectUser("ADMIN", true)
	w := e.do(http.MethodPost, "/api/v1/auth/login", body(loginPassword), nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	c, tok := sessionCookie(t, w), csrfCookie(t, w).Value
	if e.tokens == nil {
		e.tokens = map[string]string{}
	}
	e.tokens[c.Value] = tok
	return c, tok
}

func TestAUTH_008_FlaggedSessionIsBlockedExceptMeLogoutAndPassword(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c, _ := e.loginAdminMustChange(t)

	w := e.send(http.MethodPatch, "/api/v1/auth/me", `{"preferredLanguage":"en"}`, c)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"password_change_required"`)

	// /auth/me still answers, and tells the SPA why.
	e.mock.ExpectQuery("SELECT id, employee_id").WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows(userCols).AddRow(7, "123456", "x", nil, "ADMIN", "en", storedHash(t), 0, true, 0, nil))
	w = e.do(http.MethodGet, mePath, "", c)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"mustChangePassword":true`)

	assert.Equal(t, http.StatusNoContent, e.send(http.MethodPost, "/api/v1/auth/logout", "", c).Code)
}

func TestAUTH_008_ChangingThePasswordOpensTheGate(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c, _ := e.loginAdminMustChange(t)

	e.mock.ExpectQuery("SELECT id, employee_id").WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows(userCols).AddRow(7, "123456", "x", nil, "ADMIN", "en", storedHash(t), 0, true, 0, nil))
	e.expectPasswordWrite(0, 1)
	w := e.send(http.MethodPost, passwordPath, pwBody(loginPassword, newPassword), c)
	require.Equal(t, http.StatusNoContent, w.Code)
	rotated := csrfCookie(t, w).Value // ADR 0018: the token rotates with the password

	// Same session, no new login: the preference change now goes through to the handler.
	e.mock.ExpectQuery("SELECT id, employee_id").WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows(userCols).AddRow(7, "123456", "x", nil, "ADMIN", "en", storedHash(t), 1, false, 0, nil))
	e.mock.ExpectExec("UPDATE `user` SET preferred_language").WillReturnResult(sqlmock.NewResult(0, 1))
	w = e.doCSRF(http.MethodPatch, "/api/v1/auth/me", `{"preferredLanguage":"zh-TW"}`, c, rotated)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, e.mock.ExpectationsWereMet())
}

func TestAUTH_008_UnflaggedSessionIsNotGated(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c, _ := e.loginWithCSRF(t)
	e.expectUserByID()
	e.mock.ExpectExec("UPDATE `user` SET preferred_language").WillReturnResult(sqlmock.NewResult(0, 1))
	assert.Equal(t, http.StatusOK, e.send(http.MethodPatch, "/api/v1/auth/me", `{"preferredLanguage":"en"}`, c).Code)
}
