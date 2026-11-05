package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// --- fakes -------------------------------------------------------------

type fakeGate struct{ err error }

func (g fakeGate) CheckActor(bool) error { return g.err }

type fakeHasher struct{ err error }

func (h fakeHasher) Hash(context.Context, string) (string, error) { return "HASH", h.err }

// recorder captures audit entries; inside-tx ones are executed on the tx so sqlmock sees them.
type recorder struct {
	inTx       []audit.Entry
	standalone []audit.Entry
}

func (r *recorder) Record(_ context.Context, _ sqlx.ExecerContext, e audit.Entry) error {
	r.inTx = append(r.inTx, e)
	return nil
}
func (r *recorder) RecordStandalone(_ context.Context, e audit.Entry) error {
	r.standalone = append(r.standalone, e)
	return nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newSvc(t *testing.T, gate DeadlineGate, h Hasher) (*Service, sqlmock.Sqlmock, *recorder) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	rec := &recorder{}
	return NewService(sqlx.NewDb(raw, "mysql"), gate, h, rec, quiet()), mock, rec
}

func validInput() RegisterInput {
	return RegisterInput{
		EmployeeID: "123456", FullName: "  王小明 ", Department: "TSID",
		Password: "S3cret-pass!", PreferredLanguage: "zh-TW", Email: "ming@example.com",
	}
}

func problemOf(t *testing.T, err error) *platform.Problem {
	t.Helper()
	var p *platform.Problem
	require.ErrorAs(t, err, &p)
	return p
}

// --- validation (REG-001, REG-002, REG-008) ----------------------------

func TestValidateRegister(t *testing.T) {
	u, errs := validateRegister(validInput())
	require.Empty(t, errs)
	assert.Equal(t, "王小明", u.FullName, "name is trimmed")
	assert.Equal(t, platform.EmployeeID("123456"), u.EmployeeID)

	cases := []struct {
		name   string
		mutate func(*RegisterInput)
		field  string
		code   string
	}{
		{"id too short", func(i *RegisterInput) { i.EmployeeID = "12345" }, "employeeId", "invalid_format"},
		{"id too long", func(i *RegisterInput) { i.EmployeeID = "1234567" }, "employeeId", "invalid_format"},
		{"id letter", func(i *RegisterInput) { i.EmployeeID = "12a456" }, "employeeId", "invalid_format"},
		{"id symbol", func(i *RegisterInput) { i.EmployeeID = "12-456" }, "employeeId", "invalid_format"},
		{"id space not trimmed", func(i *RegisterInput) { i.EmployeeID = " 12345" }, "employeeId", "invalid_format"},
		{"id unicode digit", func(i *RegisterInput) { i.EmployeeID = "１２３４５６" }, "employeeId", "invalid_format"},
		{"id empty", func(i *RegisterInput) { i.EmployeeID = "" }, "employeeId", "required"},
		{"name blank", func(i *RegisterInput) { i.FullName = "   " }, "fullName", "required"},
		{"name too long", func(i *RegisterInput) { i.FullName = strings.Repeat("字", 101) }, "fullName", "too_long"},
		{"dept unknown", func(i *RegisterInput) { i.Department = "XXXX" }, "department", "invalid_value"},
		{"dept lowercase", func(i *RegisterInput) { i.Department = "tsid" }, "department", "invalid_value"},
		{"dept empty", func(i *RegisterInput) { i.Department = "" }, "department", "required"},
		{"lang unknown", func(i *RegisterInput) { i.PreferredLanguage = "fr" }, "preferredLanguage", "invalid_value"},
		{"lang empty", func(i *RegisterInput) { i.PreferredLanguage = "" }, "preferredLanguage", "required"},
		{"password empty", func(i *RegisterInput) { i.Password = "" }, "password", "required"},
		{"password huge", func(i *RegisterInput) { i.Password = strings.Repeat("a", 129) }, "password", "too_long"},
		{"email malformed", func(i *RegisterInput) { i.Email = "not-an-email" }, "email", "invalid_format"},
		{"email display name", func(i *RegisterInput) { i.Email = "Ming <ming@example.com>" }, "email", "invalid_format"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			tc.mutate(&in)
			_, errs := validateRegister(in)
			assert.Contains(t, errs, platform.FieldError{Field: tc.field, Code: tc.code})
		})
	}

	t.Run("email is optional", func(t *testing.T) {
		in := validInput()
		in.Email = "  "
		u, errs := validateRegister(in)
		require.Empty(t, errs)
		assert.Empty(t, u.Email)
	})
	t.Run("reports every bad field at once", func(t *testing.T) {
		_, errs := validateRegister(RegisterInput{})
		assert.Len(t, errs, 5) // email is optional
	})
}

// --- service (REG-003, REG-006, REG-009) -------------------------------

func TestRegister_Success(t *testing.T) {
	svc, mock, rec := newSvc(t, fakeGate{}, fakeHasher{})
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `user`").
		WithArgs("123456", "王小明", "ming@example.com", "TSID", "zh-TW", "HASH").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	u, err := svc.Register(context.Background(), validInput(), "10.0.0.9")
	require.NoError(t, err)
	assert.Equal(t, "123456", u.EmployeeID)
	require.NoError(t, mock.ExpectationsWereMet())

	require.Len(t, rec.inTx, 1, "audit row is written inside the registration transaction")
	e := rec.inTx[0]
	assert.Equal(t, "auth.register", e.Action)
	assert.Equal(t, audit.ResultSuccess, e.Result)
	assert.Equal(t, "123456", e.TargetID)
	assert.NotContains(t, strings.ToLower(jsonOf(t, e.Changes)), "hash")
	assert.NotContains(t, jsonOf(t, e.Changes), "S3cret", "AUD-006")
}

func TestRegister_NoEmailStoresNull(t *testing.T) {
	svc, mock, _ := newSvc(t, fakeGate{}, fakeHasher{})
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `user`").
		WithArgs("123456", "王小明", nil, "TSID", "zh-TW", "HASH").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	in := validInput()
	in.Email = ""
	_, err := svc.Register(context.Background(), in, "")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRegister_DuplicateEmployeeID(t *testing.T) {
	svc, mock, rec := newSvc(t, fakeGate{}, fakeHasher{})
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `user`").
		WillReturnError(&mysql.MySQLError{Number: 1062, Message: "Duplicate entry '123456' for key 'uq_user_employee_id'"})
	mock.ExpectRollback()

	_, err := svc.Register(context.Background(), validInput(), "10.0.0.9")
	p := problemOf(t, err)
	assert.Equal(t, http.StatusConflict, p.Status)
	assert.Equal(t, CodeEmployeeIDTaken, p.Code)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Empty(t, rec.inTx, "no success audit row for a rejected registration")
	require.Len(t, rec.standalone, 1)
	assert.Equal(t, audit.ResultFailure, rec.standalone[0].Result)
}

func TestRegister_AfterDeadline(t *testing.T) {
	deadline := problemFor(http.StatusForbidden, "deadline_passed")
	svc, mock, rec := newSvc(t, fakeGate{err: deadline}, fakeHasher{})

	_, err := svc.Register(context.Background(), validInput(), "10.0.0.9")
	p := problemOf(t, err)
	assert.Equal(t, http.StatusForbidden, p.Status)
	assert.Equal(t, "deadline_passed", p.Code)
	require.NoError(t, mock.ExpectationsWereMet(), "nothing may reach the database")
	require.Len(t, rec.standalone, 1)
	assert.Equal(t, audit.ResultFailure, rec.standalone[0].Result)
}

func TestRegister_DeadlineWinsOverValidation(t *testing.T) {
	svc, _, _ := newSvc(t, fakeGate{err: problemFor(http.StatusForbidden, "deadline_passed")}, fakeHasher{})
	_, err := svc.Register(context.Background(), RegisterInput{}, "")
	assert.Equal(t, "deadline_passed", problemOf(t, err).Code)
}

func TestRegister_InvalidInputNeverHashesOrWrites(t *testing.T) {
	svc, mock, _ := newSvc(t, fakeGate{}, fakeHasher{err: errors.New("must not be called")})
	in := validInput()
	in.EmployeeID = "12ab56"
	_, err := svc.Register(context.Background(), in, "")
	p := problemOf(t, err)
	assert.Equal(t, CodeValidationFailed, p.Code)
	assert.Equal(t, []platform.FieldError{{Field: "employeeId", Code: "invalid_format"}}, p.Errors)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRegister_DatabaseErrorIsNotLeaked(t *testing.T) {
	svc, mock, _ := newSvc(t, fakeGate{}, fakeHasher{})
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `user`").WillReturnError(errors.New("connection refused to db.internal:3306"))
	mock.ExpectRollback()
	_, err := svc.Register(context.Background(), validInput(), "")
	require.Error(t, err)
	assert.Equal(t, "internal_error", platform.AsProblem(err).Code)
	assert.NotContains(t, platform.AsProblem(err).Detail, "db.internal")
}

// --- hasher ------------------------------------------------------------

func TestArgon2idHasher(t *testing.T) {
	h := NewArgon2idHasher()
	a, err := h.Hash(context.Background(), "S3cret-pass!")
	require.NoError(t, err)
	b, err := h.Hash(context.Background(), "S3cret-pass!")
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(a, "$argon2id$v=19$m=19456,t=2,p=1$"))
	assert.NotEqual(t, a, b, "salt is random per hash")
	assert.NotContains(t, a, "S3cret")

	ok, err := verifyPassword("S3cret-pass!", a)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = verifyPassword("wrong", a)
	require.NoError(t, err)
	assert.False(t, ok)
	_, err = verifyPassword("x", "plaintext")
	assert.Error(t, err)
}

func TestArgon2idHasher_HonoursCancelledContext(t *testing.T) {
	h := &Argon2idHasher{sem: make(chan struct{}, 1)}
	h.sem <- struct{}{} // pool exhausted
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := h.Hash(ctx, "pw")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// --- HTTP handler ------------------------------------------------------

func serve(t *testing.T, gate DeadlineGate, mock func(sqlmock.Sqlmock), body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc, m, _ := newSvc(t, gate, fakeHasher{})
	if mock != nil {
		mock(m)
	}
	r := gin.New()
	r.POST("/api/v1/auth/register", NewHandler(svc, quiet()).Register)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

const goodBody = `{"employeeId":"123456","fullName":"王小明","department":"TSID","password":"S3cret-pass!","preferredLanguage":"zh-TW","email":"ming@example.com"}`

func TestHandler_Created(t *testing.T) {
	w := serve(t, fakeGate{}, func(m sqlmock.Sqlmock) {
		m.ExpectBegin()
		m.ExpectExec("INSERT INTO `user`").WillReturnResult(sqlmock.NewResult(1, 1))
		m.ExpectCommit()
	}, goodBody)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.JSONEq(t, `{"employeeId":"123456","fullName":"王小明","department":"TSID","preferredLanguage":"zh-TW","email":"ming@example.com"}`, w.Body.String())
	assert.NotContains(t, w.Body.String(), "assword")
}

func TestHandler_Problems(t *testing.T) {
	t.Run("validation lists fields", func(t *testing.T) {
		w := serve(t, fakeGate{}, nil, `{"employeeId":"12","fullName":"A","department":"TSID","password":"x","preferredLanguage":"en"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, platform.ProblemContentType, w.Header().Get("Content-Type"))
		assert.JSONEq(t, `{"title":"Bad Request","status":400,"code":"validation_failed","detail":"One or more fields are invalid.",
			"instance":"/api/v1/auth/register","errors":[{"field":"employeeId","code":"invalid_format"}]}`, w.Body.String())
	})
	t.Run("malformed json", func(t *testing.T) {
		w := serve(t, fakeGate{}, nil, `{nope`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), `"code":"invalid_request"`)
	})
	t.Run("oversized body", func(t *testing.T) {
		w := serve(t, fakeGate{}, nil, `{"fullName":"`+strings.Repeat("a", maxRegisterBody)+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
	t.Run("deadline", func(t *testing.T) {
		w := serve(t, fakeGate{err: problemFor(http.StatusForbidden, "deadline_passed")}, nil, goodBody)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), `"code":"deadline_passed"`)
	})
	t.Run("duplicate", func(t *testing.T) {
		w := serve(t, fakeGate{}, func(m sqlmock.Sqlmock) {
			m.ExpectBegin()
			m.ExpectExec("INSERT INTO `user`").WillReturnError(&mysql.MySQLError{Number: 1062})
			m.ExpectRollback()
		}, goodBody)
		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Contains(t, w.Body.String(), `"code":"employee_id_taken"`)
	})
}

// --- helpers -----------------------------------------------------------

func problemFor(status int, code string) *platform.Problem {
	return platform.NewProblem(status, code, "x")
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
