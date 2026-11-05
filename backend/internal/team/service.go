package team

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/jmoiron/sqlx"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/auth"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

const (
	actionCreate = "team.create"
	targetTeam   = "team"
	roleAdmin    = "ADMIN"
)

// Identity resolves the logged-in user from the request's session. *auth.Service
// satisfies it; it is declared here, at the consumer (ADR 0003).
type Identity interface {
	CurrentUser(ctx context.Context) (auth.SessionUser, error)
}

// EventPolicy is the part of event.Policy that team uses: the current event year and the
// deadline check. Declared here so team does not depend on event's concrete type.
type EventPolicy interface {
	Year() int
	CheckActor(isAdmin bool) error
}

// Auditor is the part of audit.Recorder that team uses.
type Auditor interface {
	Record(ctx context.Context, tx sqlx.ExecerContext, e audit.Entry) error
	RecordStandalone(ctx context.Context, e audit.Entry) error
}

// Service holds the team business rules. Construct it once in main.go.
type Service struct {
	db    *sqlx.DB
	ident Identity
	event EventPolicy
	audit Auditor
	log   *slog.Logger
}

func NewService(db *sqlx.DB, ident Identity, event EventPolicy, auditor Auditor, log *slog.Logger) *Service {
	return &Service{db: db, ident: ident, event: event, audit: auditor, log: log}
}

// Create makes a team with the caller as its leader (TEAM-001..008). It is all or
// nothing: the leader and every listed person are locked, checked and written in one
// transaction together with the audit record, so any problem leaves nothing behind.
//
// Every problem with the listed IDs is reported together, each with its reason, so the
// leader can fix the whole list in one go (TEAM-008).
func (s *Service) Create(ctx context.Context, in CreateInput, sourceIP string) (Team, error) {
	actor, err := s.ident.CurrentUser(ctx)
	if err != nil {
		return Team{}, err
	}
	// reject audits the refusal (AUD-001: every team action, failures included) and
	// returns the problem unchanged. The write is outside any business transaction.
	reject := func(p *platform.Problem) (Team, error) {
		s.auditFailure(ctx, actor, sourceIP, p)
		return Team{}, p
	}

	// TEAM-001, R-02: only a Participant creates a team; an Admin never joins one.
	if actor.Role == roleAdmin {
		return reject(ErrForbidden())
	}
	// DDL-003. Checked again inside the transaction, because the deadline is judged when
	// the action completes (DDL-005).
	if err := s.event.CheckActor(false); err != nil {
		return rejectErr(reject, err)
	}

	leaderID, err := platform.ParseEmployeeID(actor.EmployeeID)
	if err != nil {
		return Team{}, fmt.Errorf("session user has a malformed employee id: %w", err)
	}
	nt, fieldErrs := validateCreate(in, leaderID)
	if len(fieldErrs) > 0 {
		return reject(auth.ErrValidation(fieldErrs))
	}

	var created Team
	err = platform.WithTx(ctx, s.db, func(tx *sqlx.Tx) error {
		var err error
		created, err = s.createInTx(ctx, tx, actor, leaderID, nt, sourceIP)
		return err
	})
	switch {
	case errors.Is(err, errDuplicateTeamName):
		return reject(ErrTeamNameTaken())
	case errors.Is(err, errDuplicateMember):
		// The unique index is the backstop for what the row locks should already prevent.
		return reject(errMemberRaced())
	}
	var p *platform.Problem
	if errors.As(err, &p) {
		return reject(p)
	}
	if err != nil {
		return Team{}, err
	}
	return created, nil
}

// rejectErr is reject for an error that may or may not be a Problem.
func rejectErr(reject func(*platform.Problem) (Team, error), err error) (Team, error) {
	var p *platform.Problem
	if errors.As(err, &p) {
		return reject(p)
	}
	return Team{}, err
}

// createInTx is the body of the transaction. Returning an error rolls everything back.
func (s *Service) createInTx(ctx context.Context, tx *sqlx.Tx, actor auth.SessionUser, leaderID platform.EmployeeID, nt newTeam, sourceIP string) (Team, error) {
	year := s.event.Year()

	ids := make([]string, 0, 1+len(nt.MemberIDs))
	ids = append(ids, leaderID.String())
	for _, id := range nt.MemberIDs {
		ids = append(ids, id.String())
	}
	rows, err := lockUsers(ctx, tx, ids)
	if err != nil {
		return Team{}, err
	}
	byEmployeeID := make(map[string]userRow, len(rows))
	userIDs := make([]int64, 0, len(rows))
	for _, r := range rows {
		byEmployeeID[r.EmployeeID] = r
		userIDs = append(userIDs, r.ID)
	}

	leader, ok := byEmployeeID[leaderID.String()]
	if !ok {
		// The account vanished after the session check (an engineer deleted it).
		return Team{}, auth.ErrUnauthenticated()
	}
	teamed, err := teamedUserIDs(ctx, tx, year, userIDs)
	if err != nil {
		return Team{}, err
	}
	// TEAM-001, EVT-02. The leader's own state is reported before any list problem.
	if teamed[leader.ID] {
		return Team{}, ErrAlreadyInTeam()
	}

	// TEAM-007 for every ID, collecting all failures (TEAM-008).
	var memberErrs []platform.FieldError
	members := make([]userRow, 0, len(nt.MemberIDs))
	for _, id := range nt.MemberIDs {
		row, found := byEmployeeID[id.String()]
		switch {
		// An Admin ID is reported like an unknown one: R-04 says it can never be a
		// Participant, and there is no reason to tell the leader who is an Admin.
		case !found || row.Role != "PARTICIPANT":
			memberErrs = append(memberErrs, memberError(id.String(), reasonNotRegistered))
		case row.Department.String != leader.Department.String:
			memberErrs = append(memberErrs, memberError(id.String(), reasonDepartmentMismatch))
		case teamed[row.ID]:
			memberErrs = append(memberErrs, memberError(id.String(), reasonAlreadyInTeam))
		default:
			members = append(members, row)
		}
	}
	if len(memberErrs) > 0 {
		return Team{}, auth.ErrValidation(memberErrs)
	}

	// DDL-005: the action completes here, so this is the check that counts.
	if err := s.event.CheckActor(false); err != nil {
		return Team{}, err
	}

	dept := leader.Department.String // TEAM-003
	teamID, err := insertTeam(ctx, tx, year, nt, dept)
	if err != nil {
		return Team{}, err
	}
	if err := insertMember(ctx, tx, teamID, leader.ID, year, RoleLeader); err != nil {
		return Team{}, err
	}
	out := Team{
		ID: teamID, EventYear: year, Name: nt.Name, Track: nt.Track.String(),
		AwardCategory: nt.AwardCategory.String(), Department: dept,
		Members: []Member{{EmployeeID: leader.EmployeeID, FullName: leader.FullName, Role: RoleLeader}},
	}
	memberIDs := make([]string, 0, len(members))
	for _, m := range members {
		if err := insertMember(ctx, tx, teamID, m.ID, year, RoleMember); err != nil {
			return Team{}, err
		}
		out.Members = append(out.Members, Member{EmployeeID: m.EmployeeID, FullName: m.FullName, Role: RoleMember})
		memberIDs = append(memberIDs, m.EmployeeID)
	}

	// AUD-001, AUD-002: committed or rolled back together with the team.
	err = s.audit.Record(ctx, tx, audit.Entry{
		ActorEmployeeID: actor.EmployeeID, ActorRole: actor.Role,
		Action: actionCreate, TargetType: targetTeam, TargetID: strconv.FormatInt(teamID, 10),
		Result: audit.ResultSuccess, SourceIP: sourceIP,
		Changes: map[string]any{"after": map[string]any{
			"eventYear": year, "name": nt.Name, "track": nt.Track.String(),
			"awardCategory": nt.AwardCategory.String(), "department": dept,
			"leader": leader.EmployeeID, "members": memberIDs,
		}},
	})
	if err != nil {
		return Team{}, err
	}
	return out, nil
}

// auditFailure records a refused creation. It runs after the transaction rolled back, so
// the record is not lost with it. A failure to write it is logged and does not change
// the answer the caller gets.
func (s *Service) auditFailure(ctx context.Context, actor auth.SessionUser, sourceIP string, p *platform.Problem) {
	changes := map[string]any{"reason": p.Code}
	if len(p.Errors) > 0 {
		// Field, code and offending ID only; the team name is arbitrary text and stays out.
		changes["errors"] = p.Errors
	}
	err := s.audit.RecordStandalone(ctx, audit.Entry{
		ActorEmployeeID: actor.EmployeeID, ActorRole: actor.Role,
		Action: actionCreate, TargetType: targetTeam,
		Result: audit.ResultFailure, SourceIP: sourceIP, Changes: changes,
	})
	if err != nil {
		s.log.ErrorContext(ctx, "audit team creation failure", "error", err)
	}
}
