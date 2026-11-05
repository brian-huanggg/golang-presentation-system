# ADR 0007: Sessions in MariaDB with `scs`; no Redis for now

| Item | Value |
|---|---|
| Status | Accepted |
| Date | 2026-11-05 |
| Deciders | Architect |
| Related | PRD AUTH-005, AUTH-006, AUTH-007, AUTH-009; Architecture 3, 9.1 |

## Context

The PRD requires server-controlled sessions:

- 30 minutes of inactivity and 8 hours in total (AUTH-005),
- immediate invalidation on logout (AUTH-006),
- invalidation of a user's *other* sessions after a password change (AUTH-007).

Stateless signed tokens (JWT) cannot satisfy the last two without a server-side list, so a server-side session store is needed. The backend should stay stateless as a process (Architecture 3) and add no components without need. Load is about 25,000 participants (Architecture 1.1).

## Decision

Sessions are stored in **MariaDB** using **`alexedwards/scs`** with its MySQL store (compatible with MariaDB through `go-sql-driver/mysql`).

- Cookie: `HttpOnly`, `Secure`, `SameSite=Lax`. The browser holds only an opaque token; the SPA never sees it.
- `IdleTimeout` = 30 minutes and `Lifetime` = 8 hours (absolute). Both come from configuration with these defaults.
- The token is renewed at login (`RenewToken`) to prevent session fixation.
- The session data holds the internal user id, the role, and the per-session CSRF token (Architecture 9.1).
- The `sessions` table is created by a `golang-migrate` migration (ADR 0006), like any other table.
- **Expired-session cleanup** uses the store's built-in cleanup goroutine. It is library-internal housekeeping and not an application scheduler (Architecture 5.4).
- **Invalidate other sessions (AUTH-007).** The service iterates sessions with `scs` `Iterate`, decodes each, and destroys those with the same user id except the current one.

## Alternatives considered

- **JWT in a cookie or header.** Cannot revoke on logout or password change without a server-side deny list, which is a session store in disguise.
- **Redis session store.** Faster and has TTLs, but adds a component to run, secure, and back up, for a load that MariaDB handles easily.
- **A hand-written session table and middleware.** Reinvents token generation, expiry, and renewal that `scs` already handles well.
- **`gorilla/sessions` (cookie store).** Client-side storage cannot be invalidated by the server.

## Consequences

- No new infrastructure. A session lookup is one primary-key read per request, which is acceptable at this scale.
- The `scs` store keeps session data as an opaque encoded blob, so there is no indexed `user_id` column. `Iterate` on password change therefore scans all live sessions, which is acceptable for a few thousand rows and a rare operation. If it ever becomes slow, replace it with a custom store that has an indexed `user_id` column, via a new ADR.
- The session table is a hot, small table on the same database as business data. Database unavailability means nobody can log in, which was already true.
- Moving to Redis later only changes the `scs` store construction in `main.go`; handlers and services use the `scs` manager interface.
- Lockout counters stay in the `user` table, not the session store (Architecture 9.1), so they survive session expiry.
