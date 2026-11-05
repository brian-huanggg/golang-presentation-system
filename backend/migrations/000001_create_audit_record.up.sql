-- Append-only audit trail (AUD-001..007). Times are stored in UTC and converted to
-- Asia/Taipei only for display (I18N-004). The application never updates or deletes
-- rows; scripts/restrict-audit-grants.sh enforces that at the DB level (AUD-003).
CREATE TABLE audit_record (
    id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    occurred_at       DATETIME(6)     NOT NULL,
    -- NULL when there is no known actor (failed login for an unknown ID, system actions).
    actor_employee_id VARCHAR(64)     NULL,
    actor_role        VARCHAR(16)     NULL,
    action            VARCHAR(64)     NOT NULL,
    target_type       VARCHAR(32)     NULL,
    target_id         VARCHAR(64)     NULL,
    result            VARCHAR(16)     NOT NULL,
    -- 45 chars fits the longest textual IPv6 form.
    source_ip         VARCHAR(45)     NULL,
    changes           JSON            NULL,
    PRIMARY KEY (id),
    CONSTRAINT chk_audit_record_result CHECK (result IN ('success', 'failure')),
    KEY idx_audit_record_occurred_at (occurred_at),
    KEY idx_audit_record_actor (actor_employee_id, occurred_at),
    KEY idx_audit_record_target (target_type, target_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
