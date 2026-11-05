-- Credential version (ADR 0018). Bumped in the same transaction as every password change;
-- a session remembers the value it was issued under and is rejected once the value moves on.
-- This closes the race where a session created while a password change commits would escape
-- the "invalidate other sessions" sweep (AUTH-007). NOT NULL DEFAULT 0 keeps the previous
-- application version working during the rollout (expand-then-contract).
ALTER TABLE `user`
    ADD COLUMN credential_version INT UNSIGNED NOT NULL DEFAULT 0 AFTER password_hash;
