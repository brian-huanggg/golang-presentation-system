package auth

import (
	"net/http"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// Stable problem codes the frontend translates (I18N-003: text is English only).
const (
	CodeValidationFailed = "validation_failed"
	CodeInvalidRequest   = "invalid_request"
	CodeEmployeeIDTaken  = "employee_id_taken"
)

// ErrValidation is a 400 carrying one entry per invalid field.
func ErrValidation(fields []platform.FieldError) *platform.Problem {
	p := platform.NewProblem(http.StatusBadRequest, CodeValidationFailed, "One or more fields are invalid.")
	p.Errors = fields
	return p
}

// ErrInvalidRequest is a 400 for a body that is not the expected JSON at all.
func ErrInvalidRequest() *platform.Problem {
	return platform.NewProblem(http.StatusBadRequest, CodeInvalidRequest, "The request body is not valid.")
}

// ErrEmployeeIDTaken is a 409. REG-009 accepts disclosing this on registration
// because there is no email channel to tell the owner; login never does (AUTH-003).
func ErrEmployeeIDTaken() *platform.Problem {
	return platform.NewProblem(http.StatusConflict, CodeEmployeeIDTaken, "This Employee ID is already registered.")
}
