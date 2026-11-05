package auth

import (
	"unicode"
	"unicode/utf8"
)

// minPasswordRunes is REG-005's minimum length. Counted in runes, not bytes, so a
// CJK password is not judged shorter than it looks to the user. The upper bound
// (maxPasswordBytes) stays in bytes because it limits hashing work.
const minPasswordRunes = 12

// Field error codes for the REG-005 password policy, stable for the frontend.
const (
	pwTooShort = "password_too_short"
	pwNoUpper  = "password_no_upper"
	pwNoLower  = "password_no_lower"
	pwNoSymbol = "password_no_symbol"
	pwSequence = "password_sequence"
	pwRepeat   = "password_repeat"
)

// passwordViolations returns the code of every REG-005 rule pw breaks, in a fixed
// order, or nil when it complies. All rules are reported at once so the user can
// fix the password in one round trip.
func passwordViolations(pw string) []string {
	var out []string
	if utf8.RuneCountInString(pw) < minPasswordRunes {
		out = append(out, pwTooShort)
	}

	var upper, lower, symbol bool
	for _, r := range pw {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			// Spaces are neither punctuation nor symbols, so they never count.
			symbol = true
		}
	}
	if !upper {
		out = append(out, pwNoUpper)
	}
	if !lower {
		out = append(out, pwNoLower)
	}
	if !symbol {
		out = append(out, pwNoSymbol)
	}

	if hasSequence(pw) {
		out = append(out, pwSequence)
	}
	if hasRepeat(pw) {
		out = append(out, pwRepeat)
	}
	return out
}

// hasSequence reports 3+ consecutive ASCII letters (case-insensitive) or digits,
// ascending or descending (abc, cba, 123, 321). It does not wrap (890, yza) and
// never mixes classes (c12): those are not runs a person would call a sequence.
func hasSequence(pw string) bool {
	rs := []rune(pw)
	for i := 0; i+2 < len(rs); i++ {
		a, ok1 := seqKey(rs[i])
		b, ok2 := seqKey(rs[i+1])
		c, ok3 := seqKey(rs[i+2])
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		if d := b - a; (d == 1 || d == -1) && c-b == d {
			return true
		}
	}
	return false
}

// seqKey maps an ASCII letter or digit to a value in a class-specific range so
// that letters and digits can never be adjacent: digits 0-9 -> 0..9, letters
// a-z/A-Z -> 100..125. Anything else is not part of a sequence.
func seqKey(r rune) (int, bool) {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0'), true
	case r >= 'a' && r <= 'z':
		return 100 + int(r-'a'), true
	case r >= 'A' && r <= 'Z':
		return 100 + int(r-'A'), true
	}
	return 0, false
}

// hasRepeat reports the same character 3+ times in a row (aaa, 111, !!!).
// Comparison is exact, so aAa is not a repeat.
func hasRepeat(pw string) bool {
	var prev rune
	run := 0
	for _, r := range pw {
		if run > 0 && r == prev {
			run++
		} else {
			prev, run = r, 1
		}
		if run >= 3 {
			return true
		}
	}
	return false
}
