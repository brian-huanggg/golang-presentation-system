package idea

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

// mysqlDuplicateEntry is the server error number for a unique-key violation,
// the same on MySQL and MariaDB.
const mysqlDuplicateEntry = 1062

// Name of the unique index (migration 000006) that carries domain meaning.
const keyActiveTeam = "uq_idea_active_team"

// errDuplicateIdea: the unique index rejected a second active Idea for the team (IDEA-001).
var errDuplicateIdea = errors.New("team already has an active idea")

// errUploaderGone: the uploader's account no longer exists (an engineer deleted it).
var errUploaderGone = errors.New("uploader account not found")

// insertIdea creates the Idea at version 1 of its file. current_version_no is the number
// of the version inserted next to it by insertVersion.
func insertIdea(ctx context.Context, tx sqlx.ExecerContext, teamID int64, title, summary string, versionNo int) (int64, error) {
	res, err := tx.ExecContext(ctx,
		"INSERT INTO idea (team_id, title, summary, current_version_no) VALUES (?, ?, ?, ?)",
		teamID, title, summary, versionNo)
	if err != nil {
		if isDuplicateOn(err, keyActiveTeam) {
			return 0, errDuplicateIdea
		}
		return 0, fmt.Errorf("insert idea: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert idea: %w", err)
	}
	return id, nil
}

// newVersion is the row written for one stored file.
type newVersion struct {
	IdeaID     int64
	No         int
	ObjectKey  string
	Kind       string
	FileName   string
	SizeBytes  int64
	SHA256     string
	UploadedBy string // Employee ID
	UploadedAt time.Time
}

// insertVersion writes the version row. The uploader is resolved from the Employee ID in
// the same statement; no row inserted means the account is gone.
func insertVersion(ctx context.Context, tx sqlx.ExecerContext, v newVersion) error {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO idea_version
			(idea_id, version_no, object_key, file_kind, original_file_name, size_bytes, sha256, uploaded_by, uploaded_at)
		 SELECT ?, ?, ?, ?, ?, ?, ?, u.id, ? FROM `+"`user`"+` u WHERE u.employee_id = ?`,
		v.IdeaID, v.No, v.ObjectKey, v.Kind, v.FileName, v.SizeBytes, v.SHA256, v.UploadedAt.UTC(), v.UploadedBy)
	if err != nil {
		return fmt.Errorf("insert idea version: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("insert idea version: %w", err)
	}
	if n == 0 {
		return errUploaderGone
	}
	return nil
}

// isDuplicateOn reports a unique-key violation on the named index. The server names the
// index in the message ("... for key 'uq_x'").
func isDuplicateOn(err error, key string) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == mysqlDuplicateEntry && strings.Contains(me.Message, key)
}

// errIdeaGone: no active Idea with that id (never existed, or soft-deleted).
var errIdeaGone = errors.New("idea not found")

// errStaleRevision: the Idea's revision no longer matches the caller's (IDEA-030).
var errStaleRevision = errors.New("idea revision changed")

// ideaRow is the part of an Idea that replacing its file needs.
type ideaRow struct {
	TeamID           int64  `db:"team_id"`
	Title            string `db:"title"`
	Summary          string `db:"summary"`
	CurrentVersionNo int    `db:"current_version_no"`
	Revision         int64  `db:"version"`
}

// teamOfIdea reads the owning team without locking, so authorisation can lock the team
// first and the Idea second, the same order as creation.
func teamOfIdea(ctx context.Context, q sqlx.QueryerContext, ideaID int64) (int64, error) {
	var teamID int64
	err := sqlx.GetContext(ctx, q, &teamID, "SELECT team_id FROM idea WHERE id = ? AND deleted_at IS NULL", ideaID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errIdeaGone
	}
	if err != nil {
		return 0, fmt.Errorf("read idea team: %w", err)
	}
	return teamID, nil
}

// lockIdea reads the active Idea and holds its row until the transaction ends.
func lockIdea(ctx context.Context, tx sqlx.QueryerContext, ideaID int64) (ideaRow, error) {
	var r ideaRow
	err := sqlx.GetContext(ctx, tx, &r,
		"SELECT team_id, title, summary, current_version_no, version FROM idea WHERE id = ? AND deleted_at IS NULL FOR UPDATE", ideaID)
	if errors.Is(err, sql.ErrNoRows) {
		return ideaRow{}, errIdeaGone
	}
	if err != nil {
		return ideaRow{}, fmt.Errorf("lock idea: %w", err)
	}
	return r, nil
}

// advanceIdea points the Idea at its new latest version and bumps the revision (IDEA-024,
// IDEA-030). Zero affected rows means the revision moved.
func advanceIdea(ctx context.Context, tx sqlx.ExecerContext, ideaID int64, revision int64, versionNo int) error {
	res, err := tx.ExecContext(ctx,
		"UPDATE idea SET current_version_no = ?, version = version + 1 WHERE id = ? AND version = ?",
		versionNo, ideaID, revision)
	if err != nil {
		return fmt.Errorf("advance idea: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("advance idea: %w", err)
	}
	if n == 0 {
		return errStaleRevision
	}
	return nil
}

// trimmedVersion is a version that fell out of the retention window.
type trimmedVersion struct {
	No        int    `db:"version_no"`
	ObjectKey string `db:"object_key"`
}

// trimVersions deletes every version numbered at or below cutoff and returns them, so the
// caller can remove their objects after the commit (IDEA-021). Numbers are never reused,
// so "at or below latest-10" is exactly "the oldest beyond the ten kept".
func trimVersions(ctx context.Context, tx sqlx.ExtContext, ideaID int64, cutoff int) ([]trimmedVersion, error) {
	var old []trimmedVersion
	err := sqlx.SelectContext(ctx, tx, &old,
		"SELECT version_no, object_key FROM idea_version WHERE idea_id = ? AND version_no <= ? ORDER BY version_no", ideaID, cutoff)
	if err != nil {
		return nil, fmt.Errorf("select old versions: %w", err)
	}
	if len(old) == 0 {
		return nil, nil
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM idea_version WHERE idea_id = ? AND version_no <= ?", ideaID, cutoff); err != nil {
		return nil, fmt.Errorf("delete old versions: %w", err)
	}
	return old, nil
}
