package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

const (
	actionProvision = "admin.provision"
	// maxAdminsFileBytes bounds what a misconfigured path (a log, a device) can make us read.
	maxAdminsFileBytes = 1 << 20
)

// adminEntry is one element of the Vault-supplied admins.json (ADM-004, Architecture 8.6).
// Only a hash is accepted: the decoder rejects unknown fields, so a "password" key fails
// the whole file instead of being silently ignored.
type adminEntry struct {
	EmployeeID   string `json:"employeeId"`
	FullName     string `json:"fullName"`
	Email        string `json:"email"`
	PasswordHash string `json:"passwordHash"`
}

// ErrAdminConflict means a listed Employee ID already belongs to a Participant (ADM-004,
// R-01, R-04). Startup must stop: continuing would run with an Admin list that does not
// match the file.
var ErrAdminConflict = errors.New("admin seed conflict")

// SeedAdmins creates the Admin accounts listed in the file at path (ADM-004, R-03).
//
// It is idempotent and safe when several instances start together: each entry is
// INSERT ... ON DUPLICATE KEY UPDATE id = id, so an existing account is never
// overwritten (not its hash, name or flags) and a concurrent insert of the same ID is a
// no-op for the loser. All entries run in one transaction; a conflict or any other error
// rolls everything back, so a failed startup leaves nothing half seeded.
//
// New accounts get must_change_password = TRUE (AUTH-008) and one audit record each
// (AUD-001). It returns the Employee IDs it created. Hashes are never logged.
func SeedAdmins(ctx context.Context, db *sqlx.DB, aud Auditor, path string, log *slog.Logger) ([]string, error) {
	entries, err := readAdminsFile(path)
	if err != nil {
		return nil, err
	}

	var created []string
	err = platform.WithTx(ctx, db, func(tx *sqlx.Tx) error {
		created = nil
		for _, e := range entries {
			inserted, err := insertAdmin(ctx, tx, e)
			if err != nil {
				return err
			}
			if !inserted {
				if err := requireExistingAdmin(ctx, tx, e.EmployeeID); err != nil {
					return err
				}
				continue
			}
			created = append(created, e.EmployeeID)
			if err := aud.Record(ctx, tx, audit.Entry{
				// No actor: the application itself provisions the account (no request, no user).
				Action:     actionProvision,
				TargetType: targetUser, TargetID: e.EmployeeID,
				Result: audit.ResultSuccess,
				// AUD-006: the hash stays out of the record.
				Changes: map[string]any{"after": map[string]any{
					"role": roleAdmin, "mustChangePassword": true, "emailProvided": e.Email != "",
				}},
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	log.InfoContext(ctx, "admin accounts seeded", "listed", len(entries), "created", len(created))
	return append([]string(nil), created...), nil
}

// readAdminsFile loads and fully validates the file before the database is touched.
func readAdminsFile(path string) ([]adminEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open admins file: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only file: a close error changes nothing

	raw, err := io.ReadAll(io.LimitReader(f, maxAdminsFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read admins file: %w", err)
	}
	if len(raw) > maxAdminsFileBytes {
		return nil, fmt.Errorf("admins file %s is larger than %d bytes", path, maxAdminsFileBytes)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var entries []adminEntry
	if err := dec.Decode(&entries); err != nil {
		// json errors describe structure and offsets, not values, so no hash leaks here.
		return nil, fmt.Errorf("admins file %s is not a valid JSON array of admins: %w", path, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("admins file %s has data after the JSON array", path)
	}

	seen := map[string]bool{}
	for i := range entries {
		e := &entries[i]
		if err := validateAdminEntry(e); err != nil {
			return nil, fmt.Errorf("admins file entry %d: %w", i+1, err)
		}
		if seen[e.EmployeeID] {
			return nil, fmt.Errorf("admins file entry %d: employeeId %s is listed twice", i+1, e.EmployeeID)
		}
		seen[e.EmployeeID] = true
	}
	return entries, nil
}

func validateAdminEntry(e *adminEntry) error {
	id, err := platform.ParseEmployeeID(e.EmployeeID)
	if err != nil {
		return errors.New("employeeId must be exactly 6 digits")
	}
	e.EmployeeID = id.String()

	e.FullName = strings.TrimSpace(e.FullName)
	if e.FullName == "" || utf8.RuneCountInString(e.FullName) > maxFullNameRunes {
		return fmt.Errorf("employeeId %s: fullName is required and at most %d characters", e.EmployeeID, maxFullNameRunes)
	}
	e.Email = strings.TrimSpace(e.Email)
	if e.Email != "" && (len(e.Email) > maxEmailLen || !validEmail(e.Email)) {
		return fmt.Errorf("employeeId %s: email is not a valid address", e.EmployeeID)
	}
	// Parse only: a plaintext password pasted here would not look like an Argon2id PHC
	// string, and storing it would make the account unusable and the secret visible.
	if err := checkPHC(e.PasswordHash); err != nil {
		return fmt.Errorf("employeeId %s: passwordHash is not an Argon2id hash made by cmd/hashpw (%v)", e.EmployeeID, err)
	}
	return nil
}

const insertAdminSQL = "INSERT INTO `user` " +
	"(employee_id, full_name, email, department, role, password_hash, must_change_password) " +
	"VALUES (?, ?, ?, NULL, 'ADMIN', ?, TRUE) ON DUPLICATE KEY UPDATE id = id"

// insertAdmin reports whether a row was created. With ON DUPLICATE KEY UPDATE id = id the
// server reports 0 affected rows for an existing account (the update changes nothing),
// 1 for a new one.
func insertAdmin(ctx context.Context, tx sqlx.ExecerContext, e adminEntry) (bool, error) {
	var email any // NULL when not provided
	if e.Email != "" {
		email = e.Email
	}
	res, err := tx.ExecContext(ctx, insertAdminSQL, e.EmployeeID, e.FullName, email, e.PasswordHash)
	if err != nil {
		return false, fmt.Errorf("provision admin %s: %w", e.EmployeeID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("provision admin %s: %w", e.EmployeeID, err)
	}
	return n == 1, nil
}

// requireExistingAdmin fails startup when the ID that already exists is a Participant
// (ADM-004). The row is locked so the answer cannot change before the transaction ends.
func requireExistingAdmin(ctx context.Context, tx sqlx.QueryerContext, employeeID string) error {
	var role string
	err := sqlx.GetContext(ctx, tx, &role, "SELECT role FROM `user` WHERE employee_id = ? FOR UPDATE", employeeID)
	if errors.Is(err, sql.ErrNoRows) {
		// Deleted between the insert and now (engineer at work). Fail rather than guess.
		return fmt.Errorf("provision admin %s: the account disappeared during startup", employeeID)
	}
	if err != nil {
		return fmt.Errorf("provision admin %s: %w", employeeID, err)
	}
	if role != roleAdmin {
		return fmt.Errorf("%w: Employee ID %s is listed in the admins file but is already registered as a %s; "+
			"an Employee ID cannot be both (R-01, R-04). Remove it from the file or resolve the account in the database",
			ErrAdminConflict, employeeID, strings.ToLower(role))
	}
	return nil
}
