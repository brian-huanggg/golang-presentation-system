package team

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// Field names are the JSON names of the API.
const (
	fieldName          = "name"
	fieldTrack         = "track"
	fieldAwardCategory = "awardCategory"
	fieldMemberIDs     = "memberIds"
)

// Field and member reason codes, stable for the frontend.
const (
	codeRequired      = "required"
	codeTooShort      = "too_short"
	codeTooLong       = "too_long"
	codeInvalidFormat = "invalid_format"
	codeInvalidValue  = "invalid_value"
	codeTooMany       = "too_many"

	// Reasons for one listed Employee ID (TEAM-007, TEAM-008).
	reasonInvalidFormat      = "invalid_format"
	reasonDuplicate          = "duplicate"
	reasonIsLeader           = "is_leader"
	reasonNotRegistered      = "not_registered"
	reasonAlreadyInTeam      = "already_in_team"
	reasonDepartmentMismatch = "department_mismatch"
)

// maxEchoRunes bounds how much of a malformed ID is echoed back in an error, so a
// hostile request cannot make the response large.
const maxEchoRunes = 20

// validateCreate checks everything that needs no database: TEAM-002, TEAM-004, TEAM-006
// and the shape of every listed ID (format, duplicates, the leader's own ID). It
// returns every problem found, never just the first (TEAM-008). Checks that need the
// database (registered, free, same department) happen in the service, inside the transaction.
func validateCreate(in CreateInput, leader platform.EmployeeID) (newTeam, []platform.FieldError) {
	var errs []platform.FieldError
	bad := func(field, code string) { errs = append(errs, platform.FieldError{Field: field, Code: code}) }
	var t newTeam

	// Trimmed so " A " and "A" are the same name; the case-insensitive collation of the
	// unique index then makes "team" and "TEAM" collide as TEAM-004 requires.
	t.Name = strings.TrimSpace(in.Name)
	switch n := utf8.RuneCountInString(t.Name); {
	case n == 0:
		bad(fieldName, codeRequired)
	case n < minNameRunes:
		bad(fieldName, codeTooShort)
	case n > maxNameRunes:
		bad(fieldName, codeTooLong)
	case strings.ContainsFunc(t.Name, unicode.IsControl):
		bad(fieldName, codeInvalidFormat)
	}

	if in.Track == "" {
		bad(fieldTrack, codeRequired)
	} else if v, err := platform.ParseTrack(in.Track); err != nil {
		bad(fieldTrack, codeInvalidValue)
	} else {
		t.Track = v
	}

	if in.AwardCategory == "" {
		bad(fieldAwardCategory, codeRequired)
	} else if v, err := platform.ParseAwardCategory(in.AwardCategory); err != nil {
		bad(fieldAwardCategory, codeInvalidValue)
	} else {
		t.AwardCategory = v
	}

	// A team has at most 5 people with the leader (TEAM-006). Stop here: reporting the
	// shape of an oversized list adds nothing.
	if len(in.MemberIDs) > maxOtherMembers {
		bad(fieldMemberIDs, codeTooMany)
		return t, errs
	}

	seen := make(map[string]bool, len(in.MemberIDs))
	for _, raw := range in.MemberIDs {
		// No trimming, as at registration (REG-002): " 123456" is not an Employee ID.
		id, err := platform.ParseEmployeeID(raw)
		if err != nil {
			errs = append(errs, memberError(echo(raw), reasonInvalidFormat))
			continue
		}
		switch {
		case id == leader:
			errs = append(errs, memberError(raw, reasonIsLeader))
		case seen[raw]:
			// Every repetition is reported; the first occurrence is the one that counts.
			errs = append(errs, memberError(raw, reasonDuplicate))
		default:
			t.MemberIDs = append(t.MemberIDs, id)
		}
		seen[raw] = true
	}
	return t, errs
}

func memberError(id, reason string) platform.FieldError {
	return platform.FieldError{Field: fieldMemberIDs, Code: reason, Value: id}
}

// echo shortens caller-supplied text that is sent back in an error.
func echo(s string) string {
	if utf8.RuneCountInString(s) <= maxEchoRunes {
		return s
	}
	return string([]rune(s)[:maxEchoRunes])
}
