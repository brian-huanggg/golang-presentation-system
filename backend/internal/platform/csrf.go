package platform

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CSRF contract shared with the SPA (frontend/src/api/client.ts) and AUTH-009.
const (
	CSRFCookieName = "csrf_token"
	CSRFHeaderName = "X-CSRF-Token"
	// CSRFSessionKey is the session-data key holding the per-session token (ADR 0007).
	CSRFSessionKey = "csrfToken"
	// CodeCSRFInvalid is the stable problem code for a missing or wrong token.
	CodeCSRFInvalid = "csrf_token_invalid"
)

// SessionReader is the part of *scs.SessionManager the middleware needs; defined
// here, at the consumer (ADR 0003).
type SessionReader interface {
	GetString(ctx context.Context, key string) string
}

// NewCSRFToken returns 32 random bytes, base64url encoded (cookie-safe).
func NewCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CSRF protects every state-changing request of a logged-in session (AUTH-009).
// It compares the X-CSRF-Token header with the token stored in the *session*, not
// with the cookie: a cookie an attacker managed to plant proves nothing.
//
// Requests whose session holds no token (visitors on register/login) are not
// checked: there is no ambient authority to abuse, and SameSite=Lax already stops
// cross-site POSTs. Must run inside the session middleware (scs LoadAndSave).
func CSRF(sessions SessionReader, log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		want := sessions.GetString(c.Request.Context(), CSRFSessionKey)
		if want == "" {
			c.Next()
			return
		}
		// Self-heal: if the browser lost the cookie, the SPA's next GET (/auth/me) restores it.
		if ck, err := c.Request.Cookie(CSRFCookieName); err != nil || ck.Value != want {
			SetCSRFCookie(c.Writer, want)
		}
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		got := c.GetHeader(CSRFHeaderName)
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			log.Warn("csrf token rejected", "ip", c.ClientIP(), "method", c.Request.Method,
				"path", c.Request.URL.Path, "headerPresent", got != "")
			WriteProblem(c.Writer, c.Request.URL.Path,
				NewProblem(http.StatusForbidden, CodeCSRFInvalid, "Missing or invalid CSRF token."))
			c.Abort()
			return
		}
		c.Next()
	}
}

// SetCSRFCookie issues the token cookie. It is deliberately not HttpOnly: the SPA
// echoes it in the header. SameSite=Lax and a browser-session lifetime match the
// session cookie.
func SetCSRFCookie(w http.ResponseWriter, token string) {
	// One csrf_token per response: the middleware's self-heal may already have queued
	// one for the old value, and a password change then sets the rotated one (ADR 0018).
	dropQueuedCSRFCookie(w)
	http.SetCookie(w, &http.Cookie{
		Name: CSRFCookieName, Value: token, Path: "/",
		Secure: true, HttpOnly: false, SameSite: http.SameSiteLaxMode,
	})
}

// ClearCSRFCookie expires the token cookie (logout). It first drops a csrf_token
// Set-Cookie already queued on this response (the middleware's self-heal), so the
// response carries exactly one, the clearing one.
func ClearCSRFCookie(w http.ResponseWriter) {
	dropQueuedCSRFCookie(w)
	http.SetCookie(w, &http.Cookie{
		Name: CSRFCookieName, Value: "", Path: "/", MaxAge: -1,
		Secure: true, HttpOnly: false, SameSite: http.SameSiteLaxMode,
	})
}

// dropQueuedCSRFCookie removes any csrf_token Set-Cookie already queued on the response.
func dropQueuedCSRFCookie(w http.ResponseWriter) {
	h := w.Header()
	var kept []string
	for _, v := range h.Values("Set-Cookie") {
		if !strings.HasPrefix(v, CSRFCookieName+"=") {
			kept = append(kept, v)
		}
	}
	h.Del("Set-Cookie")
	for _, v := range kept {
		h.Add("Set-Cookie", v)
	}
}
