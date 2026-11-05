package auth

import (
	"context"
	"errors"
	"fmt"

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
