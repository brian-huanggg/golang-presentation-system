package auth

import (
	"net/mail"
	"strings"
	"unicode/utf8"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

const (
	maxFullNameRunes = 100 // matches user.full_name VARCHAR(100)
	maxEmailLen      = 254 // matches user.email VARCHAR(254)
)

// Field error codes, stable for the frontend.
const (
	fieldRequired      = "required"
	fieldInvalidFormat = "invalid_format"
	fieldInvalidValue  = "invalid_value"
	fieldTooLong       = "too_long"
)

// RegisterInput is the raw registration request. Fields stay strings so that
// every problem is reported at once instead of failing on the first bad value.
type RegisterInput struct {
	EmployeeID        string
	FullName          string
	Department        string
	Password          string
	PreferredLanguage string
	Email             string // optional (REG-008)
}

// newUser is a validated, normalized registration ready to store.
type newUser struct {
	EmployeeID        platform.EmployeeID
	FullName          string
	Department        platform.Department
	PreferredLanguage string
	Email             string // "" means none
}

// validateRegister checks REG-001/002/004/008 and returns the normalized user
// or the per-field failures. JSON names are used as field names (they match the API).
func validateRegister(in RegisterInput) (newUser, []platform.FieldError) {
	var errs []platform.FieldError
	bad := func(field, code string) { errs = append(errs, platform.FieldError{Field: field, Code: code}) }
	var u newUser

	// REG-002: exactly 6 digits. No trimming: spaces are rejected, not forgiven.
	if in.EmployeeID == "" {
		bad("employeeId", fieldRequired)
	} else if id, err := platform.ParseEmployeeID(in.EmployeeID); err != nil {
		bad("employeeId", fieldInvalidFormat)
	} else {
		u.EmployeeID = id
	}

	u.FullName = strings.TrimSpace(in.FullName)
	switch {
	case u.FullName == "":
		bad("fullName", fieldRequired)
	case utf8.RuneCountInString(u.FullName) > maxFullNameRunes:
		bad("fullName", fieldTooLong)
	}

	if in.Department == "" {
		bad("department", fieldRequired)
	} else if d, err := platform.ParseDepartment(in.Department); err != nil {
		bad("department", fieldInvalidValue)
	} else {
		u.Department = d
	}

	switch in.PreferredLanguage {
	case "zh-TW", "en":
		u.PreferredLanguage = in.PreferredLanguage
	case "":
		bad("preferredLanguage", fieldRequired)
	default:
		bad("preferredLanguage", fieldInvalidValue)
	}

	if code := checkPassword(in.Password); code != "" {
		bad("password", code)
	}

	// REG-008: format check only; not unique, not verified.
	if email := strings.TrimSpace(in.Email); email != "" {
		switch {
		case len(email) > maxEmailLen:
			bad("email", fieldTooLong)
		case !validEmail(email):
			bad("email", fieldInvalidFormat)
		default:
			u.Email = email
		}
	}
	return u, errs
}

// checkPassword returns a field error code, or "" when acceptable.
// TODO(REG-005): the composition rules (length 12, character classes, no
// sequences or repeats) belong to their own ticket and plug in here; until then
// only presence and an upper bound are enforced.
func checkPassword(pw string) string {
	switch {
	case pw == "":
		return fieldRequired
	case len(pw) > maxPasswordBytes:
		return fieldTooLong
	}
	return ""
}

// validEmail accepts a bare addr-spec only; "Name <a@b>" is a display-name form
// we do not want to store.
func validEmail(s string) bool {
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Address == s
}
