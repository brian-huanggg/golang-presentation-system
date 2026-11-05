package team

import (
	"context"
	"errors"
	"strconv"

	"github.com/jmoiron/sqlx"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/auth"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// fieldEmployeeID is the JSON name of the single ID in add/remove requests.
const fieldEmployeeID = "employeeId"

// maxTeamSize is TEAM-006: the leader plus four others.
const maxTeamSize = maxOtherMembers + 1

// AddMember adds one person to the caller's team (TEAM-010). The person is checked as
// at creation (TEAM-007) and the team may not grow past 5 (TEAM-006). The whole change,
// with its audit record, is one transaction.
func (s *Service) AddMember(ctx context.Context, teamID int64, rawID, sourceIP string) (Team, error) {
	actor, err := s.ident.CurrentUser(ctx)
	if err != nil {
		return Team{}, err
	}
	var out Team
	err = s.rosterChange(ctx, actor, actionAddMember, teamID, sourceIP, ErrNotLeader(), func() error {
		id, perr := platform.ParseEmployeeID(rawID)
		if perr != nil {
			return idProblem(echo(rawID), reasonInvalidFormat)
		}
		if id.String() == actor.EmployeeID {
			return idProblem(rawID, reasonIsLeader)
		}
		return platform.WithTx(ctx, s.db, func(tx *sqlx.Tx) error {
			var err error
			out, err = s.addInTx(ctx, tx, actor, teamID, id, sourceIP)
			return err
		})
	})
	if err != nil {
		return Team{}, err
	}
	return out, nil
}

// RemoveMember removes one non-leader from the caller's team (TEAM-011). The
// confirmation of NFR-UX-003 belongs to the UI.
func (s *Service) RemoveMember(ctx context.Context, teamID int64, rawID, sourceIP string) error {
	actor, err := s.ident.CurrentUser(ctx)
	if err != nil {
		return err
	}
	return s.rosterChange(ctx, actor, actionRemoveMember, teamID, sourceIP, ErrNotLeader(), func() error {
		id, perr := platform.ParseEmployeeID(rawID)
		if perr != nil {
			return idProblem(echo(rawID), reasonInvalidFormat)
		}
		return platform.WithTx(ctx, s.db, func(tx *sqlx.Tx) error {
			return s.removeInTx(ctx, tx, actor, teamID, id, sourceIP)
		})
	})
}

// Leave takes the caller out of their own team (TEAM-012). Only a plain member may leave;
// the leader must dissolve the team instead (TEAM-013). The confirmation of NFR-UX-003
// belongs to the UI. The team is found through the caller's own membership, so a
// teamId the caller does not belong to is "not found".
func (s *Service) Leave(ctx context.Context, teamID int64, sourceIP string) error {
	actor, err := s.ident.CurrentUser(ctx)
	if err != nil {
		return err
	}
	return s.rosterChange(ctx, actor, actionLeave, teamID, sourceIP, ErrAdminCannotLeave(), func() error {
		return platform.WithTx(ctx, s.db, func(tx *sqlx.Tx) error {
			return s.leaveInTx(ctx, tx, actor, teamID, sourceIP)
		})
	})
}

func (s *Service) leaveInTx(ctx context.Context, tx *sqlx.Tx, actor auth.SessionUser, teamID int64, sourceIP string) error {
	// Same lock order as add/remove (team first), so a leave cannot deadlock with them.
	if _, err := lockTeam(ctx, tx, teamID, s.event.Year()); err != nil {
		return err
	}
	roster, err := listRoster(ctx, tx, teamID)
	if err != nil {
		return err
	}
	var me *rosterRow
	for i := range roster {
		if roster[i].EmployeeID == actor.EmployeeID {
			me = &roster[i]
		}
	}
	switch {
	case me == nil: // a stranger learns nothing about which team ids exist
		return errTeamNotFound
	case me.Role == RoleLeader: // TEAM-013
		return ErrLeaderCannotLeave()
	}

	if err := s.event.CheckActor(false); err != nil { // DDL-005
		return err
	}
	ok, err := deleteMember(ctx, tx, teamID, me.UserID)
	if err != nil {
		return err
	}
	if !ok {
		return errTeamNotFound
	}
	after := make([]string, 0, len(roster)-1)
	for _, r := range roster {
		if r.UserID != me.UserID {
			after = append(after, r.EmployeeID)
		}
	}
	return s.audit.Record(ctx, tx, audit.Entry{
		ActorEmployeeID: actor.EmployeeID, ActorRole: actor.Role,
		Action: actionLeave, TargetType: targetTeam, TargetID: strconv.FormatInt(teamID, 10),
		Result: audit.ResultSuccess, SourceIP: sourceIP,
		Changes: map[string]any{
			"before": map[string]any{"members": memberIDsOf(roster)},
			"after":  map[string]any{"members": after},
		},
	})
}

// rosterChange runs the checks shared by add, remove and leave, then body. Whatever comes back
// as a Problem is audited as a failure (AUD-001) and returned unchanged; other errors are
// internal and pass through.
func (s *Service) rosterChange(ctx context.Context, actor auth.SessionUser, action string, teamID int64, sourceIP string, adminErr *platform.Problem, body func() error) error {
	// R-02, ADM-005: an Admin has no part in rosters. DDL-003: checked again in the
	// transaction because the deadline is judged when the action completes (DDL-005).
	err := func() error {
		if actor.Role == roleAdmin {
			return adminErr
		}
		if err := s.event.CheckActor(false); err != nil {
			return err
		}
		return body()
	}()
	switch {
	case errors.Is(err, errDuplicateMember):
		err = errMemberRaced()
	case errors.Is(err, errTeamNotFound):
		err = ErrTeamNotFound()
	}
	var p *platform.Problem
	if errors.As(err, &p) {
		s.auditFailure(ctx, actor, action, strconv.FormatInt(teamID, 10), sourceIP, p)
	}
	return err
}

// leaderOf locks the team and proves the actor leads it. A person outside the team gets
// "not found", a plain member gets "forbidden".
func (s *Service) leaderOf(ctx context.Context, tx *sqlx.Tx, actor auth.SessionUser, teamID int64) (teamRow, []rosterRow, error) {
	t, err := lockTeam(ctx, tx, teamID, s.event.Year())
	if err != nil {
		return teamRow{}, nil, err
	}
	roster, err := listRoster(ctx, tx, teamID)
	if err != nil {
		return teamRow{}, nil, err
	}
	for _, r := range roster {
		if r.EmployeeID == actor.EmployeeID {
			if r.Role != RoleLeader {
				return teamRow{}, nil, ErrNotLeader()
			}
			return t, roster, nil
		}
	}
	return teamRow{}, nil, errTeamNotFound
}

func (s *Service) addInTx(ctx context.Context, tx *sqlx.Tx, actor auth.SessionUser, teamID int64, id platform.EmployeeID, sourceIP string) (Team, error) {
	t, roster, err := s.leaderOf(ctx, tx, actor, teamID)
	if err != nil {
		return Team{}, err
	}
	if len(roster) >= maxTeamSize { // TEAM-006; the team lock makes this count exact.
		return Team{}, ErrTeamFull()
	}

	// TEAM-007, same rules and reasons as creation.
	rows, err := lockUsers(ctx, tx, []string{id.String()})
	if err != nil {
		return Team{}, err
	}
	if len(rows) == 0 || rows[0].Role != "PARTICIPANT" { // an Admin looks like an unknown ID, as at creation (R-04)
		return Team{}, idProblem(id.String(), reasonNotRegistered)
	}
	person := rows[0]
	if person.Department.String != t.Department {
		return Team{}, idProblem(id.String(), reasonDepartmentMismatch)
	}
	teamed, err := teamedUserIDs(ctx, tx, t.EventYear, []int64{person.ID})
	if err != nil {
		return Team{}, err
	}
	if teamed[person.ID] {
		return Team{}, idProblem(id.String(), reasonAlreadyInTeam)
	}

	// DDL-005: the action completes here, so this check counts.
	if err := s.event.CheckActor(false); err != nil {
		return Team{}, err
	}
	if err := insertMember(ctx, tx, teamID, person.ID, t.EventYear, RoleMember); err != nil {
		return Team{}, err
	}
	before := memberIDsOf(roster)
	err = s.audit.Record(ctx, tx, audit.Entry{
		ActorEmployeeID: actor.EmployeeID, ActorRole: actor.Role,
		Action: actionAddMember, TargetType: targetTeam, TargetID: strconv.FormatInt(teamID, 10),
		Result: audit.ResultSuccess, SourceIP: sourceIP,
		Changes: map[string]any{
			"before": map[string]any{"members": before},
			"after":  map[string]any{"members": append(append([]string{}, before...), person.EmployeeID)},
		},
	})
	if err != nil {
		return Team{}, err
	}
	out := toTeam(t, roster)
	out.Members = append(out.Members, Member{EmployeeID: person.EmployeeID, FullName: person.FullName, Role: RoleMember})
	return out, nil
}

func (s *Service) removeInTx(ctx context.Context, tx *sqlx.Tx, actor auth.SessionUser, teamID int64, id platform.EmployeeID, sourceIP string) error {
	_, roster, err := s.leaderOf(ctx, tx, actor, teamID)
	if err != nil {
		return err
	}
	var victim *rosterRow
	for i := range roster {
		if roster[i].EmployeeID == id.String() {
			victim = &roster[i]
		}
	}
	switch {
	case victim == nil:
		return ErrMemberNotFound()
	case victim.Role == RoleLeader: // TEAM-013
		return ErrLeaderCannotBeRemoved()
	}

	if err := s.event.CheckActor(false); err != nil { // DDL-005
		return err
	}
	ok, err := deleteMember(ctx, tx, teamID, victim.UserID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrMemberNotFound()
	}
	after := make([]string, 0, len(roster)-1)
	for _, r := range roster {
		if r.UserID != victim.UserID {
			after = append(after, r.EmployeeID)
		}
	}
	return s.audit.Record(ctx, tx, audit.Entry{
		ActorEmployeeID: actor.EmployeeID, ActorRole: actor.Role,
		Action: actionRemoveMember, TargetType: targetTeam, TargetID: strconv.FormatInt(teamID, 10),
		Result: audit.ResultSuccess, SourceIP: sourceIP,
		Changes: map[string]any{
			"before": map[string]any{"members": memberIDsOf(roster)},
			"after":  map[string]any{"members": after},
		},
	})
}

func memberIDsOf(roster []rosterRow) []string {
	ids := make([]string, 0, len(roster))
	for _, r := range roster {
		ids = append(ids, r.EmployeeID)
	}
	return ids
}

func toTeam(t teamRow, roster []rosterRow) Team {
	out := Team{
		ID: t.ID, EventYear: t.EventYear, Name: t.Name, Track: t.Track,
		AwardCategory: t.Award, Department: t.Department,
		Members: make([]Member, 0, len(roster)+1),
	}
	for _, r := range roster {
		out.Members = append(out.Members, Member{EmployeeID: r.EmployeeID, FullName: r.FullName, Role: r.Role})
	}
	return out
}

// idProblem is the 400 for one bad Employee ID in an add or remove request.
func idProblem(value, reason string) *platform.Problem {
	return auth.ErrValidation([]platform.FieldError{{Field: fieldEmployeeID, Code: reason, Value: value}})
}
