package team

import (
	"net/http"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// Stable problem codes the frontend translates (I18N-003: text is English only).
const (
	CodeForbidden     = "forbidden"
	CodeAlreadyInTeam = "already_in_team"
	CodeTeamNameTaken = "team_name_taken"
)

// ErrForbidden is a 403 for an Admin trying to act as a Participant (R-02, TEAM-001).
func ErrForbidden() *platform.Problem {
	return platform.NewProblem(http.StatusForbidden, CodeForbidden, "Only a Participant can create a team.")
}

// ErrAlreadyInTeam is a 409: the caller already belongs to a team this event year
// (TEAM-001, EVT-02).
func ErrAlreadyInTeam() *platform.Problem {
	return platform.NewProblem(http.StatusConflict, CodeAlreadyInTeam, "You are already in a team this year.")
}

// ErrTeamNameTaken is a 409: an active team of this year has the name (TEAM-004).
func ErrTeamNameTaken() *platform.Problem {
	return platform.NewProblem(http.StatusConflict, CodeTeamNameTaken, "A team with this name already exists.")
}

// errMemberRaced is a 409 for the rare case where a person we validated joined another
// team before our insert committed. The unique index caught it; asking the leader to
// retry is right, because a second attempt reports the exact reason per ID (TEAM-008).
func errMemberRaced() *platform.Problem {
	return platform.NewProblem(http.StatusConflict, CodeAlreadyInTeam,
		"A listed person joined another team while the request was processed. Please try again.")
}
