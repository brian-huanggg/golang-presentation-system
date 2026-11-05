package team

import (
	"context"
	"strconv"

	"github.com/jmoiron/sqlx"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/auth"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

const actionDissolve = "team.dissolve"

// IdeaCascade is what team needs from the idea feature when a team is dissolved. It is
// declared here, at the consumer, and implemented by package idea; main.go wires it, so
// the import arrow stays idea -> team (Architecture 5.3, ADR 0003).
type IdeaCascade interface {
	// SoftDeleteByTeam hides the team's Idea and its versions inside the caller's
	// transaction. It must succeed when the team has no Idea.
	SoftDeleteByTeam(ctx context.Context, tx sqlx.ExecerContext, teamID int64) error
}

// WithIdeaCascade sets the Idea cascade run on dissolution. Without one (package idea has
// not landed yet) dissolving only touches the team.
func (s *Service) WithIdeaCascade(c IdeaCascade) *Service {
	s.ideas = c
	return s
}

// Dissolve soft-deletes the caller's team (TEAM-014, TEAM-015). In one transaction the
// team is hidden and its name released, its roster is released, its Idea is soft-deleted,
// and the former roster is written to the audit record so an engineer can restore the team
// by hand within 7 days (TEAM-016). The confirmation of NFR-UX-003 belongs to the UI.
// cmd/purge removes the data for good after 7 days.
func (s *Service) Dissolve(ctx context.Context, teamID int64, sourceIP string) error {
	actor, err := s.ident.CurrentUser(ctx)
	if err != nil {
		return err
	}
	return s.rosterChange(ctx, actor, actionDissolve, teamID, sourceIP, ErrAdminCannotDissolve(), func() error {
		return platform.WithTx(ctx, s.db, func(tx *sqlx.Tx) error {
			return s.dissolveInTx(ctx, tx, actor, teamID, sourceIP)
		})
	})
}

func (s *Service) dissolveInTx(ctx context.Context, tx *sqlx.Tx, actor auth.SessionUser, teamID int64, sourceIP string) error {
	t, roster, err := s.leaderOf(ctx, tx, actor, teamID)
	if err != nil {
		return err
	}
	if err := s.event.CheckActor(false); err != nil { // DDL-005
		return err
	}
	if err := softDeleteTeam(ctx, tx, teamID); err != nil {
		return err
	}
	if err := deleteAllMembers(ctx, tx, teamID); err != nil { // release everyone at once
		return err
	}
	if s.ideas != nil {
		if err := s.ideas.SoftDeleteByTeam(ctx, tx, teamID); err != nil {
			return err
		}
	}

	former := make([]map[string]string, 0, len(roster))
	for _, r := range roster {
		former = append(former, map[string]string{"employeeId": r.EmployeeID, "role": r.Role})
	}
	return s.audit.Record(ctx, tx, audit.Entry{
		ActorEmployeeID: actor.EmployeeID, ActorRole: actor.Role,
		Action: actionDissolve, TargetType: targetTeam, TargetID: strconv.FormatInt(teamID, 10),
		Result: audit.ResultSuccess, SourceIP: sourceIP,
		Changes: map[string]any{
			"before": map[string]any{
				"name": t.Name, "track": t.Track, "awardCategory": t.Award,
				"department": t.Department, "eventYear": t.EventYear, "roster": former,
			},
			"after": map[string]any{"deleted": true},
		},
	})
}
