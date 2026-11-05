package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alexedwards/scs/v2"
	"github.com/alexedwards/scs/v2/memstore"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
)

const loginPassword = "S3cret-pass!"

var (
	hashOnce  sync.Once
	storedPHC string
)

// storedHash is Argon2id of loginPassword, computed once: hashing is deliberately slow.
func storedHash(t *testing.T) string {
	t.Helper()
	hashOnce.Do(func() {
		h, err := NewArgon2idHasher().Hash(context.Background(), loginPassword)
		require.NoError(t, err)
		storedPHC = h
	})
	return storedPHC
}

var userCols = []string{"id", "employee_id", "full_name", "department", "role", "preferred_language", "password_hash", "must_change_password"}

// env is a router wired like production: Gin inside the scs middleware, real
// Argon2id, real scs session logic over an in-memory store, mocked SQL.
type env struct {
	t     *testing.T
	mock  sqlmock.Sqlmock
	rec   *recorder
	sm    *scs.SessionManager
	store *memstore.MemStore
	h     http.Handler
}

func newEnv(t *testing.T, idle, lifetime time.Duration) *env {
	t.Helper()
	gin.SetMode(gin.TestMode)
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })

	store := memstore.New()
	sm := NewSessionManager(store, idle, lifetime)
	rec := &recorder{}
	svc := NewService(sqlx.NewDb(raw, "mysql"), fakeGate{}, NewArgon2idHasher(), sm, rec, quiet())
	h := NewHandler(svc, quiet())

	r := gin.New()
	r.POST("/api/v1/auth/login", h.Login)
	r.POST("/api/v1/auth/logout", h.Logout)
	r.GET("/api/v1/auth/me", h.GetCurrentUser)
	return &env{t: t, mock: mock, rec: rec, sm: sm, store: store, h: sm.LoadAndSave(r)}
}

func (e *env) do(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	return w
}

func (e *env) expectUser(role string, mustChange bool) {
	var dept any = "TSID"
	if role == "ADMIN" {
		dept = nil
	}
	e.mock.ExpectQuery("SELECT id, employee_id").WithArgs("123456").
		WillReturnRows(sqlmock.NewRows(userCols).AddRow(7, "123456", "王小明", dept, role, "zh-TW", storedHash(e.t), mustChange))
}

func (e *env) expectUserByID() {
	e.mock.ExpectQuery("SELECT id, employee_id").WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows(userCols).AddRow(7, "123456", "王小明", "TSID", "PARTICIPANT", "zh-TW", storedHash(e.t), false))
}

func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == SessionCookieName {
			return c
		}
	}
	t.Fatalf("no %s cookie in response", SessionCookieName)
	return nil
}

func (e *env) login(t *testing.T) *http.Cookie {
	t.Helper()
	e.expectUser("PARTICIPANT", false)
	w := e.do(http.MethodPost, "/api/v1/auth/login", `{"employeeId":"123456","password":"`+loginPassword+`"}`, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	return sessionCookie(t, w)
}

func (e *env) me(c *http.Cookie) *httptest.ResponseRecorder {
	e.expectUserByID() // consumed only when a session is live
	return e.do(http.MethodGet, "/api/v1/auth/me", "", c)
}

// --- AUTH-001: Employee ID + password, never email ----------------------

func TestAUTH_001_LoginReturnsUserAndNeverTheHash(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	e.expectUser("PARTICIPANT", false)
	w := e.do(http.MethodPost, "/api/v1/auth/login", `{"employeeId":"123456","password":"`+loginPassword+`"}`, nil)

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"employeeId":"123456","fullName":"王小明","role":"PARTICIPANT","department":"TSID","preferredLanguage":"zh-TW","mustChangePassword":false}`, w.Body.String())
	assert.NotContains(t, w.Body.String(), "argon2")
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Len(t, e.rec.standalone, 1)
	got := e.rec.standalone[0]
	assert.Equal(t, "auth.login", got.Action)
	assert.Equal(t, audit.ResultSuccess, got.Result)
	assert.Equal(t, "123456", got.ActorEmployeeID)
	require.NoError(t, e.mock.ExpectationsWereMet())
}

func TestAUTH_001_AdminLoginHasNoDepartmentAndFlagsPasswordChange(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	e.expectUser("ADMIN", true)
	w := e.do(http.MethodPost, "/api/v1/auth/login", `{"employeeId":"123456","password":"`+loginPassword+`"}`, nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"employeeId":"123456","fullName":"王小明","role":"ADMIN","preferredLanguage":"zh-TW","mustChangePassword":true}`, w.Body.String())
}

func TestAUTH_001_EmailIsNotALoginIdentifier(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	// No query is expected: an email never reaches the database.
	w := e.do(http.MethodPost, "/api/v1/auth/login", `{"employeeId":"ming@example.com","password":"`+loginPassword+`"}`, nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"invalid_credentials"`)
	require.NoError(t, e.mock.ExpectationsWereMet())
	assert.Empty(t, w.Result().Cookies(), "a failed login must not set a session cookie")
}

// --- AUTH-003: failures are indistinguishable ---------------------------

func TestAUTH_003_UnknownIDWrongPasswordAndMalformedIDLookIdentical(t *testing.T) {
	cases := map[string]struct {
		body   string
		expect func(*env)
		reason string
	}{
		"unknown id": {`{"employeeId":"999999","password":"whatever-Pass1!"}`, func(e *env) {
			e.mock.ExpectQuery("SELECT id, employee_id").WithArgs("999999").WillReturnRows(sqlmock.NewRows(userCols))
		}, "unknown_user"},
		"wrong password": {`{"employeeId":"123456","password":"Wrong-pass-9!"}`, func(e *env) { e.expectUser("PARTICIPANT", false) }, "bad_password"},
		"malformed id":   {`{"employeeId":"12 456","password":"x"}`, func(*env) {}, "invalid_employee_id"},
		"oversize pw":    {`{"employeeId":"123456","password":"` + strings.Repeat("a", 500) + `"}`, func(e *env) { e.expectUser("PARTICIPANT", false) }, "bad_password"},
	}
	var first string
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, 30*time.Minute, 8*time.Hour)
			tc.expect(e)
			w := e.do(http.MethodPost, "/api/v1/auth/login", tc.body, nil)

			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Contains(t, w.Body.String(), `"detail":"Invalid Employee ID or password"`)
			assert.Empty(t, w.Result().Cookies())
			// Same body modulo the request path (identical here), so nothing distinguishes the cases.
			if first == "" {
				first = w.Body.String()
			}
			assert.Equal(t, first, w.Body.String())

			require.Len(t, e.rec.standalone, 1, "every failure is audited (AUD-001)")
			f := e.rec.standalone[0]
			assert.Equal(t, audit.ResultFailure, f.Result)
			assert.Equal(t, map[string]any{"reason": tc.reason}, f.Changes)
			assert.Empty(t, f.ActorEmployeeID, "the claimant is not an authenticated actor")
			require.NoError(t, e.mock.ExpectationsWereMet())
		})
	}
}

func TestAUTH_003_LoginBodyProblems(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	w := e.do(http.MethodPost, "/api/v1/auth/login", `{nope`, nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"invalid_request"`)
	w = e.do(http.MethodPost, "/api/v1/auth/login", `{"employeeId":"123456","password":"`+strings.Repeat("a", maxLoginBody)+`"}`, nil)
	assert.Equal(t, http.StatusBadRequest, w.Code, "oversized body")
}

func TestAUTH_003_CorruptStoredHashIsAServerError(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	e.mock.ExpectQuery("SELECT id, employee_id").WithArgs("123456").
		WillReturnRows(sqlmock.NewRows(userCols).AddRow(7, "123456", "x", "TSID", "PARTICIPANT", "en", "plaintext", false))
	w := e.do(http.MethodPost, "/api/v1/auth/login", `{"employeeId":"123456","password":"x"}`, nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "plaintext")
}

func TestAUTH_003_LookupErrorIsNotLeaked(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	e.mock.ExpectQuery("SELECT id, employee_id").WillReturnError(errors.New("dial db.internal:3306 refused"))
	w := e.do(http.MethodPost, "/api/v1/auth/login", `{"employeeId":"123456","password":"x"}`, nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "db.internal")
}

// --- DDL-004: login works after the deadline ----------------------------

func TestDDL_004_LoginIgnoresTheDeadline(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	// newEnv's gate allows everything, so swap in one that would reject any participant action.
	e.h = rebuildWithGate(t, e, fakeGate{err: problemFor(http.StatusForbidden, "deadline_passed")})
	e.expectUser("PARTICIPANT", false)
	w := e.do(http.MethodPost, "/api/v1/auth/login", `{"employeeId":"123456","password":"`+loginPassword+`"}`, nil)
	assert.Equal(t, http.StatusOK, w.Code)
}

func rebuildWithGate(t *testing.T, e *env, gate DeadlineGate) http.Handler {
	t.Helper()
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	e.mock = mock
	svc := NewService(sqlx.NewDb(raw, "mysql"), gate, NewArgon2idHasher(), e.sm, e.rec, quiet())
	r := gin.New()
	r.POST("/api/v1/auth/login", NewHandler(svc, quiet()).Login)
	return e.sm.LoadAndSave(r)
}

// --- AUTH-005: cookie attributes and timeouts ----------------------------

func TestAUTH_005_SessionCookieAttributes(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)

	assert.Equal(t, "esg_session", c.Name)
	assert.True(t, c.HttpOnly, "HttpOnly")
	assert.True(t, c.Secure, "Secure")
	assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
	assert.Equal(t, "/", c.Path)
	assert.NotEmpty(t, c.Value)
	assert.NotContains(t, c.Value, "123456", "the token is opaque")
	assert.Zero(t, c.MaxAge, "a browser-session cookie; the server decides expiry")
}

func TestAUTH_005_ManagerUsesConfiguredTimeouts(t *testing.T) {
	sm := NewSessionManager(memstore.New(), 30*time.Minute, 8*time.Hour)
	assert.Equal(t, 30*time.Minute, sm.IdleTimeout)
	assert.Equal(t, 8*time.Hour, sm.Lifetime)
}

func TestAUTH_005_ValidSessionAuthenticates(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)
	w := e.me(c)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"employeeId":"123456"`)
}

func TestAUTH_005_NoCookieIsUnauthenticated(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	w := e.do(http.MethodGet, "/api/v1/auth/me", "", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"unauthenticated"`)
	require.NoError(t, e.mock.ExpectationsWereMet())
}

func TestAUTH_005_ForgedTokenIsUnauthenticated(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	w := e.do(http.MethodGet, "/api/v1/auth/me", "", &http.Cookie{Name: SessionCookieName, Value: strings.Repeat("A", 43)})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// The timeout tests use short real durations because scs stamps expiry with
// time.Now(), not an injectable clock; the logic under test is unchanged.

func TestAUTH_005_IdleSessionExpires(t *testing.T) {
	e := newEnv(t, 250*time.Millisecond, time.Hour)
	c := e.login(t)
	time.Sleep(400 * time.Millisecond)

	w := e.me(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code, "idle longer than IdleTimeout")
	assert.Contains(t, w.Body.String(), `"code":"unauthenticated"`)
}

func TestAUTH_005_ActivityKeepsSessionAliveWithinIdleWindow(t *testing.T) {
	e := newEnv(t, 300*time.Millisecond, time.Hour)
	c := e.login(t)
	// 4 x 150 ms = 600 ms total, twice the idle timeout, but never idle for more than 150 ms.
	for i := 0; i < 4; i++ {
		time.Sleep(150 * time.Millisecond)
		w := e.me(c)
		require.Equal(t, http.StatusOK, w.Code, "request %d", i)
		if rc := w.Result().Cookies(); len(rc) > 0 {
			c = sessionCookie(t, w) // a refreshed cookie, if the server sent one
		}
	}
}

func TestAUTH_005_AbsoluteLifetimeEndsAnActiveSession(t *testing.T) {
	e := newEnv(t, 300*time.Millisecond, 600*time.Millisecond)
	c := e.login(t)
	// Stay active well inside the idle window, past the absolute limit.
	for i := 0; i < 3; i++ {
		time.Sleep(150 * time.Millisecond)
		require.Equal(t, http.StatusOK, e.me(c).Code, "still within the lifetime at request %d", i)
	}
	time.Sleep(250 * time.Millisecond) // total ~700 ms > Lifetime, never idle > IdleTimeout
	w := e.me(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code, "activity must not extend the absolute lifetime")
}

func TestAUTH_005_UserDeletedWhileLoggedInEndsTheSession(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)
	e.mock.ExpectQuery("SELECT id, employee_id").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows(userCols))
	w := e.do(http.MethodGet, "/api/v1/auth/me", "", c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	// The next request, even if the lookup would now succeed, finds no session.
	assert.Equal(t, http.StatusUnauthorized, e.me(c).Code)
}

func TestAUTH_005_CurrentUserLookupErrorIsNotLeaked(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)
	e.mock.ExpectQuery("SELECT id, employee_id").WillReturnError(errors.New("dial db.internal refused"))
	w := e.do(http.MethodGet, "/api/v1/auth/me", "", c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "db.internal")
}

// --- session fixation ----------------------------------------------------

type orderedSessions struct{ calls []string }

func (o *orderedSessions) RenewToken(context.Context) error {
	o.calls = append(o.calls, "renew")
	return nil
}
func (o *orderedSessions) Put(_ context.Context, k string, _ any) {
	o.calls = append(o.calls, "put:"+k)
}
func (o *orderedSessions) GetInt64(context.Context, string) int64   { return 0 }
func (o *orderedSessions) GetString(context.Context, string) string { return "" }
func (o *orderedSessions) Destroy(context.Context) error {
	o.calls = append(o.calls, "destroy")
	return nil
}

func TestAUTH_005_LoginRenewsTheTokenBeforeStoringIdentity(t *testing.T) {
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	ord := &orderedSessions{}
	svc := NewService(sqlx.NewDb(raw, "mysql"), fakeGate{}, NewArgon2idHasher(), ord, &recorder{}, quiet())
	mock.ExpectQuery("SELECT id, employee_id").WithArgs("123456").
		WillReturnRows(sqlmock.NewRows(userCols).AddRow(7, "123456", "x", "TSID", "PARTICIPANT", "en", storedHash(t), false))

	_, err = svc.Login(context.Background(), "123456", loginPassword, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"renew", "put:userID", "put:employeeID", "put:role"}, ord.calls)
}

type failingAuditor struct{ recorder }

func (failingAuditor) RecordStandalone(context.Context, audit.Entry) error {
	return errors.New("audit down")
}

func TestAUDIT_LoginFailsClosedWhenTheAuditWriteFails(t *testing.T) {
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	ord := &orderedSessions{}
	svc := NewService(sqlx.NewDb(raw, "mysql"), fakeGate{}, NewArgon2idHasher(), ord, &failingAuditor{}, quiet())
	mock.ExpectQuery("SELECT id, employee_id").WithArgs("123456").
		WillReturnRows(sqlmock.NewRows(userCols).AddRow(7, "123456", "x", "TSID", "PARTICIPANT", "en", storedHash(t), false))

	_, err = svc.Login(context.Background(), "123456", loginPassword, "")
	require.Error(t, err)
	assert.Equal(t, "destroy", ord.calls[len(ord.calls)-1], "no unaudited session may survive")
}

// --- AUTH-006: logout invalidates immediately ----------------------------

func TestAUTH_006_LogoutKillsTheSessionImmediately(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)
	require.Equal(t, http.StatusOK, e.me(c).Code)

	w := e.do(http.MethodPost, "/api/v1/auth/logout", "", c)
	assert.Equal(t, http.StatusNoContent, w.Code)
	cleared := sessionCookie(t, w)
	assert.Empty(t, cleared.Value, "cookie is cleared")
	assert.True(t, cleared.MaxAge < 0 || !cleared.Expires.After(time.Now()), "cookie is expired")

	// The captured token is dead server-side, not just forgotten by the browser.
	w = e.me(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	_, found, err := e.store.Find(c.Value)
	require.NoError(t, err)
	assert.False(t, found, "the row is gone from the store")

	last := e.rec.standalone[len(e.rec.standalone)-1]
	assert.Equal(t, "auth.logout", last.Action)
	assert.Equal(t, audit.ResultSuccess, last.Result)
	assert.Equal(t, "123456", last.ActorEmployeeID)
	assert.Equal(t, "PARTICIPANT", last.ActorRole)
}

func TestAUTH_006_LogoutWithoutASessionIsIdempotent(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	w := e.do(http.MethodPost, "/api/v1/auth/logout", "", nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, e.rec.standalone, "nothing to record")

	// An expired/unknown token behaves the same.
	w = e.do(http.MethodPost, "/api/v1/auth/logout", "", &http.Cookie{Name: SessionCookieName, Value: strings.Repeat("B", 43)})
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestAUTH_006_LogoutSucceedsEvenIfTheAuditWriteFails(t *testing.T) {
	raw, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	ord := &logoutSessions{}
	svc := NewService(sqlx.NewDb(raw, "mysql"), fakeGate{}, NewArgon2idHasher(), ord, &failingAuditor{}, quiet())
	require.NoError(t, svc.Logout(context.Background(), ""))
	assert.True(t, ord.destroyed, "invalidation does not depend on the audit trail")
}

type logoutSessions struct {
	orderedSessions
	destroyed bool
}

func (l *logoutSessions) GetInt64(context.Context, string) int64   { return 7 }
func (l *logoutSessions) GetString(context.Context, string) string { return "123456" }
func (l *logoutSessions) Destroy(context.Context) error            { l.destroyed = true; return nil }

func TestAUTH_006_LogoutReportsAStoreFailure(t *testing.T) {
	raw, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	svc := NewService(sqlx.NewDb(raw, "mysql"), fakeGate{}, NewArgon2idHasher(), &brokenStore{}, &recorder{}, quiet())
	err = svc.Logout(context.Background(), "")
	require.Error(t, err, "if the session could not be deleted the user must be told")
}

type brokenStore struct{ logoutSessions }

func (*brokenStore) Destroy(context.Context) error { return errors.New("store down") }

// --- hasher.Verify ---------------------------------------------------------

func TestArgon2idHasher_Verify(t *testing.T) {
	h := NewArgon2idHasher()
	ok, err := h.Verify(context.Background(), loginPassword, storedHash(t))
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = h.Verify(context.Background(), "nope", storedHash(t))
	require.NoError(t, err)
	assert.False(t, ok)
	ok, err = h.Verify(context.Background(), strings.Repeat("a", maxPasswordBytes+1), storedHash(t))
	require.NoError(t, err)
	assert.False(t, ok, "oversized input is rejected without hashing")
}

func TestArgon2idHasher_VerifyHonoursCancelledContext(t *testing.T) {
	h := &Argon2idHasher{sem: make(chan struct{}, 1)}
	h.sem <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := h.Verify(ctx, "pw", storedHash(t))
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
