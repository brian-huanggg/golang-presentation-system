package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

// errDuplicateEmployeeID is returned when the unique index on user.employee_id
// rejects an insert (REG-003).
var errDuplicateEmployeeID = errors.New("employee id already registered")

// mysqlDuplicateEntry is the server error number for a unique-key violation,
// the same on MySQL and MariaDB.
const mysqlDuplicateEntry = 1062

const insertUserSQL = `INSERT INTO ` + "`user`" + `
	(employee_id, full_name, email, department, role, preferred_language, password_hash)
	VALUES (?, ?, ?, ?, 'PARTICIPANT', ?, ?)`

// insertUser stores a new Participant. It relies on the unique index rather than a
// prior SELECT, so two simultaneous registrations cannot both succeed (REG-003).
func insertUser(ctx context.Context, tx sqlx.ExecerContext, u newUser, passwordHash string) error {
	var email any // NULL when not provided
	if u.Email != "" {
		email = u.Email
	}
	_, err := tx.ExecContext(ctx, insertUserSQL,
		u.EmployeeID.String(), u.FullName, email, u.Department.String(), u.PreferredLanguage, passwordHash)
	var me *mysql.MySQLError
	if errors.As(err, &me) && me.Number == mysqlDuplicateEntry {
		return errDuplicateEmployeeID
	}
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

// userRow is the subset of `user` that login and the session need.
type userRow struct {
	ID                 int64          `db:"id"`
	EmployeeID         string         `db:"employee_id"`
	FullName           string         `db:"full_name"`
	Department         sql.NullString `db:"department"`
	Role               string         `db:"role"`
	PreferredLanguage  string         `db:"preferred_language"`
	PasswordHash       string         `db:"password_hash"`
	CredentialVersion  int64          `db:"credential_version"` // ADR 0019
	MustChangePassword bool           `db:"must_change_password"`
	FailedAttempts     int            `db:"failed_attempts"`
	LockedUntil        sql.NullTime   `db:"locked_until"` // UTC (DSN loc=UTC)
}

const selectUserCols = "SELECT id, employee_id, full_name, department, role, preferred_language, password_hash, credential_version, must_change_password, failed_attempts, locked_until FROM `user` "

// errUserNotFound means no row matched; callers decide how much to reveal.
var errUserNotFound = errors.New("user not found")

func findUserByEmployeeID(ctx context.Context, q sqlx.QueryerContext, employeeID string) (userRow, error) {
	return findUser(ctx, q, selectUserCols+"WHERE employee_id = ?", employeeID)
}

func findUserByID(ctx context.Context, q sqlx.QueryerContext, id int64) (userRow, error) {
	return findUser(ctx, q, selectUserCols+"WHERE id = ?", id)
}

func findUser(ctx context.Context, q sqlx.QueryerContext, query string, arg any) (userRow, error) {
	var u userRow
	err := sqlx.GetContext(ctx, q, &u, query, arg)
	if errors.Is(err, sql.ErrNoRows) {
		return userRow{}, errUserNotFound
	}
	if err != nil {
		return userRow{}, fmt.Errorf("find user: %w", err)
	}
	return u, nil
}

// lockState is the lockout bookkeeping of one account (AUTH-002).
type lockState struct {
	FailedAttempts int          `db:"failed_attempts"`
	LockedUntil    sql.NullTime `db:"locked_until"`
}

// lockStateForUpdate reads the counters under a row lock, so concurrent wrong
// guesses are counted one at a time and cannot slip past the limit.
func lockStateForUpdate(ctx context.Context, tx sqlx.QueryerContext, userID int64) (lockState, error) {
	var st lockState
	err := sqlx.GetContext(ctx, tx, &st, "SELECT failed_attempts, locked_until FROM `user` WHERE id = ? FOR UPDATE", userID)
	if err != nil {
		return lockState{}, fmt.Errorf("read lock state of user %d: %w", userID, err)
	}
	return st, nil
}

// saveLockState stores the counters; a zero lockedUntil clears the lock.
func saveLockState(ctx context.Context, ex sqlx.ExecerContext, userID int64, attempts int, lockedUntil time.Time) error {
	var until any // NULL when not locked
	if !lockedUntil.IsZero() {
		until = lockedUntil
	}
	if _, err := ex.ExecContext(ctx, "UPDATE `user` SET failed_attempts = ?, locked_until = ? WHERE id = ?", attempts, until, userID); err != nil {
		return fmt.Errorf("save lock state of user %d: %w", userID, err)
	}
	return nil
}

// errCredentialsChanged means the credential version moved between our read and our
// write: another password change won the race (ADR 0019).
var errCredentialsChanged = errors.New("credentials changed concurrently")

// updatePassword stores a new hash, bumps the credential version, clears the forced
// change flag (AUTH-008) and any lockout state. It is conditional on the version the
// caller verified the old password against, so two simultaneous changes cannot both win.
func updatePassword(ctx context.Context, ex sqlx.ExecerContext, userID, expectedVersion int64, hash string) error {
	res, err := ex.ExecContext(ctx, "UPDATE `user` SET password_hash = ?, credential_version = credential_version + 1, "+
		"must_change_password = FALSE, failed_attempts = 0, locked_until = NULL WHERE id = ? AND credential_version = ?",
		hash, userID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update password of user %d: %w", userID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update password of user %d: %w", userID, err)
	}
	if n == 0 {
		return errCredentialsChanged
	}
	return nil
}

func updatePreferredLanguage(ctx context.Context, ex sqlx.ExecerContext, userID int64, lang string) error {
	if _, err := ex.ExecContext(ctx, "UPDATE `user` SET preferred_language = ? WHERE id = ?", lang, userID); err != nil {
		return fmt.Errorf("update language of user %d: %w", userID, err)
	}
	return nil
}
