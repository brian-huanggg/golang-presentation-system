package platform

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testSessionKey struct{}

// fakeSession stands in for the scs manager: the "session token" travels in the
// request context so each case chooses its session directly.
type fakeSession struct{}

func (fakeSession) GetString(ctx context.Context, key string) string {
	if key != CSRFSessionKey {
		return ""
	}
	v, _ := ctx.Value(testSessionKey{}).(string)
	return v
}

func csrfRouter() (*gin.Engine, *int) {
	gin.SetMode(gin.TestMode)
	hits := 0
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if tok := c.GetHeader("X-Test-Session-Token"); tok != "" {
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), testSessionKey{}, tok))
		}
	})
	r.Use(CSRF(fakeSession{}, slog.New(slog.NewTextHandler(io.Discard, nil))))
	h := func(c *gin.Context) { hits++; c.Status(http.StatusNoContent) }
	r.GET("/x", h)
	r.HEAD("/x", h)
	r.OPTIONS("/x", h)
	r.POST("/x", h)
	r.PUT("/x", h)
	r.PATCH("/x", h)
	r.DELETE("/x", h)
	return r, &hits
}

func csrfDo(r http.Handler, method, session, header, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/x", nil)
	if session != "" {
		req.Header.Set("X-Test-Session-Token", session)
	}
	if header != "" {
		req.Header.Set(CSRFHeaderName, header)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: cookie})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAUTH_009_SafeMethodsNeedNoToken(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		r, hits := csrfRouter()
		w := csrfDo(r, m, "tok", "", "tok")
		assert.Equal(t, http.StatusNoContent, w.Code, m)
		assert.Equal(t, 1, *hits, m)
	}
}

func TestAUTH_009_StateChangingRequestWithSessionNeedsMatchingHeader(t *testing.T) {
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for name, header := range map[string]string{"missing": "", "wrong": "other-token", "prefix": "to"} {
			r, hits := csrfRouter()
			w := csrfDo(r, m, "tok", header, "tok")
			assert.Equal(t, http.StatusForbidden, w.Code, m+" "+name)
			assert.Equal(t, 0, *hits, "handler must not run: "+m+" "+name)
			assert.Equal(t, ProblemContentType, w.Header().Get("Content-Type"))
			assert.Contains(t, w.Body.String(), `"code":"`+CodeCSRFInvalid+`"`)
		}
		r, hits := csrfRouter()
		w := csrfDo(r, m, "tok", "tok", "tok")
		assert.Equal(t, http.StatusNoContent, w.Code, m)
		assert.Equal(t, 1, *hits, m)
	}
}

func TestAUTH_009_CookieAloneIsNotEnoughTheSessionDecides(t *testing.T) {
	// An attacker who plants a cookie and echoes it in the header still lacks the session's token.
	r, hits := csrfRouter()
	w := csrfDo(r, http.MethodPost, "real", "forged", "forged")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, 0, *hits)
}

func TestAUTH_009_RequestsWithoutASessionAreNotChecked(t *testing.T) {
	// Register and login have no session yet (ticket decision: simplified scope).
	r, hits := csrfRouter()
	w := csrfDo(r, http.MethodPost, "", "", "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, 1, *hits)
}

func TestAUTH_009_MissingCookieIsReissuedFromTheSession(t *testing.T) {
	r, _ := csrfRouter()
	w := csrfDo(r, http.MethodGet, "tok", "", "")
	var got *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == CSRFCookieName {
			got = c
		}
	}
	require.NotNil(t, got)
	assert.Equal(t, "tok", got.Value)
}

func TestAUTH_009_MatchingCookieIsNotReissued(t *testing.T) {
	r, _ := csrfRouter()
	w := csrfDo(r, http.MethodGet, "tok", "", "tok")
	assert.Empty(t, w.Result().Cookies())
}

func TestAUTH_009_CookieAttributes(t *testing.T) {
	w := httptest.NewRecorder()
	SetCSRFCookie(w, "tok")
	c := w.Result().Cookies()[0]
	assert.Equal(t, CSRFCookieName, c.Name)
	assert.Equal(t, "tok", c.Value)
	assert.False(t, c.HttpOnly, "the SPA must read it to echo it in the header")
	assert.True(t, c.Secure)
	assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
	assert.Equal(t, "/", c.Path)
	assert.Zero(t, c.MaxAge, "browser-session cookie like the session cookie")

	w = httptest.NewRecorder()
	ClearCSRFCookie(w)
	c = w.Result().Cookies()[0]
	assert.Equal(t, CSRFCookieName, c.Name)
	assert.Negative(t, c.MaxAge)
}

func TestAUTH_009_TokensAreRandomAndLongEnough(t *testing.T) {
	a, err := NewCSRFToken()
	require.NoError(t, err)
	b, err := NewCSRFToken()
	require.NoError(t, err)
	assert.NotEqual(t, a, b)
	assert.GreaterOrEqual(t, len(a), 43, "32 bytes base64url")
}
