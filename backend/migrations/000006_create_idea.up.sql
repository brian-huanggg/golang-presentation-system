-- Ideas and their file versions (Architecture 7.1, 7.2; IDEA-001, IDEA-020). Times are UTC (see 000001).
CREATE TABLE idea (
    id                 BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    team_id            BIGINT UNSIGNED NOT NULL,
    -- Required, at most 100 characters (IDEA-010).
    title              VARCHAR(100)    NOT NULL,
    -- Optional, at most 1,000 characters (IDEA-010); '' when absent.
    summary            VARCHAR(1000)   NOT NULL DEFAULT '',
    -- Number of the latest version (IDEA-024). Never reused, even after old versions are trimmed.
    current_version_no INT UNSIGNED    NOT NULL,
    -- Optimistic lock behind ETag / If-Match (IDEA-030). Bumped on every change.
    version            BIGINT UNSIGNED NOT NULL DEFAULT 1,
    created_at         DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at         DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    -- Soft delete (IDEA-040). Hidden immediately; removed by cmd/purge after 7 days.
    deleted_at         DATETIME(6)     NULL,
    -- NULL once the Idea is soft-deleted: a team has at most one active Idea (IDEA-001) and can
    -- upload a new one right after deleting (IDEA-042). NULLs never collide in a unique index.
    active_team_id     BIGINT UNSIGNED AS (IF(deleted_at IS NULL, team_id, NULL)) VIRTUAL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_idea_active_team (active_team_id),
    CONSTRAINT fk_idea_team FOREIGN KEY (team_id) REFERENCES team (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE idea_version (
    id                 BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    idea_id            BIGINT UNSIGNED NOT NULL,
    version_no         INT UNSIGNED    NOT NULL,
    -- Random key in the object store (Architecture 7.3); the file name is never part of it.
    object_key         CHAR(32)        NOT NULL,
    -- 'pptx' or 'pdf', as validated by package upload (IDEA-002).
    file_kind          VARCHAR(8)      NOT NULL,
    -- Name as uploaded, reduced to a plain file name. Only stored here, never used as a path.
    original_file_name VARCHAR(255)    NOT NULL,
    size_bytes         BIGINT UNSIGNED NOT NULL,
    sha256             CHAR(64)        NOT NULL,
    uploaded_by        BIGINT UNSIGNED NOT NULL,
    uploaded_at        DATETIME(6)     NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_idea_version_no (idea_id, version_no),
    UNIQUE KEY uq_idea_version_object (object_key),
    CONSTRAINT fk_idea_version_idea FOREIGN KEY (idea_id) REFERENCES idea (id),
    CONSTRAINT fk_idea_version_user FOREIGN KEY (uploaded_by) REFERENCES `user` (id),
    CONSTRAINT chk_idea_version_kind CHECK (file_kind IN ('pptx', 'pdf')),
    CONSTRAINT chk_idea_version_size CHECK (size_bytes > 0 AND size_bytes <= 5242880)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
