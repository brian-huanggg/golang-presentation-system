package idea

import (
	"context"
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
