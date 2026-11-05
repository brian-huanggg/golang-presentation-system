-- Teams and their roster (Architecture 7.1, 7.2; TEAM-001..008). Times are UTC (see 000001).
-- Integrity that concurrent requests could otherwise break is enforced here, not only in
-- application code: the service translates these unique-key violations into domain errors.
CREATE TABLE team (
    id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    -- The event year the team belongs to (EVT-01); the year comes from configuration (ADR 0014).
    event_year     SMALLINT UNSIGNED NOT NULL,
    -- 2-50 characters (TEAM-004). The collation is case-insensitive, so the unique index below
    -- gives "unique within the year, case-insensitive".
    name           VARCHAR(50)     NOT NULL,
    -- Codes mirror platform.Track / platform.AwardCategory / platform.Department.
    track          VARCHAR(32)     NOT NULL,
    award_category VARCHAR(32)     NOT NULL,
    -- The leader's department at creation (TEAM-003); never changed (TEAM-005).
    department     VARCHAR(8)      NOT NULL,
    created_at     DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at     DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    -- Soft delete (TEAM-015). Hidden immediately; removed by cmd/purge after 7 days.
    deleted_at     DATETIME(6)     NULL,
    -- NULL once the team is soft-deleted, so a dissolved team releases its name at once
    -- (TEAM-015). NULLs never collide in a unique index.
    name_active    VARCHAR(50)     AS (IF(deleted_at IS NULL, name, NULL)) VIRTUAL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_team_name_active (event_year, name_active),
    CONSTRAINT chk_team_name_length CHECK (CHAR_LENGTH(name) >= 2),
    CONSTRAINT chk_team_track CHECK (track IN ('smart_innovation', 'sustainable_development')),
    CONSTRAINT chk_team_award_category CHECK (award_category IN
        ('engineering', 'administration', 'manufacturing_process', 'transportation', 'public_safety')),
    CONSTRAINT chk_team_department CHECK (department IN ('TSID', 'BCID', 'DEID', 'NTAD'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE team_member (
    team_id    BIGINT UNSIGNED NOT NULL,
    user_id    BIGINT UNSIGNED NOT NULL,
    -- Copy of team.event_year so the next constraint can be a plain unique index.
    event_year SMALLINT UNSIGNED NOT NULL,
    role       VARCHAR(8)      NOT NULL,
    created_at DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    -- team_id when the row is the leader, else NULL: a unique index on it means exactly
    -- one leader per team (Architecture 7.2).
    leader_team_id BIGINT UNSIGNED AS (IF(role = 'LEADER', team_id, NULL)) VIRTUAL,
    PRIMARY KEY (team_id, user_id),
    -- One team per person per event year (EVT-02).
    UNIQUE KEY uq_team_member_year_user (event_year, user_id),
    UNIQUE KEY uq_team_member_leader (leader_team_id),
    CONSTRAINT fk_team_member_team FOREIGN KEY (team_id) REFERENCES team (id),
    CONSTRAINT fk_team_member_user FOREIGN KEY (user_id) REFERENCES `user` (id),
    CONSTRAINT chk_team_member_role CHECK (role IN ('LEADER', 'MEMBER'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
