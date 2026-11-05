package idea

import (
	"net/http"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// Stable problem codes the frontend translates (I18N-003: text is English only).
const (
	CodeValidationFailed = "validation_failed"
	CodeInvalidRequest   = "invalid_request"
	CodeForbidden        = "forbidden"
	CodeTeamNotFound     = "team_not_found"
	CodeIdeaExists       = "idea_exists"
	CodeIdeaNotFound     = "idea_not_found"
	CodeIdeaChanged      = "idea_changed"
	CodePreconditionReq  = "precondition_required"
)

// Field problem codes and names for validation_failed.
const (
	fieldTitle   = "title"
	fieldSummary = "summary"
	fieldFile    = "file"
	codeRequired = "required"
	codeTooLong  = "too_long"
	codeBadName  = "invalid_file_name"
)

const (
	roleAdmin     = "ADMIN"
	targetIdea    = "idea"
	actionUpload  = "idea.upload"
	actionReplace = "idea.replace"
)

// ErrValidation is a 400 carrying one entry per invalid field.
func ErrValidation(fields []platform.FieldError) *platform.Problem {
	p := platform.NewProblem(http.StatusBadRequest, CodeValidationFailed, "One or more fields are invalid.")
	p.Errors = fields
	return p
}

// ErrInvalidRequest is a 400 for a request that is not the expected multipart form, or
// whose body ended before it was complete (IDEA-006).
func ErrInvalidRequest() *platform.Problem {
	return platform.NewProblem(http.StatusBadRequest, CodeInvalidRequest, "The request body is not valid.")
}

// errUnauthenticated mirrors auth's 401 (same code) for a session whose account is gone.
func errUnauthenticated() *platform.Problem {
	return platform.NewProblem(http.StatusUnauthorized, "unauthenticated", "Authentication is required.")
}

// ErrForbidden is a 403 for a Team Member: members only read (IDEA-011, IDEA-012).
func ErrForbidden() *platform.Problem {
	return platform.NewProblem(http.StatusForbidden, CodeForbidden,
		"Only the Team Leader or an Admin can change the Idea.")
}

// ErrTeamNotFound is a 404 for an unknown or dissolved team, and also for a Participant
// who is not on the roster: they must not learn that someone else's team exists.
func ErrTeamNotFound() *platform.Problem {
	return platform.NewProblem(http.StatusNotFound, CodeTeamNotFound, "The team was not found.")
}

// ErrIdeaExists is a 409: the team already has an Idea (IDEA-001). The first upload
// creates it; later files replace the file of the existing Idea.
func ErrIdeaExists() *platform.Problem {
	return platform.NewProblem(http.StatusConflict, CodeIdeaExists, "The team already has an Idea.")
}

// ErrIdeaNotFound is a 404 for an unknown or deleted Idea, and for one that belongs to a
// team the caller is not on (the same reasoning as ErrTeamNotFound).
func ErrIdeaNotFound() *platform.Problem {
	return platform.NewProblem(http.StatusNotFound, CodeIdeaNotFound, "The Idea was not found.")
}

// ErrIdeaChanged is a 412: the Idea was updated after the caller opened it (IDEA-030).
func ErrIdeaChanged() *platform.Problem {
	return platform.NewProblem(http.StatusPreconditionFailed, CodeIdeaChanged,
		"Someone else has updated the Idea. Reload it and try again.")
}

// ErrPreconditionRequired is a 428: changing the Idea needs the ETag in If-Match.
func ErrPreconditionRequired() *platform.Problem {
	return platform.NewProblem(http.StatusPreconditionRequired, CodePreconditionReq,
		"The If-Match header with the Idea's ETag is required.")
}
