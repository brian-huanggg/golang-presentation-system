package platform

import (
	"encoding/json"
	"errors"
	"net/http"
)

// ProblemContentType is the RFC 9457 media type.
const ProblemContentType = "application/problem+json"

// Problem is an RFC 9457 Problem Details object. Title and Detail are English
// (I18N-003); Code is the stable identifier the frontend translates.
type Problem struct {
	Type     string `json:"type,omitempty"` // omitted means "about:blank"
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
	Code     string `json:"code"`
	// Errors carries per-field validation failures.
	Errors []FieldError `json:"errors,omitempty"`
}

// FieldError is one invalid input field.
type FieldError struct {
	Field string `json:"field"`
	Code  string `json:"code"`
	// Value is the offending element when Field is a list (for example one Employee ID
	// in a roster), so a response can say which entry failed and why (TEAM-008).
	Value string `json:"value,omitempty"`
}

// Error makes *Problem usable as an error that services return and the error
// middleware renders.
func (p *Problem) Error() string {
	if p.Detail != "" {
		return p.Code + ": " + p.Detail
	}
	return p.Code + ": " + p.Title
}

// NewProblem builds a Problem with the standard HTTP status text as title.
func NewProblem(status int, code, detail string) *Problem {
	return &Problem{Title: http.StatusText(status), Status: status, Code: code, Detail: detail}
}

// Generic problems shared by every feature.
var (
	ErrInternal = func() *Problem {
		return NewProblem(http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
	}
)

// AsProblem converts any error into a Problem. Unknown errors become a generic
// 500 so internal messages never leak to clients.
func AsProblem(err error) *Problem {
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	return ErrInternal()
}

// WriteProblem renders p as application/problem+json. instance is the request
// path; it is set only when p has none.
func WriteProblem(w http.ResponseWriter, instance string, p *Problem) {
	out := *p // do not mutate a shared value
	if out.Instance == "" {
		out.Instance = instance
	}
	w.Header().Set("Content-Type", ProblemContentType)
	w.WriteHeader(out.Status)
	_ = json.NewEncoder(w).Encode(out)
}
