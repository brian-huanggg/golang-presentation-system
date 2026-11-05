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

var errTeamNotFound = errors.New("team not found")

// teamRow is the part of `team` that roster changes read.
type teamRow struct {
	ID         int64  `db:"id"`
	EventYear  int    `db:"event_year"`
	Name       string `db:"name"`
	Track      string `db:"track"`
	Award      string `db:"award_category"`
	Department string `db:"department"`
}

// lockTeam locks an active team of the year. Every roster change takes this lock first,
// so changes to one team are serialised: the size check of TEAM-006 cannot be raced
// (Architecture 7.2). Lock order is always team, then users.
func lockTeam(ctx context.Context, tx sqlx.QueryerContext, teamID int64, year int) (teamRow, error) {
	var t teamRow
	err := sqlx.GetContext(ctx, tx, &t,
		"SELECT id, event_year, name, track, award_category, department FROM team WHERE id = ? AND event_year = ? AND deleted_at IS NULL FOR UPDATE",
		teamID, year)
	if errors.Is(err, sql.ErrNoRows) {
		return teamRow{}, errTeamNotFound
	}
	if err != nil {
		return teamRow{}, fmt.Errorf("lock team: %w", err)
	}
	return t, nil
}

// rosterRow is one person on a team.
type rosterRow struct {
	UserID     int64  `db:"user_id"`
	EmployeeID string `db:"employee_id"`
	FullName   string `db:"full_name"`
	Role       string `db:"role"`
}

// listRoster returns the leader first, then the members in the order they joined. It is
// read after lockTeam, so it sees every change committed before we got the lock.
func listRoster(ctx context.Context, tx sqlx.QueryerContext, teamID int64) ([]rosterRow, error) {
	var rows []rosterRow
	err := sqlx.SelectContext(ctx, tx, &rows,
		"SELECT m.user_id, u.employee_id, u.full_name, m.role FROM team_member m JOIN `user` u ON u.id = m.user_id "+
			"WHERE m.team_id = ? ORDER BY (m.role = 'LEADER') DESC, m.created_at, m.user_id", teamID)
	if err != nil {
		return nil, fmt.Errorf("read roster: %w", err)
	}
	return rows, nil
}

// deleteMember removes one non-leader row and reports whether a row was there.
func deleteMember(ctx context.Context, tx sqlx.ExecerContext, teamID, userID int64) (bool, error) {
	res, err := tx.ExecContext(ctx, "DELETE FROM team_member WHERE team_id = ? AND user_id = ? AND role = 'MEMBER'", teamID, userID)
	if err != nil {
		return false, fmt.Errorf("delete team member: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete team member: %w", err)
	}
	return n == 1, nil
}

// softDeleteTeam hides the team (TEAM-015). Setting deleted_at also empties the generated
// name_active column, which frees the name for reuse at once.
func softDeleteTeam(ctx context.Context, tx sqlx.ExecerContext, teamID int64) error {
	res, err := tx.ExecContext(ctx, "UPDATE team SET deleted_at = CURRENT_TIMESTAMP(6) WHERE id = ? AND deleted_at IS NULL", teamID)
	if err != nil {
		return fmt.Errorf("soft delete team: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("soft delete team: %w", err)
	}
	if n != 1 {
		return errTeamNotFound
	}
	return nil
}

// deleteAllMembers releases every person of the team, the leader included, so they can
// join or create another team at once (TEAM-015, AC-TEAM-015).
func deleteAllMembers(ctx context.Context, tx sqlx.ExecerContext, teamID int64) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM team_member WHERE team_id = ?", teamID); err != nil {
		return fmt.Errorf("release team members: %w", err)
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
