# ADR 0013: Admin accounts provisioned from a configured file only

| Item | Value |
|---|---|
| Status | Proposed |
| Date | 2026-11-05 |
| Deciders | Architect (to confirm); proposed by the developer of CUR-63 |
| Related | PRD R-01…R-04, ADM-004, AUTH-008, AUD-001; Architecture 8.6, 9.1; ADR 0019 |

## Context

Admins are created only through the secure configuration store (R-03). The application has no way to create, promote, demote or delete one. In production Vault renders `/vault/secrets/admins.json`; in development it is a local untracked file. The path is `ADMINS_FILE`.

## Decision

- **When.** `auth.SeedAdmins` runs once at startup in `main.go`, after the database ping and before the HTTP server listens. Any error stops the process.
- **File.** A JSON array of `{employeeId, fullName, email, passwordHash}`. Only a hash is accepted (made by `cmd/hashpw`); the decoder rejects unknown fields, so a pasted `password` key fails the file. The hash must be a well-formed Argon2id PHC string with bounded parameters, so a typo cannot make each login allocate gigabytes. Duplicate IDs in the file fail it. Everything is validated before the database is touched. Errors never echo values.
- **Idempotent, never overwrites.** Each entry is `INSERT … ON DUPLICATE KEY UPDATE id = id`. An existing Admin keeps its hash, name and flags, so a password the Admin changed is not reset by the next restart, and instances starting together are safe: the loser of a race is a no-op.
- **Conflict.** When the ID exists and its role is not `ADMIN`, startup fails with `ErrAdminConflict` naming the ID (ADM-004, R-01, R-04). All entries share one transaction, so a failure seeds nothing.
- **Forced change.** New Admins get `must_change_password = TRUE`. Login copies the flag into the session; `auth.RequirePasswordChange` then answers `403 password_change_required` to everything except `GET /auth/me`, `POST /auth/password` and `POST /auth/logout` (deny by default, so later routes are covered). A successful password change clears the flag in the database and the session. Other sessions of the user die through ADR 0019.
- **Audit.** One `admin.provision` record per created account, with no actor and no hash (AUD-001, AUD-006).
- **R-04 on the Participant side** needs no code: registering an Admin's ID hits the unique index and returns `employee_id_taken`.

## Consequences

- Removing an Admin from the file does nothing; demotion or deletion is an engineer's database procedure (ADM-005).
- Rotating an Admin's password through the file does not work for the same reason. Use the engineer reset procedure and bump `credential_version` (ADR 0019).
- The session copy of the flag means an engineer setting `must_change_password` on a user with a live session takes effect at that user's next login.
