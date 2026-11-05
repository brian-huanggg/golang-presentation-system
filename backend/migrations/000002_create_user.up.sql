-- Participants and Admins (Architecture 7.1). Times are UTC (see 000001).
-- `user` is quoted because it is also a SQL keyword.
CREATE TABLE `user` (
    id                   BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    -- Exactly six digits (REG-002). Unique index = "Employee ID unique" (Architecture 7.2).
    employee_id          CHAR(6)         NOT NULL,
    full_name            VARCHAR(100)    NOT NULL,
    -- Optional, format-checked in the application only; not unique, not a login ID (REG-008).
    email                VARCHAR(254)    NULL,
    -- NULL for Admin accounts. Codes mirror platform.Department (REG-004).
    department           VARCHAR(8)      NULL,
    role                 VARCHAR(16)     NOT NULL,
    -- BCP 47 tags offered by the UI (I18N-001, I18N-002).
    preferred_language   VARCHAR(8)      NOT NULL DEFAULT 'zh-TW',
    -- Argon2id PHC string; 255 leaves room for parameter changes.
    password_hash        VARCHAR(255)    NOT NULL,
    must_change_password BOOLEAN         NOT NULL DEFAULT FALSE,
    -- Lockout state lives here, not in the session store, so it survives session expiry.
    failed_attempts      INT UNSIGNED    NOT NULL DEFAULT 0,
    locked_until         DATETIME(6)     NULL,
    created_at           DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at           DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_user_employee_id (employee_id),
    CONSTRAINT chk_user_employee_id CHECK (employee_id REGEXP '^[0-9]{6}$'),
    CONSTRAINT chk_user_role CHECK (role IN ('PARTICIPANT', 'ADMIN')),
    CONSTRAINT chk_user_department CHECK (department IS NULL OR department IN ('TSID', 'BCID', 'DEID', 'NTAD')),
    -- A Participant always has a department; an Admin has none.
    CONSTRAINT chk_user_role_department CHECK (
        (role = 'PARTICIPANT' AND department IS NOT NULL) OR (role = 'ADMIN' AND department IS NULL)
    ),
    CONSTRAINT chk_user_language CHECK (preferred_language IN ('zh-TW', 'en'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
