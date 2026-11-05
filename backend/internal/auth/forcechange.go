package auth

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// BoolSessionReader is the part of the session manager the gate needs.
type BoolSessionReader interface {
	GetBool(ctx context.Context, key string) bool
}

// RequirePasswordChange enforces AUTH-008: while a session is flagged
// must-change-password, every route is refused with 403 password_change_required except
// the ones the user needs to get out of that state (who am I, change password, log out).
// It is a deny-by-default list: a route added later is blocked without anyone remembering
// to block it. Register it inside the session middleware, after CSRF.
func RequirePasswordChange(sessions BoolSessionReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !sessions.GetBool(c.Request.Context(), keyMustChange) || passwordChangeAllowed(c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}
		platform.WriteProblem(c.Writer, c.Request.URL.Path, ErrPasswordChangeRequired())
		c.Abort()
	}
}

// Paths mirror api/openapi.yaml; main.go registers them under /api/v1.
func passwordChangeAllowed(method, path string) bool {
	switch {
	case method == http.MethodGet && path == "/api/v1/auth/me":
		return true
	case method == http.MethodPost && (path == "/api/v1/auth/password" || path == "/api/v1/auth/logout"):
		return true
	}
	return false
}
