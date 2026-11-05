package auth

import (
	"reflect"
	"strings"
	"testing"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// Table-driven tests for the REG-005 password policy. Test names carry the
// requirement ID so NFR-QA-003 traceability can be checked by grep.

func TestREG_005_passwordViolations(t *testing.T) {
	tests := []struct {
		name string
		pw   string
		want []string // nil means the password is acceptable
	}{
		// Acceptable
		{"valid mixed", "Xk9!pQ4#mW2z", nil},
		{"valid exactly 12 runes", "Ab!dEg@hIk#l", nil},
		{"valid CJK counted by runes", "密碼密碼密碼密碼密碼Ab!d", nil},

		// Length (rune count, not bytes)
		{"11 runes too short", "Ab!dEg@hIk#", []string{pwTooShort}},
		{"CJK 11 runes too short even if >11 bytes", "密碼密碼密碼密碼Ab!", []string{pwTooShort}},

		// Character classes
		{"no uppercase", "xk9!pq4#mw2z", []string{pwNoUpper}},
		{"no lowercase", "XK9!PQ4#MW2Z", []string{pwNoLower}},
		{"no symbol", "Xk9ApQ4BmW2z", []string{pwNoSymbol}},
		{"space is not a symbol", "Xk9 pQ4 mW2z", []string{pwNoSymbol}},
		{"CJK letters are neither upper nor lower", "密碼密碼密碼密碼密碼密碼!", []string{pwNoUpper, pwNoLower}},
		{"unicode symbol counts", "Xk9€pQ4Hm2zq", nil},

		// Sequences: ascending and descending, letters and digits
		{"ascending letters abc", "Xk!abcQ4mW2z", []string{pwSequence}},
		{"descending letters cba", "Xk!cbaQ4mW2z", []string{pwSequence}},
		{"ascending digits 123", "Xk!pQ123mWzq", []string{pwSequence}},
		{"descending digits 321", "Xk!pQ321mWzq", []string{pwSequence}},
		{"mixed-case sequence aBc", "Xk!aBcQ4mW2z", []string{pwSequence}},
		{"sequence at the very end", "Xk9!pQ4#mXyz", []string{pwSequence}},
		{"sequence at the very start", "abcXk9!pQ4#M", []string{pwSequence}},
		{"789 is a sequence", "Xk!pQ789mWzq", []string{pwSequence}},
		{"no wrap 890", "Xk!pQ890mWzq", nil},
		{"no wrap yza", "Xk!yzaQ4mW2z", nil},
		{"gap is not a sequence abd", "Xk!abdQ4mW2z", nil},
		{"two in a row is fine ab", "Xk!abQ4mW2zq", nil},
		{"letter-digit mix is not a sequence c12", "Xk!c12Q4mWzq", nil},
		{"non-consecutive digits 135", "Xk!pQ135mWzq", nil},

		// Repeats: same character 3+ times
		{"repeat letters aaa", "Xk!aaaQ4mW2z", []string{pwRepeat}},
		{"repeat digits 111", "Xk!pQ111mWzq", []string{pwRepeat}},
		{"repeat symbol !!!", "Xk9!!!Q4mW2z", []string{pwRepeat}},
		{"four repeats", "Xk!aaaaQ4mW2z", []string{pwRepeat}},
		{"two repeats are fine", "Xk!aaQ4mW2zq", nil},
		{"repeat is case sensitive aAa", "Xk!aAaQ4mW2z", nil},
		{"non-adjacent repeats are fine", "Xk!aQaQaQ4mWz", nil},

		// Several violations are all reported, in a fixed order
		{"everything wrong", "abc", []string{pwTooShort, pwNoUpper, pwNoSymbol, pwSequence}},
		{"all lowercase digits repeat", "aaaaaaaaaaaa", []string{pwNoUpper, pwNoSymbol, pwRepeat}},
		{"short symbol-only", "!!!", []string{pwTooShort, pwNoUpper, pwNoLower, pwRepeat}},

		// AC-REG-005: contains bcd / xyz
		{"AC-REG-005 Abcdef!Xyz12Q", "Abcdef!Xyz12Q", []string{pwSequence}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := passwordViolations(tc.pw)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("passwordViolations(%q) = %v, want %v", tc.pw, got, tc.want)
			}
		})
	}
}

func TestAC_REG_005_rejectsAbcdefXyz12Q(t *testing.T) {
	got := passwordViolations("Abcdef!Xyz12Q")
	if len(got) != 1 || got[0] != pwSequence {
		t.Fatalf("Abcdef!Xyz12Q must be rejected for a sequence, got %v", got)
	}
}

func TestREG_005_checkPassword(t *testing.T) {
	tests := []struct {
		name string
		pw   string
		want []string
	}{
		{"empty is required", "", []string{fieldRequired}},
		{"over 128 bytes is too long and skips composition rules", strings.Repeat("a", 129), []string{fieldTooLong}},
		{"exactly 128 bytes is not too long", strings.Repeat("Xk9!pQ4#", 16), nil},
		{"weak password reports every violation", "abc", []string{pwTooShort, pwNoUpper, pwNoSymbol, pwSequence}},
		{"strong password passes", "Xk9!pQ4#mW2z", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkPassword(tc.pw)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("checkPassword(%q) = %v, want %v", tc.pw, got, tc.want)
			}
		})
	}
}

// REG-005 through the registration validator: one FieldError per violated rule.
func TestREG_005_validateRegisterReportsEachViolation(t *testing.T) {
	in := RegisterInput{
		EmployeeID: "123456", FullName: "王小明", Department: "TSID",
		PreferredLanguage: "en", Password: "Abcdef!Xyz12Q",
	}
	_, errs := validateRegister(in)
	want := []platform.FieldError{{Field: "password", Code: pwSequence}}
	if !reflect.DeepEqual(errs, want) {
		t.Fatalf("errs = %v, want %v", errs, want)
	}

	in.Password = "abc"
	_, errs = validateRegister(in)
	want = []platform.FieldError{
		{Field: "password", Code: pwTooShort},
		{Field: "password", Code: pwNoUpper},
		{Field: "password", Code: pwNoSymbol},
		{Field: "password", Code: pwSequence},
	}
	if !reflect.DeepEqual(errs, want) {
		t.Fatalf("errs = %v, want %v", errs, want)
	}
}
