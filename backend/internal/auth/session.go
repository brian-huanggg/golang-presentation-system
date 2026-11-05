package auth

import (
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// SessionCookieName is also the name of the securityScheme in openapi.yaml.
const SessionCookieName = "esg_session"

// Session data keys: identity plus the per-session CSRF token (AUTH-009, ADR 0007).
const (
	keyUserID     = "userID"
	keyEmployeeID = "employeeID"
	keyRole       = "role"
	// keyCredVersion is the user's credential_version when the session was issued
	// (ADR 0019). A password change moves the user's value on, which kills the session.
	keyCredVersion = "credVersion"
	// keyMustChange mirrors user.must_change_password for the gate (AUTH-008), so the
	// middleware needs no database read per request.
	keyMustChange = "mustChange"
	keyCSRF       = platform.CSRFSessionKey // shared with the platform middleware that reads it
)

// NewSessionManager builds the scs manager (ADR 0007). The store is injected so
// main.go passes the MariaDB store and tests pass an in-memory one.
//
// scs semantics that AUTH-005 depends on:
//   - Lifetime is absolute: the deadline is fixed when the session is created or
//     its token renewed (login) and is never extended by activity.
//   - With IdleTimeout > 0 every request that loads a session re-commits it with
//     expiry = min(deadline, now+IdleTimeout), which is the sliding idle window.
//
// The cookie is a browser-session cookie (no Max-Age); the server-side expiry is
// what actually ends a session, so a stolen or retained cookie is useless after it.
func NewSessionManager(store scs.Store, idleTimeout, lifetime time.Duration) *scs.SessionManager {
	sm := scs.New()
	sm.Store = store
	sm.IdleTimeout = idleTimeout
	sm.Lifetime = lifetime
	sm.Cookie.Name = SessionCookieName
	sm.Cookie.HttpOnly = true
	sm.Cookie.Secure = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Path = "/"
	sm.Cookie.Persist = false
	return sm
}
