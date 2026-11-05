# ADR 0017: In-app per-IP rate limit on `/api/v1/auth/*` instead of a reverse proxy

| Item | Value |
|---|---|
| Status | Proposed |
| Date | 2026-11-05 |
| Deciders | Architect (to confirm); proposed by the developer of CUR-60 |
| Related | PRD AUTH-002, AUTH-003, AUTH-004, AUTH-010; Architecture 9.1, 13; ADR 0003, 0007 |

> Numbered 0017 because 0004, 0006, 0008…0013 and 0015 are reserved in Architecture Section 12 and 0016 is taken. Architecture Section 12 does not list this ADR yet.

## Context

AUTH-004 requires that repeated login attempts from the same network source are rate-limited, independent of the per-account lockout (AUTH-002). The lockout alone does not cover it: one address can try one password against many accounts and never lock any of them.

The original plan was to enforce this at the reverse proxy (Traefik). DevOps and operations are deferred (PRD Section 9, Architecture OI-04), so no proxy exists in this phase. Architecture 9.1 already names an in-process limiter (`golang.org/x/time/rate`) for this requirement; this ADR records the concrete design and that it replaces the proxy.

The limiter is only as good as the client address it keys on. Gin takes `X-Forwarded-For` from any peer unless told which proxies to trust, so an unconfigured server lets a client pick a new "address" per request.

## Decision

**1. A Gin middleware in `platform`** (`RateLimitByIP`, `platform/ratelimit.go`). It holds no business rule, so it fits `platform` (ADR 0003, no new import edge). It is registered once on the router in `main.go` and acts only on paths under `/api/v1/auth/`. This covers register, login, logout and `/auth/me`, and any future auth route, without each route opting in.

**2. Token bucket per client address**, from `x/time/rate`. Defaults: burst 10, refill one token per 6 s (10 per minute sustained). Both come from configuration (`AUTH_RATE_LIMIT_BURST`, `AUTH_RATE_LIMIT_REFILL`) and are validated at startup. The key is `c.ClientIP()`; the port is not part of it.

**3. Rejection** is `429 Too Many Requests`, an RFC 9457 problem with code `rate_limited` (English text, I18N-003) and a `Retry-After` header. The response is the same for every endpoint and says nothing about accounts, so AUTH-003 is not weakened. The OpenAPI contract gets a shared `RateLimited` response on the four auth operations.

**4. Trusted proxies come from `TRUSTED_PROXIES`** (comma-separated IPs or CIDRs), passed to `router.SetTrustedProxies`. The default is empty: no proxy is trusted and the key is the TCP peer address. Entries are validated at startup. The same setting fixes the audit `source_ip`, which uses the same `ClientIP()`.

**5. Bounded memory without a scheduler.** Idle buckets are removed lazily, inside the request path, at most once a minute, once a client has been silent long enough to have a full bucket again. Forgetting such a bucket changes nothing for that client. There is no goroutine (Architecture 1.3, 5.4).

**6. Time comes from `platform.Clock`**, so tests move time without sleeping (ADR 0014).

## Alternatives considered

- **Traefik (or another proxy) rate-limit middleware.** The original plan. It needs a proxy in front of the app, which is a DevOps topic that is deferred. It would also need the proxy to be the only path to the backend.
- **Limit in a database table or the session store.** Shared across instances, but it adds a write to every auth request, including attacker traffic, and a failed-attempt table that needs purging (no scheduler).
- **A third-party Gin rate-limit package.** Saves little code over about 60 lines on `x/time/rate`, and adds a dependency to audit.
- **Only the per-account lockout.** Does not meet AUTH-004 (see Context).
- **Limit by `Forwarded` header always.** Rejected: forgeable (see Context).

## Consequences

- **The limit is per process.** With N instances the effective limit is up to N times higher. This is the risk already listed in Architecture 13 and is acceptable at ~25,000 participants. A shared limiter is a later option; it changes only the `allow` method.
- **State is lost on restart.** An attacker can reset the counters by crashing the process, which they cannot normally do. Accepted.
- **Shared addresses share a bucket.** People behind one office NAT count as one client. Burst 10 is meant to let a group of employees log in together, but a deadline rush from one site could hit it. If so, raise `AUTH_RATE_LIMIT_BURST`; no code change is needed.
- **`TRUSTED_PROXIES` is a correctness setting for the limit.** When a proxy is added later and the value is left empty, all clients appear as the proxy address and share one bucket. If the value is too wide, clients can forge addresses. Whoever adds the proxy must set it; this belongs in the later DevOps phase's deployment notes.
- IPv6 clients are keyed by full address, so one user with a /64 can rotate addresses. Accepted for now; keying by prefix is a possible later change.
- Rate-limited requests are logged at `warn` with the address and path, but are **not** written to the audit log: AUD-001 does not list them, and a flood would fill the append-only table. This is a deliberate choice; add it to AUD-001 first if wanted.
- The frontend should show a generic "try again later" message for code `rate_limited` and honour `Retry-After`. That is outside this ticket.
- `golang.org/x/time` becomes a direct dependency. It is pinned to v0.11.0 because newer releases require a Go version newer than the module's `go 1.24`.
