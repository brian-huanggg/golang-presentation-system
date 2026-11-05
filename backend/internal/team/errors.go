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

	CodeTeamNotFound          = "team_not_found"
	CodeTeamFull              = "team_full"
	CodeMemberNotFound        = "member_not_found"
	CodeLeaderCannotBeRemoved = "leader_cannot_be_removed"
	CodeLeaderCannotLeave     = "leader_cannot_leave"
)

// ErrNotLeader is a 403: roster changes belong to the Team Leader (TEAM-010, TEAM-011).
// It is also what an Admin gets (R-02, ADM-005).
func ErrNotLeader() *platform.Problem {
	return platform.NewProblem(http.StatusForbidden, CodeForbidden, "Only the Team Leader can change the team roster.")
}

// ErrTeamNotFound is a 404. It is also the answer for a team the caller does not belong
// to, so a stranger cannot probe which team ids exist.
func ErrTeamNotFound() *platform.Problem {
	return platform.NewProblem(http.StatusNotFound, CodeTeamNotFound, "Team not found.")
}

// ErrTeamFull is a 409: a team has at most 5 people, leader included (TEAM-006).
func ErrTeamFull() *platform.Problem {
	return platform.NewProblem(http.StatusConflict, CodeTeamFull, "The team already has 5 people.")
}

// ErrMemberNotFound is a 404: the Employee ID is not on this team's roster.
func ErrMemberNotFound() *platform.Problem {
	return platform.NewProblem(http.StatusNotFound, CodeMemberNotFound, "That person is not a member of this team.")
}

// ErrLeaderCannotBeRemoved is a 409: the leader can only dissolve the team (TEAM-013).
func ErrLeaderCannotBeRemoved() *platform.Problem {
	return platform.NewProblem(http.StatusConflict, CodeLeaderCannotBeRemoved,
		"The Team Leader cannot be removed. Dissolve the team instead.")
}

// ErrLeaderCannotLeave is a 409: the leader cannot leave, only dissolve (TEAM-013).
func ErrLeaderCannotLeave() *platform.Problem {
	return platform.NewProblem(http.StatusConflict, CodeLeaderCannotLeave,
		"The Team Leader cannot leave the team. Dissolve the team instead.")
}

// ErrAdminCannotLeave is a 403: an Admin is in no team (R-02).
func ErrAdminCannotLeave() *platform.Problem {
	return platform.NewProblem(http.StatusForbidden, CodeForbidden, "Only a Team Member can leave a team.")
}

// ErrAdminCannotDissolve is a 403: an Admin is in no team (R-02, ADM-005).
func ErrAdminCannotDissolve() *platform.Problem {
	return platform.NewProblem(http.StatusForbidden, CodeForbidden, "Only the Team Leader can dissolve a team.")
}

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
