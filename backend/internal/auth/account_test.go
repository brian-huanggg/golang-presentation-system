package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alexedwards/scs/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
)

const (
	passwordPath = "/api/v1/auth/password"
	mePath       = "/api/v1/auth/me"
	newPassword  = "N3w-secret-pw#Q"
)

func pwBody(cur, next string) string {
	return `{"currentPassword":"` + cur + `","newPassword":"` + next + `"}`
}

// expectUserRow answers the by-ID lookup that every authenticated call starts with.
func (e *env) expectUserRow(version int64, mustChange bool, attempts int, locked any) {
	e.mock.ExpectQuery("SELECT id, employee_id").WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows(userCols).AddRow(7, "123456", "王小明", "TSID", "PARTICIPANT", "zh-TW", storedHash(e.t), version, mustChange, attempts, locked))
}

// expectPasswordWrite expects the successful change: one conditional UPDATE and its commit.
func (e *env) expectPasswordWrite(fromVersion int64, rows int64) {
	e.mock.ExpectBegin()
	e.mock.ExpectExec("UPDATE `user` SET password_hash = \\?, credential_version = credential_version \\+ 1, must_change_password = FALSE").
		WithArgs(sqlmock.AnyArg(), int64(7), fromVersion).WillReturnResult(sqlmock.NewResult(0, rows))
	if rows == 0 {
		e.mock.ExpectRollback()
		return
	}
	e.mock.ExpectCommit()
}

func (e *env) post(path, body string, c *http.Cookie) *httptest.ResponseRecorder {
	return e.do(http.MethodPost, path, body, c)
}

func (e *env) stored(token string) bool {
	_, found, err := e.store.Find(token)
	require.NoError(e.t, err)
	return found
}

// --- REG-007 / AUTH-007 -------------------------------------------------

func TestREG_007_ChangesPasswordAndAUTH_007_EndsOtherSessionsOnly(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	a, b := e.login(t), e.login(t)

	// A foreign user's session must survive the sweep.
	ctx, err := e.sm.Load(context.Background(), "")
	require.NoError(t, err)
	e.sm.Put(ctx, keyUserID, int64(99))
	foreign, _, err := e.sm.Commit(ctx)
	require.NoError(t, err)

	e.expectUserRow(0, false, 0, nil)
	e.expectPasswordWrite(0, 1)
	w := e.post(passwordPath, pwBody(loginPassword, newPassword), a)

	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.NoError(t, e.mock.ExpectationsWereMet())
	assert.False(t, e.stored(b.Value), "AUTH-007: the other session is gone at once")
	assert.True(t, e.stored(foreign), "another user's session is untouched")

	// The caller keeps working: its session now carries the new version.
	e.expectUserRow(1, false, 0, nil)
	assert.Equal(t, http.StatusOK, e.do(http.MethodGet, mePath, "", a).Code)
	// And the other cookie is dead without any database lookup.
	assert.Equal(t, http.StatusUnauthorized, e.do(http.MethodGet, mePath, "", b).Code)

	require.Len(t, e.rec.inTx, 1)
	got := e.rec.inTx[0]
	assert.Equal(t, actionPassword, got.Action)
	assert.Equal(t, audit.ResultSuccess, got.Result)
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "secret", "AUD-006: no password in the audit record")
}

// ADR 0018: the race the sweep cannot close. A session whose version is behind the
// user's is refused on its next request even though nothing deleted it.
func TestAUTH_007_SessionIssuedBeforePasswordChangeIsRejectedByVersion(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t) // issued under version 0

	e.expectUserRow(1, false, 0, nil) // the password changed since
	w := e.do(http.MethodGet, mePath, "", c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"unauthenticated"`)
	assert.Equal(t, http.StatusUnauthorized, e.do(http.MethodGet, mePath, "", c).Code, "and it is destroyed, not just refused once")
}

func TestREG_007_NewPasswordWorksAndAUTH_008_ClearsForcedChange(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	e.expectUser("ADMIN", true)
	w := e.do(http.MethodPost, loginPath, body(loginPassword), nil)
	require.Equal(t, http.StatusOK, w.Code)
	c := sessionCookie(t, w)

	e.mock.ExpectQuery("SELECT id, employee_id").WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows(userCols).AddRow(7, "123456", "x", nil, "ADMIN", "en", storedHash(t), 0, true, 0, nil))
	e.expectPasswordWrite(0, 1) // the statement itself carries must_change_password = FALSE
	w = e.post(passwordPath, pwBody(loginPassword, newPassword), c)

	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.NoError(t, e.mock.ExpectationsWereMet())
	assert.Equal(t, true, e.rec.inTx[0].Changes.(map[string]any)["before"].(map[string]any)["mustChangePassword"])
}

func TestREG_007_WrongCurrentPasswordIsRejectedAndCountsTowardLockout(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)

	e.expectUserRow(0, false, 0, nil)
	e.expectCount(0, 1)
	w := e.post(passwordPath, pwBody("Wrong-pass-9!", newPassword), c)

	assert.Equal(t, http.StatusBadRequest, w.Code, "400, not 401: the SPA must not read it as an expired session")
	assert.Contains(t, w.Body.String(), `"field":"currentPassword"`)
	assert.Contains(t, w.Body.String(), `"code":"password_incorrect"`)
	require.NoError(t, e.mock.ExpectationsWereMet())
	require.Len(t, e.rec.standalone, 2) // login success + this failure
	assert.Equal(t, audit.ResultFailure, e.rec.standalone[1].Result)
}

func TestREG_007_FifthWrongCurrentPasswordLocksTheAccount(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)

	e.expectUserRow(0, false, 4, nil)
	e.mock.ExpectBegin()
	e.mock.ExpectQuery("SELECT failed_attempts, locked_until FROM `user`").WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"failed_attempts", "locked_until"}).AddRow(4, nil))
	e.mock.ExpectExec("UPDATE `user` SET failed_attempts").WithArgs(0, t0.Add(5*time.Minute), int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	e.mock.ExpectCommit()
	w := e.post(passwordPath, pwBody("Wrong-pass-9!", newPassword), c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, e.mock.ExpectationsWereMet())
	require.Len(t, e.rec.inTx, 1)
	assert.Equal(t, actionLockout, e.rec.inTx[0].Action)
}

func TestREG_007_LockedAccountRefusesEvenTheRightCurrentPassword(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)

	e.expectUserRow(0, false, 0, t0.Add(time.Minute))
	w := e.post(passwordPath, pwBody(loginPassword, newPassword), c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"password_incorrect"`)
	require.NoError(t, e.mock.ExpectationsWereMet(), "no write and no failure count while locked")
}

func TestREG_007_NewPasswordMustMeetREG_005(t *testing.T) {
	cases := map[string]string{
		"Short1!":          "password_too_short",
		"alllowercase1!xx": "password_no_upper",
		"ALLUPPERCASE1!XX": "password_no_lower",
		"NoSymbolsHere12x": "password_no_symbol",
		"Abcdef!Xyz12Q":    "password_sequence",
		"Aaaa-secret-pw#Q": "password_repeat",
	}
	for pw, want := range cases {
		t.Run(want, func(t *testing.T) {
			e := newEnv(t, 30*time.Minute, 8*time.Hour)
			c := e.login(t)
			e.expectUserRow(0, false, 0, nil)

			w := e.post(passwordPath, pwBody(loginPassword, pw), c)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), `"field":"newPassword"`)
			assert.Contains(t, w.Body.String(), `"code":"`+want+`"`)
			require.NoError(t, e.mock.ExpectationsWereMet(), "a rejected password writes nothing")
		})
	}
}

func TestREG_007_FullWidthSequenceIsJudgedAfterNFKC(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)
	e.expectUserRow(0, false, 0, nil)

	w := e.post(passwordPath, pwBody(loginPassword, "Ｘ-ａｂｃ-secret-Q"), c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"password_sequence"`)
}

func TestREG_007_ConcurrentChangeIsAConflict(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)
	e.expectUserRow(0, false, 0, nil)
	e.expectPasswordWrite(0, 0) // someone else bumped the version first

	w := e.post(passwordPath, pwBody(loginPassword, newPassword), c)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"conflict"`)
}

func TestREG_007_RequiresASession(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)

	w := e.post(passwordPath, pwBody(loginPassword, newPassword), nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	w = e.do(http.MethodPatch, mePath, `{"preferredLanguage":"en"}`, nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	require.NoError(t, e.mock.ExpectationsWereMet())
}

func TestREG_007_RejectsMalformedBody(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)

	assert.Equal(t, http.StatusBadRequest, e.post(passwordPath, `{"currentPassword":`, c).Code)
	assert.Equal(t, http.StatusBadRequest, e.do(http.MethodPatch, mePath, `nope`, c).Code)
}

// DDL-004 / DDL-006: a closed event does not stop a password change.
func TestDDL_004_PasswordChangeIgnoresTheDeadline(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)
	e.h = e.rebuild(fakeGate{err: problemFor(http.StatusForbidden, "deadline_passed")}, e.sm)
	e.expectUserRow(0, false, 0, nil)
	e.expectPasswordWrite(0, 1)

	w := e.post(passwordPath, pwBody(loginPassword, newPassword), c)

	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
}

// ADR 0018 decision 3: the version check is the guarantee, the sweep is cleanup.
func TestAUTH_007_SweepFailureStillSucceeds(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)
	e.h = e.rebuild(fakeGate{}, brokenIterate{e.sm})
	e.expectUserRow(0, false, 0, nil)
	e.expectPasswordWrite(0, 1)

	w := e.post(passwordPath, pwBody(loginPassword, newPassword), c)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

type brokenIterate struct{ *scs.SessionManager }

func (brokenIterate) Iterate(context.Context, func(context.Context) error) error {
	return errors.New("store unavailable")
}

func (e *env) rebuild(gate DeadlineGate, sessions Sessions) http.Handler {
	e.t.Helper()
	svc := NewService(e.db, gate, NewArgon2idHasher(), sessions, e.rec, e.clock, quiet())
	h := NewHandler(svc, quiet())
	r := gin.New()
	r.POST("/api/v1/auth/login", h.Login)
	r.GET(mePath, h.GetCurrentUser)
	r.PATCH(mePath, h.UpdatePreferences)
	r.POST(passwordPath, h.ChangePassword)
	return e.sm.LoadAndSave(r)
}

func TestREG_007_DatabaseErrorIsNotLeaked(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)
	e.expectUserRow(0, false, 0, nil)
	e.mock.ExpectBegin()
	e.mock.ExpectExec("UPDATE `user` SET password_hash").WillReturnError(errors.New("dial db.internal:3306 refused"))
	e.mock.ExpectRollback()

	w := e.post(passwordPath, pwBody(loginPassword, newPassword), c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "db.internal")
}

// --- I18N-002 -------------------------------------------------------------

func TestI18N_002_PersistsPreferredLanguage(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)

	e.expectUserRow(0, false, 0, nil)
	e.mock.ExpectExec("UPDATE `user` SET preferred_language = \\?").WithArgs("en", int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	w := e.do(http.MethodPatch, mePath, `{"preferredLanguage":"en"}`, c)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"preferredLanguage":"en"`)
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.NoError(t, e.mock.ExpectationsWereMet())
}

func TestI18N_002_RejectsUnsupportedLanguage(t *testing.T) {
	for _, lang := range []string{"fr", ""} {
		e := newEnv(t, 30*time.Minute, 8*time.Hour)
		c := e.login(t)
		e.expectUserRow(0, false, 0, nil)

		w := e.do(http.MethodPatch, mePath, `{"preferredLanguage":"`+lang+`"}`, c)

		assert.Equal(t, http.StatusBadRequest, w.Code, lang)
		assert.Contains(t, w.Body.String(), `"field":"preferredLanguage"`)
		require.NoError(t, e.mock.ExpectationsWereMet(), "nothing is written")
	}
}

func TestI18N_002_LoginReturnsTheSavedLanguage(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	e.mock.ExpectQuery("SELECT id, employee_id").WithArgs("123456").
		WillReturnRows(sqlmock.NewRows(userCols).AddRow(7, "123456", "x", "TSID", "PARTICIPANT", "en", storedHash(t), 0, false, 0, nil))

	w := e.do(http.MethodPost, loginPath, body(loginPassword), nil)

	assert.Contains(t, w.Body.String(), `"preferredLanguage":"en"`)
}

func TestI18N_002_DatabaseErrorIsNotLeaked(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)
	e.expectUserRow(0, false, 0, nil)
	e.mock.ExpectExec("UPDATE `user` SET preferred_language").WillReturnError(sql.ErrConnDone)

	w := e.do(http.MethodPatch, mePath, `{"preferredLanguage":"en"}`, c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
