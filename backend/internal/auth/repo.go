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
	MustChangePassword bool           `db:"must_change_password"`
	FailedAttempts     int            `db:"failed_attempts"`
	LockedUntil        sql.NullTime   `db:"locked_until"` // UTC (DSN loc=UTC)
}

const selectUserCols = "SELECT id, employee_id, full_name, department, role, preferred_language, password_hash, must_change_password, failed_attempts, locked_until FROM `user` "

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
