package team

import "gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"

// Roster roles, as stored in team_member.role.
const (
	RoleLeader = "LEADER"
	RoleMember = "MEMBER"
)

// Team size limits (TEAM-006) and name length (TEAM-004). Fixed by the PRD, so constants.
const (
	maxOtherMembers = 4 // plus the leader makes the 5 of TEAM-006
	minNameRunes    = 2
	maxNameRunes    = 50 // matches team.name VARCHAR(50)
)

// CreateInput is the raw create-team request. Fields stay strings so every problem is
// reported at once (TEAM-008) instead of failing on the first bad value.
type CreateInput struct {
	Name          string
	Track         string
	AwardCategory string
	MemberIDs     []string
}

// newTeam is a validated, normalized request.
type newTeam struct {
	Name          string
	Track         platform.Track
	AwardCategory platform.AwardCategory
	MemberIDs     []platform.EmployeeID
}

// Member is one person on a created team.
type Member struct {
	EmployeeID string
	FullName   string
	Role       string
}

// Team is what a successful creation returns: the leader first, then the members in the
// order the leader listed them.
type Team struct {
	ID            int64
	EventYear     int
	Name          string
	Track         string
	AwardCategory string
	Department    string
	Members       []Member
}
