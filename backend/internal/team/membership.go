package team

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// Membership says whether a team is active and what role one person has in it. Package
// idea reads it through its own consumer-side interface (ADR 0003, rule 2).
type Membership struct {
	// Exists is false when the team is unknown or dissolved (soft-deleted, TEAM-015).
	Exists bool
	// Role is RoleLeader, RoleMember, or "" when the person is not on the roster.
	Role string
}

// MembershipOf reads the team row and the person's roster row with a locking read, so a
// dissolution or roster change that commits meanwhile waits for the caller's transaction
// (and the caller never acts on a team that has just been dissolved). Call it inside the
// transaction that performs the change.
func (s *Service) MembershipOf(ctx context.Context, q sqlx.QueryerContext, teamID int64, employeeID string) (Membership, error) {
	var found int
	err := sqlx.GetContext(ctx, q, &found, "SELECT 1 FROM team WHERE id = ? AND deleted_at IS NULL FOR UPDATE", teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{}, nil
	}
	if err != nil {
		return Membership{}, fmt.Errorf("read team: %w", err)
	}
	var role string
	err = sqlx.GetContext(ctx, q, &role,
		"SELECT tm.role FROM team_member tm JOIN `user` u ON u.id = tm.user_id WHERE tm.team_id = ? AND u.employee_id = ?",
		teamID, employeeID)
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{Exists: true}, nil
	}
	if err != nil {
		return Membership{}, fmt.Errorf("read membership: %w", err)
	}
	return Membership{Exists: true, Role: role}, nil
}
