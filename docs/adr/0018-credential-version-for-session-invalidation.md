# ADR 0018: Credential version in the session to invalidate other sessions on password change

| Item | Value |
|---|---|
| Status | Proposed |
| Date | 2026-11-05 |
| Deciders | Architect (to confirm); proposed by the developer of CUR-62 |
| Related | PRD AUTH-005, AUTH-007, REG-007; Architecture 7.5, 9.1; ADR 0007 |

> Numbered 0018 because 0016 and 0017 are taken. It supplements ADR 0007 (accepted ADRs are not edited) and does not replace it.

## Context

AUTH-007 requires that after a password change all other sessions of that user are invalidated. ADR 0007 chose to do this by iterating the `scs` store (`Iterate`), decoding each session and destroying those with the same user id.

A sweep after the fact has a race. `Iterate` reads a snapshot of the store. A login on another device that stores its session after the snapshot, but whose password check ran against the old hash, survives the sweep and keeps working with a credential the user just revoked. The window is small, but this is the exact scenario AUTH-007 exists for (a leaked password), so an attacker who is logging in at that moment is the case to cover.

## Decision

**1. A credential version on the user.** `user.credential_version` (`INT UNSIGNED NOT NULL DEFAULT 0`, migration 000004). It is incremented in the same transaction that writes the new password hash. The update is conditional on the version that was read, so two simultaneous changes cannot both succeed.

**2. The session carries the version it was issued under.** Login stores the version it read together with the hash it verified. `CurrentUser` compares the session's version with the user's current one on every request; a mismatch destroys the session and answers `401 unauthenticated`. A session created concurrently with a password change was verified against the old hash and so holds the old version: it is rejected on its first request, whatever the order of events.

**3. The sweep stays, as cleanup.** After the commit the service still iterates the store and destroys the user's other sessions (ADR 0007), so they disappear at once instead of at their next request. The version check is the guarantee; the sweep is best effort. If the sweep fails, the error is logged and the password change still succeeds (`204`), because the old sessions are already dead.

**4. The caller's own session is kept and updated** to the new version. Its token is **not** renewed: `RenewToken` also resets the absolute 8 hour deadline, which would let a password change extend a session past AUTH-005.

## Alternatives considered

- **Sweep only (ADR 0007 as written).** Simple, but leaves the race above.
- **`password_changed_at` timestamp compared with the session's issue time.** Needs a trustworthy session issue time and clock agreement; a counter has neither problem.
- **Custom session store with an indexed `user_id` column.** Makes the sweep a single `DELETE` but not atomic with the password write either, and replaces a library component. Still possible later (ADR 0007 consequences).
- **Fail the request when the sweep fails.** The password has already changed, so the client would see an error for a done change, and retrying would fail the current-password check.

## Consequences

- One extra column and one integer comparison per authenticated request, on the row `CurrentUser` already reads.
- Any future code that changes credentials (an engineer's reset, `cmd/hashpw` followed by an SQL update) must also bump `credential_version` to kill old sessions. The engineer procedure for AUTH-010 should say so.
- Sessions issued before this migration have no stored version; they are read as 0, which equals the column default, so they stay valid until their first password change.
- Admin provisioning (ADM-004) inserts with the default of 0.
