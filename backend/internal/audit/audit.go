package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// Result is the outcome of the audited operation (AUD-002).
type Result string

const (
	ResultSuccess Result = "success"
	ResultFailure Result = "failure"
)

// ErrInvalidEntry marks an Entry that is rejected before touching the database.
var ErrInvalidEntry = errors.New("invalid audit entry")

// Entry is one audit record. Actor fields are empty when no actor is known (for
// example a failed login for an unknown Employee ID). The timestamp is not part of
// Entry: the Recorder stamps it from its Clock.
type Entry struct {
	ActorEmployeeID string
	ActorRole       string
	Action          string
	TargetType      string
	TargetID        string
	Result          Result
	SourceIP        string
	// Changes holds before/after values; anything encoding/json can marshal.
	// It must not contain passwords, session identifiers or file contents (AUD-006).
	Changes any
}

// Recorder writes audit rows. Construct it once in main.go.
type Recorder struct {
	db    *sqlx.DB
	clock platform.Clock
}

func NewRecorder(db *sqlx.DB, clock platform.Clock) *Recorder {
	return &Recorder{db: db, clock: clock}
}

const insertSQL = `INSERT INTO audit_record
	(occurred_at, actor_employee_id, actor_role, action, target_type, target_id, result, source_ip, changes)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

// Record inserts the entry inside the caller's transaction, so it commits or rolls
// back together with the business change (Architecture 5.3, AUD-001). It takes
// sqlx.ExecerContext so a *sqlx.Tx fits without audit depending on more than sqlx.
func (r *Recorder) Record(ctx context.Context, tx sqlx.ExecerContext, e Entry) error {
	args, err := r.args(e)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, insertSQL, args...); err != nil {
		return fmt.Errorf("insert audit record: %w", err)
	}
	return nil
}

// RecordStandalone inserts the entry in its own short transaction, for events with
// no business transaction (failed login, rejected upload, authorization denial).
func (r *Recorder) RecordStandalone(ctx context.Context, e Entry) error {
	// Validate first so a bad entry never opens a transaction.
	if _, err := r.args(e); err != nil {
		return err
	}
	return platform.WithTx(ctx, r.db, func(tx *sqlx.Tx) error {
		return r.Record(ctx, tx, e)
	})
}

// sensitiveKeyParts are substrings of JSON object keys that must never appear in
// Changes (AUD-006). Matching is case-insensitive.
var sensitiveKeyParts = []string{"password", "passwd", "session", "token", "secret", "file_content", "filecontent"}

func (r *Recorder) args(e Entry) ([]any, error) {
	if strings.TrimSpace(e.Action) == "" {
		return nil, fmt.Errorf("%w: action is required", ErrInvalidEntry)
	}
	if e.Result != ResultSuccess && e.Result != ResultFailure {
		return nil, fmt.Errorf("%w: result must be %q or %q", ErrInvalidEntry, ResultSuccess, ResultFailure)
	}
	changes, err := encodeChanges(e.Changes)
	if err != nil {
		return nil, err
	}
	return []any{
		r.clock.Now().UTC(),
		nullable(e.ActorEmployeeID), nullable(e.ActorRole),
		e.Action,
		nullable(e.TargetType), nullable(e.TargetID),
		string(e.Result),
		nullable(e.SourceIP),
		changes,
	}, nil
}

// nullable maps "" to SQL NULL so absent fields are not stored as empty strings.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func encodeChanges(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: encode changes: %v", ErrInvalidEntry, err)
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, fmt.Errorf("%w: decode changes: %v", ErrInvalidEntry, err)
	}
	if key := findSensitiveKey(tree); key != "" {
		return nil, fmt.Errorf("%w: changes must not contain key %q (AUD-006)", ErrInvalidEntry, key)
	}
	return string(raw), nil
}

func findSensitiveKey(v any) string {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			lk := strings.ToLower(k)
			for _, bad := range sensitiveKeyParts {
				if strings.Contains(lk, bad) {
					return k
				}
			}
			if found := findSensitiveKey(child); found != "" {
				return found
			}
		}
	case []any:
		for _, child := range t {
			if found := findSensitiveKey(child); found != "" {
				return found
			}
		}
	}
	return ""
}
