package auth

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

const logoutPath = "/api/v1/auth/logout"

func TestAUTH_009_LoginIssuesAReadableTokenCookieMatchingTheSession(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c, token := e.loginWithCSRF(t)

	assert.GreaterOrEqual(t, len(token), 43)
	// The header is accepted only because it equals the token held in the session.
	w := e.doCSRF(http.MethodPost, logoutPath, "", c, token)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestAUTH_009_LogoutWithoutTheHeaderIsRejectedAndKeepsTheSession(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)

	w := e.do(http.MethodPost, logoutPath, "", c)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), platform.CodeCSRFInvalid)
	assert.Equal(t, http.StatusOK, e.me(c).Code, "a forged logout must not end the session")
}

func TestAUTH_009_WrongHeaderIsRejected(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c := e.login(t)

	w := e.doCSRF(http.MethodPost, logoutPath, "", c, "not-the-token")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAUTH_009_EachLoginGetsANewToken(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	_, first := e.loginWithCSRF(t)
	_, second := e.loginWithCSRF(t)
	assert.NotEqual(t, first, second)
}

func TestAUTH_009_OneSessionsTokenDoesNotWorkForAnother(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c1, _ := e.loginWithCSRF(t)
	_, token2 := e.loginWithCSRF(t)

	w := e.doCSRF(http.MethodPost, logoutPath, "", c1, token2)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAUTH_009_LogoutClearsTheTokenCookie(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c, token := e.loginWithCSRF(t)

	w := e.doCSRF(http.MethodPost, logoutPath, "", c, token)
	require.Equal(t, http.StatusNoContent, w.Code)
	cleared := csrfCookie(t, w)
	assert.Empty(t, cleared.Value)
	assert.Negative(t, cleared.MaxAge)
}

func TestAUTH_009_LoginAndLogoutWithoutASessionNeedNoToken(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	// Login is exercised by every other test without a header; logout is idempotent without a session.
	w := e.do(http.MethodPost, logoutPath, "", nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestAUTH_009_MeRestoresALostTokenCookie(t *testing.T) {
	e := newEnv(t, 30*time.Minute, 8*time.Hour)
	c, token := e.loginWithCSRF(t)

	w := e.me(c) // request carries only the session cookie, as after the browser dropped csrf_token
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, token, csrfCookie(t, w).Value)
}
