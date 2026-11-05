package team

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

const leaderID = platform.EmployeeID("100001")

func validCreate() CreateInput {
	return CreateInput{
		Name: "Green Wave", Track: "smart_innovation", AwardCategory: "engineering",
		MemberIDs: []string{"100002", "100003"},
	}
}

func TestTEAM_002_AcceptsNameTrackCategoryAndZeroToFourMembers(t *testing.T) {
	for n := 0; n <= 4; n++ {
		in := validCreate()
		in.MemberIDs = []string{"200001", "200002", "200003", "200004"}[:n]
		nt, errs := validateCreate(in, leaderID)
		require.Empty(t, errs, "%d members", n)
		assert.Len(t, nt.MemberIDs, n)
	}
	nt, errs := validateCreate(validCreate(), leaderID)
	require.Empty(t, errs)
	assert.Equal(t, platform.TrackSmartInnovation, nt.Track)
	assert.Equal(t, platform.CategoryEngineering, nt.AwardCategory)
}

func TestTEAM_002_NilMemberListIsATeamOfOne(t *testing.T) {
	in := validCreate()
	in.MemberIDs = nil
	_, errs := validateCreate(in, leaderID)
	assert.Empty(t, errs)
}

func TestTEAM_004_NameLength(t *testing.T) {
	cases := []struct {
		name string
		in   string
		code string // "" means valid
	}{
		{"empty", "", codeRequired},
		{"blank", "   ", codeRequired},
		{"one char", "A", codeTooShort},
		{"one char padded", "  A  ", codeTooShort},
		{"two chars", "AB", ""},
		{"two CJK chars", "永續", ""},
		{"fifty chars", strings.Repeat("a", 50), ""},
		{"fifty CJK chars", strings.Repeat("字", 50), ""},
		{"fifty-one chars", strings.Repeat("a", 51), codeTooLong},
		{"fifty-one CJK chars", strings.Repeat("字", 51), codeTooLong},
		{"padding is not counted", "  " + strings.Repeat("a", 50) + "  ", ""},
		{"newline inside", "Green\nWave", codeInvalidFormat},
		{"tab inside", "Green\tWave", codeInvalidFormat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validCreate()
			in.Name = tc.in
			nt, errs := validateCreate(in, leaderID)
			if tc.code == "" {
				require.Empty(t, errs)
				assert.Equal(t, strings.TrimSpace(tc.in), nt.Name, "name is stored trimmed")
				return
			}
			assert.Equal(t, []platform.FieldError{{Field: "name", Code: tc.code}}, errs)
		})
	}
}

func TestTEAM_002_TrackAndCategoryMustBeKnownCodes(t *testing.T) {
	in := validCreate()
	in.Track, in.AwardCategory = "", ""
	_, errs := validateCreate(in, leaderID)
	assert.ElementsMatch(t, []platform.FieldError{
		{Field: "track", Code: codeRequired}, {Field: "awardCategory", Code: codeRequired},
	}, errs)

	in.Track, in.AwardCategory = "Smart Innovation", "Engineering"
	_, errs = validateCreate(in, leaderID)
	assert.ElementsMatch(t, []platform.FieldError{
		{Field: "track", Code: codeInvalidValue}, {Field: "awardCategory", Code: codeInvalidValue},
	}, errs, "display names are not codes")

	for _, c := range []string{"engineering", "administration", "manufacturing_process", "transportation", "public_safety"} {
		in = validCreate()
		in.AwardCategory = c
		_, errs = validateCreate(in, leaderID)
		assert.Empty(t, errs, c)
	}
	in = validCreate()
	in.Track = "sustainable_development"
	_, errs = validateCreate(in, leaderID)
	assert.Empty(t, errs)
}

func TestTEAM_006_MoreThanFourOtherMembersIsRejected(t *testing.T) {
	in := validCreate()
	in.MemberIDs = []string{"200001", "200002", "200003", "200004", "200005"}
	_, errs := validateCreate(in, leaderID)
	assert.Equal(t, []platform.FieldError{{Field: "memberIds", Code: codeTooMany}}, errs)
}

func TestTEAM_007_ListedIDShapeIsCheckedPerID(t *testing.T) {
	in := validCreate()
	in.MemberIDs = []string{"12345", "100001", "100002", "100002"}
	nt, errs := validateCreate(in, leaderID)
	assert.Equal(t, []platform.FieldError{
		{Field: "memberIds", Code: reasonInvalidFormat, Value: "12345"},
		{Field: "memberIds", Code: reasonIsLeader, Value: "100001"},
		{Field: "memberIds", Code: reasonDuplicate, Value: "100002"},
	}, errs)
	assert.Equal(t, []platform.EmployeeID{"100002"}, nt.MemberIDs, "the first occurrence is the one kept")
}

func TestTEAM_007_MalformedIDsAreRejectedNotForgiven(t *testing.T) {
	for _, raw := range []string{"", " 100002", "100002 ", "10000a", "１００００２", "1000022", "-00002"} {
		in := validCreate()
		in.MemberIDs = []string{raw}
		_, errs := validateCreate(in, leaderID)
		assert.Equal(t, []platform.FieldError{{Field: "memberIds", Code: reasonInvalidFormat, Value: raw}}, errs, "%q", raw)
	}
}

func TestTEAM_007_EchoedValueIsBounded(t *testing.T) {
	in := validCreate()
	in.MemberIDs = []string{strings.Repeat("9", 5000)}
	_, errs := validateCreate(in, leaderID)
	require.Len(t, errs, 1)
	assert.Len(t, errs[0].Value, maxEchoRunes)
}

func TestTEAM_008_EveryProblemIsReportedTogether(t *testing.T) {
	_, errs := validateCreate(CreateInput{
		Name: "x", Track: "nope", AwardCategory: "",
		MemberIDs: []string{"bad", "100001", "100002", "100002"},
	}, leaderID)
	assert.ElementsMatch(t, []platform.FieldError{
		{Field: "name", Code: codeTooShort},
		{Field: "track", Code: codeInvalidValue},
		{Field: "awardCategory", Code: codeRequired},
		{Field: "memberIds", Code: reasonInvalidFormat, Value: "bad"},
		{Field: "memberIds", Code: reasonIsLeader, Value: "100001"},
		{Field: "memberIds", Code: reasonDuplicate, Value: "100002"},
	}, errs)
}
