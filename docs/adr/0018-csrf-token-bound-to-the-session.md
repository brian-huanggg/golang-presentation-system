# ADR 0018: CSRF token bound to the session, echoed in a header

| Item | Value |
|---|---|
| Status | Accepted |
| Date | 2026-11-05 |
| Deciders | Architect |
| Related | PRD AUTH-009; ADR 0007; Architecture 6, 9.1 |

## Context

AUTH-009 requires protection against cross-site request forgery for every state-changing action. Sessions are cookie based (ADR 0007), so a forged cross-site request would carry the session cookie. The SPA is served from the same origin as the API (Architecture 3), and the frontend client already echoes a `csrf_token` cookie in an `X-CSRF-Token` header on every non-safe request.

## Decision

- **Token.** At login, after the session token is renewed, the service stores a 32-byte random token (base64url) in the session under `csrfToken`. Every login creates a new one.
- **Cookie.** The login response also sets cookie `csrf_token` with the same value: `Secure`, `SameSite=Lax`, `Path=/`, browser-session lifetime, and **not `HttpOnly`**, because the SPA must read it to build the header. Logout clears it.
- **Check.** A Gin middleware in `platform` (`platform.CSRF`), registered after the rate limiter and before the routes, runs inside the scs `LoadAndSave` wrapper. For every request whose session holds a token and whose method is not GET, HEAD or OPTIONS, the `X-CSRF-Token` header must equal the session's token (constant-time compare). Otherwise the response is `403` with problem code `csrf_token_invalid` and the handler does not run.
- **The session decides, not the cookie.** The header is compared with the value stored server side. A cookie an attacker plants (for example from a sibling subdomain) is worthless without the session's token, which is what plain double-submit cookies cannot guarantee.
- **Self-healing.** If a request carries a session but the cookie is missing or different, the middleware re-issues it. The SPA calls `/auth/me` on load, so a browser that dropped the cookie recovers without logging in again.
- **Layering.** The middleware depends only on a consumer-side interface (`GetString`) that `*scs.SessionManager` satisfies; `platform` imports no feature (ADR 0003). `auth` owns token creation and the cookie lifecycle.

## Scope decision: requests without a session are not checked

Register and login arrive without a session, so no token exists to compare, and logout without a session changes nothing. They are not checked. There is no logged-in identity to abuse, and `SameSite=Lax` already withholds cookies on cross-site POSTs.

The accepted residue is *login CSRF* (an attacker logs the victim into the attacker's account). Its impact is low here: the victim would have to enter team or Idea data into the wrong account. Closing it needs an anonymous pre-login token (for example `GET /auth/csrf`) and a second code path; do that in a new ADR if the risk assessment changes.

## Alternatives considered

- **Plain double-submit cookie (header equals cookie).** No server state, but forgeable by anyone who can set cookies for the site. We already have a session store, so there is no reason to accept the weaker form.
- **Token only in the page or in a response body.** Needs an extra bootstrap request and a place to keep the token in the SPA; the readable cookie is simpler and already implemented in the client.
- **Origin / Referer checking only.** Useful defence in depth, but absent or stripped headers make it unreliable as the sole control. Not added in this ticket.
- **Pre-login anonymous token.** See the scope decision.

## Consequences

- Every future state-changing route gets protection by default: nothing per route has to be remembered.
- A rejected request is logged with `slog` (IP, method, path) but **not** written to the audit table: `platform` may not import `audit` (ADR 0003), and AUD-001 does not list CSRF rejections. Revisit if the Product Owner wants them audited.
- Password change (AUTH-007, a later ticket) must rotate the token together with the session token.
- The token is readable by script on our own origin; XSS would defeat CSRF protection, which is why the CSP in Architecture 9.4 matters.
