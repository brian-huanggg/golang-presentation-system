package team

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

// mysqlDuplicateEntry is the server error number for a unique-key violation,
// the same on MySQL and MariaDB.
const mysqlDuplicateEntry = 1062

// Names of the unique indexes (migration 000005) that carry domain meaning.
const (
	keyTeamName   = "uq_team_name_active"
	keyMemberYear = "uq_team_member_year_user"
)

var (
	// errDuplicateTeamName: the unique index rejected the name (TEAM-004).
	errDuplicateTeamName = errors.New("team name already used this year")
	// errDuplicateMember: someone is already in a team this event year (EVT-02).
	errDuplicateMember = errors.New("person already in a team this year")
)

// userRow is the part of `user` that team creation reads. It is read-only access to the
// table that package auth owns; team never writes it and never calls auth's repo.
type userRow struct {
	ID         int64          `db:"id"`
	EmployeeID string         `db:"employee_id"`
	FullName   string         `db:"full_name"`
	Department sql.NullString `db:"department"`
	Role       string         `db:"role"`
}

// lockUsers reads the rows for the given Employee IDs and locks them. Rows come back in
// primary-key order, which is also the lock order, so two creations that share a person
// cannot deadlock. Holding these locks until commit serialises concurrent attempts to
// claim the same person: the second one waits, then sees the first one's membership.
func lockUsers(ctx context.Context, tx sqlx.QueryerContext, employeeIDs []string) ([]userRow, error) {
	q, args, err := sqlx.In("SELECT id, employee_id, full_name, department, role FROM `user` WHERE employee_id IN (?) ORDER BY id FOR UPDATE", employeeIDs)
	if err != nil {
		return nil, fmt.Errorf("build user lookup: %w", err)
	}
	var rows []userRow
	if err := sqlx.SelectContext(ctx, tx, &rows, q, args...); err != nil {
		return nil, fmt.Errorf("lock users: %w", err)
	}
	return rows, nil
}

// teamedUserIDs returns which of userIDs already belong to a team in the year. It is a
// locking read, so it sees memberships committed by a transaction that finished while we
// waited for the user locks (a plain read would use the older snapshot).
func teamedUserIDs(ctx context.Context, tx sqlx.QueryerContext, year int, userIDs []int64) (map[int64]bool, error) {
	q, args, err := sqlx.In("SELECT user_id FROM team_member WHERE event_year = ? AND user_id IN (?) FOR UPDATE", year, userIDs)
	if err != nil {
		return nil, fmt.Errorf("build membership lookup: %w", err)
	}
	var ids []int64
	if err := sqlx.SelectContext(ctx, tx, &ids, q, args...); err != nil {
		return nil, fmt.Errorf("read memberships: %w", err)
	}
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

func insertTeam(ctx context.Context, tx sqlx.ExecerContext, year int, t newTeam, department string) (int64, error) {
	res, err := tx.ExecContext(ctx,
		"INSERT INTO team (event_year, name, track, award_category, department) VALUES (?, ?, ?, ?, ?)",
		year, t.Name, t.Track.String(), t.AwardCategory.String(), department)
	if err != nil {
		if isDuplicateOn(err, keyTeamName) {
			return 0, errDuplicateTeamName
		}
		return 0, fmt.Errorf("insert team: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert team: %w", err)
	}
	return id, nil
}

func insertMember(ctx context.Context, tx sqlx.ExecerContext, teamID, userID int64, year int, role string) error {
	_, err := tx.ExecContext(ctx,
		"INSERT INTO team_member (team_id, user_id, event_year, role) VALUES (?, ?, ?, ?)",
		teamID, userID, year, role)
	if err != nil {
		if isDuplicateOn(err, keyMemberYear) {
			return errDuplicateMember
		}
		return fmt.Errorf("insert team member: %w", err)
	}
	return nil
}

// isDuplicateOn reports a unique-key violation on the named index. The server names the
// index in the message ("... for key 'uq_x'"), which is how two unique indexes on one
// statement are told apart.
func isDuplicateOn(err error, key string) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == mysqlDuplicateEntry && strings.Contains(me.Message, key)
}
