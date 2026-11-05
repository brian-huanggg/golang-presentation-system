package auth

import (
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
)

// SessionCookieName is also the name of the securityScheme in openapi.yaml.
const SessionCookieName = "esg_session"

// Session data keys. The session holds only identity; CSRF is a later ticket.
const (
	keyUserID     = "userID"
	keyEmployeeID = "employeeID"
	keyRole       = "role"
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
