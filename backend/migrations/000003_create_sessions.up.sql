-- Session store for alexedwards/scs (mysqlstore), ADR 0007. The schema must match what the
-- library expects, so do not rename or retype these columns. `data` is an opaque encoded
-- blob (no user_id column); `expiry` is read by the store's cleanup goroutine.
CREATE TABLE sessions (
    token  CHAR(43)     NOT NULL,
    data   BLOB         NOT NULL,
    expiry TIMESTAMP(6) NOT NULL,
    PRIMARY KEY (token),
    KEY sessions_expiry_idx (expiry)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
