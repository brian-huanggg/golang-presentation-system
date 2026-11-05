package team

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/auth"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// --- fakes -------------------------------------------------------------

type fakeIdentity struct {
	user auth.SessionUser
	err  error
}

func (f fakeIdentity) CurrentUser(context.Context) (auth.SessionUser, error) { return f.user, f.err }

// fakeEvent answers the deadline check from a script: the n-th call returns errs[n]
// (nil once the script runs out), so a test can let the first check pass and fail the
// one inside the transaction (DDL-005).
type fakeEvent struct {
	year  int
	errs  []error
	calls int
}

func (f *fakeEvent) Year() int { return f.year }
func (f *fakeEvent) CheckActor(isAdmin bool) error {
	defer func() { f.calls++ }()
	if f.calls < len(f.errs) {
		return f.errs[f.calls]
	}
	return nil
}

type recorder struct {
	inTx       []audit.Entry
	standalone []audit.Entry
	err        error // returned by Record, to test rollback
}

func (r *recorder) Record(_ context.Context, _ sqlx.ExecerContext, e audit.Entry) error {
	r.inTx = append(r.inTx, e)
	return r.err
}
func (r *recorder) RecordStandalone(_ context.Context, e audit.Entry) error {
	r.standalone = append(r.standalone, e)
	return nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func leaderUser() auth.SessionUser {
	return auth.SessionUser{EmployeeID: "100001", FullName: "Lee Leader", Role: "PARTICIPANT", Department: "TSID"}
}

type fixture struct {
	svc   *Service
	mock  sqlmock.Sqlmock
	rec   *recorder
	event *fakeEvent
}

func newFixture(t *testing.T, id fakeIdentity) *fixture {
	t.Helper()
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	f := &fixture{mock: mock, rec: &recorder{}, event: &fakeEvent{year: 2026}}
	f.svc = NewService(sqlx.NewDb(raw, "mysql"), id, f.event, f.rec, quiet())
	return f
}

func newParticipantFixture(t *testing.T) *fixture {
	return newFixture(t, fakeIdentity{user: leaderUser()})
}

// userRows builds the result of the user lock query. Each entry is
// {id, employee_id, full_name, department, role}.
func userRows(rows ...[]driver.Value) *sqlmock.Rows {
	r := sqlmock.NewRows([]string{"id", "employee_id", "full_name", "department", "role"})
	for _, v := range rows {
		r.AddRow(v...)
	}
	return r
}

func teamedRows(userIDs ...int64) *sqlmock.Rows {
	r := sqlmock.NewRows([]string{"user_id"})
	for _, id := range userIDs {
		r.AddRow(id)
	}
	return r
}

var (
	rowLeader = []driver.Value{int64(1), "100001", "Lee Leader", "TSID", "PARTICIPANT"}
	rowAnna   = []driver.Value{int64(2), "100002", "Anna", "TSID", "PARTICIPANT"}
	rowBen    = []driver.Value{int64(3), "100003", "Ben", "TSID", "PARTICIPANT"}
	rowOther  = []driver.Value{int64(4), "100004", "Other Dept", "BCID", "PARTICIPANT"}
	rowAdmin  = []driver.Value{int64(5), "100005", "An Admin", nil, "ADMIN"}
)

const (
	lockUsersSQL = "SELECT id, employee_id, full_name, department, role FROM `user` WHERE employee_id IN"
	teamedSQL    = "SELECT user_id FROM team_member WHERE event_year"
	insertTeamSQ = "INSERT INTO team "
	insertMemSQL = "INSERT INTO team_member"
)

func problemOf(t *testing.T, err error) *platform.Problem {
	t.Helper()
	var p *platform.Problem
	require.ErrorAs(t, err, &p)
	return p
}

// expectLookup queues the begin, the user lock and the membership read of a creation.
func (f *fixture) expectLookup(users *sqlmock.Rows, teamed *sqlmock.Rows) {
	f.mock.ExpectBegin()
	f.mock.ExpectQuery(lockUsersSQL).WillReturnRows(users)
	f.mock.ExpectQuery(teamedSQL).WillReturnRows(teamed)
}

func createdInput(members ...string) CreateInput {
	in := validCreate()
	in.MemberIDs = members
	return in
}

// --- success -----------------------------------------------------------

func TestTEAM_001_LeaderCreatesTeamAndBecomesItsLeader(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLookup(userRows(rowLeader, rowAnna, rowBen), teamedRows())
	f.mock.ExpectExec(insertTeamSQ).WithArgs(2026, "Green Wave", "smart_innovation", "engineering", "TSID").
		WillReturnResult(sqlmock.NewResult(7, 1))
	f.mock.ExpectExec(insertMemSQL).WithArgs(int64(7), int64(1), 2026, "LEADER").WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectExec(insertMemSQL).WithArgs(int64(7), int64(2), 2026, "MEMBER").WillReturnResult(sqlmock.NewResult(2, 1))
	f.mock.ExpectExec(insertMemSQL).WithArgs(int64(7), int64(3), 2026, "MEMBER").WillReturnResult(sqlmock.NewResult(3, 1))
	f.mock.ExpectCommit()

	got, err := f.svc.Create(context.Background(), createdInput("100002", "100003"), "10.0.0.9")
	require.NoError(t, err)
	require.NoError(t, f.mock.ExpectationsWereMet())

	assert.Equal(t, Team{
		ID: 7, EventYear: 2026, Name: "Green Wave", Track: "smart_innovation",
		AwardCategory: "engineering", Department: "TSID",
		Members: []Member{
			{EmployeeID: "100001", FullName: "Lee Leader", Role: RoleLeader},
			{EmployeeID: "100002", FullName: "Anna", Role: RoleMember},
			{EmployeeID: "100003", FullName: "Ben", Role: RoleMember},
		},
	}, got)
	assert.Empty(t, f.rec.standalone)
}

func TestTEAM_002_ZeroMembersMakesATeamOfOne(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLookup(userRows(rowLeader), teamedRows())
	f.mock.ExpectExec(insertTeamSQ).WillReturnResult(sqlmock.NewResult(8, 1))
	f.mock.ExpectExec(insertMemSQL).WithArgs(int64(8), int64(1), 2026, "LEADER").WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectCommit()

	got, err := f.svc.Create(context.Background(), createdInput(), "")
	require.NoError(t, err)
	require.NoError(t, f.mock.ExpectationsWereMet())
	assert.Len(t, got.Members, 1)
}

func TestTEAM_003_TeamDepartmentIsTheLeadersNotTheRequests(t *testing.T) {
	f := newFixture(t, fakeIdentity{user: auth.SessionUser{EmployeeID: "100004", FullName: "Other Dept", Role: "PARTICIPANT", Department: "BCID"}})
	f.expectLookup(userRows(rowOther), teamedRows())
	f.mock.ExpectExec(insertTeamSQ).WithArgs(2026, "Green Wave", "smart_innovation", "engineering", "BCID").
		WillReturnResult(sqlmock.NewResult(9, 1))
	f.mock.ExpectExec(insertMemSQL).WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectCommit()

	got, err := f.svc.Create(context.Background(), createdInput(), "")
	require.NoError(t, err)
	assert.Equal(t, "BCID", got.Department)
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestAUD_001_SuccessIsAuditedInTheSameTransaction(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLookup(userRows(rowLeader, rowAnna), teamedRows())
	f.mock.ExpectExec(insertTeamSQ).WillReturnResult(sqlmock.NewResult(7, 1))
	f.mock.ExpectExec(insertMemSQL).WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectExec(insertMemSQL).WillReturnResult(sqlmock.NewResult(2, 1))
	f.mock.ExpectCommit()

	_, err := f.svc.Create(context.Background(), createdInput("100002"), "10.0.0.9")
	require.NoError(t, err)

	require.Len(t, f.rec.inTx, 1, "the audit row goes through the business transaction")
	assert.Empty(t, f.rec.standalone)
	e := f.rec.inTx[0]
	assert.Equal(t, "team.create", e.Action)
	assert.Equal(t, audit.ResultSuccess, e.Result)
	assert.Equal(t, "100001", e.ActorEmployeeID)
	assert.Equal(t, "PARTICIPANT", e.ActorRole)
	assert.Equal(t, "team", e.TargetType)
	assert.Equal(t, "7", e.TargetID)
	assert.Equal(t, "10.0.0.9", e.SourceIP)
	assert.Equal(t, map[string]any{"after": map[string]any{
		"eventYear": 2026, "name": "Green Wave", "track": "smart_innovation",
		"awardCategory": "engineering", "department": "TSID",
		"leader": "100001", "members": []string{"100002"},
	}}, e.Changes)
}

func TestTEAM_004_NameIsStoredTrimmed(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLookup(userRows(rowLeader), teamedRows())
	f.mock.ExpectExec(insertTeamSQ).WithArgs(2026, "Green Wave", "smart_innovation", "engineering", "TSID").
		WillReturnResult(sqlmock.NewResult(7, 1))
	f.mock.ExpectExec(insertMemSQL).WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectCommit()

	in := createdInput()
	in.Name = "   Green Wave  "
	_, err := f.svc.Create(context.Background(), in, "")
	require.NoError(t, err)
	require.NoError(t, f.mock.ExpectationsWereMet())
}

// --- TEAM-007 / TEAM-008: member validation, all or nothing -------------

func TestTEAM_007_RejectsEachInvalidMemberWithItsReason(t *testing.T) {
	cases := []struct {
		name   string
		users  *sqlmock.Rows
		teamed *sqlmock.Rows
		ids    []string
		want   platform.FieldError
	}{
		{"unknown id", userRows(rowLeader), teamedRows(), []string{"100099"},
			platform.FieldError{Field: "memberIds", Code: reasonNotRegistered, Value: "100099"}},
		{"admin id looks unknown", userRows(rowLeader, rowAdmin), teamedRows(), []string{"100005"},
			platform.FieldError{Field: "memberIds", Code: reasonNotRegistered, Value: "100005"}},
		{"department mismatch", userRows(rowLeader, rowOther), teamedRows(), []string{"100004"},
			platform.FieldError{Field: "memberIds", Code: reasonDepartmentMismatch, Value: "100004"}},
		{"already in a team", userRows(rowLeader, rowAnna), teamedRows(2), []string{"100002"},
			platform.FieldError{Field: "memberIds", Code: reasonAlreadyInTeam, Value: "100002"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newParticipantFixture(t)
			f.expectLookup(tc.users, tc.teamed)
			f.mock.ExpectRollback()

			_, err := f.svc.Create(context.Background(), createdInput(tc.ids...), "10.0.0.9")
			p := problemOf(t, err)
			assert.Equal(t, http.StatusBadRequest, p.Status)
			assert.Equal(t, auth.CodeValidationFailed, p.Code)
			assert.Equal(t, []platform.FieldError{tc.want}, p.Errors)
			require.NoError(t, f.mock.ExpectationsWereMet(), "rolled back, nothing inserted")
			assert.Empty(t, f.rec.inTx)
		})
	}
}

func TestTEAM_008_OneBadIDFailsTheWholeRequestAndAllReasonsAreListed(t *testing.T) {
	f := newParticipantFixture(t)
	// Anna is fine; Ben is in a team, 100004 is another department, 100099 does not exist.
	f.expectLookup(userRows(rowLeader, rowAnna, rowBen, rowOther), teamedRows(3))
	f.mock.ExpectRollback()

	_, err := f.svc.Create(context.Background(), createdInput("100002", "100003", "100004", "100099"), "")
	p := problemOf(t, err)
	assert.Equal(t, []platform.FieldError{
		{Field: "memberIds", Code: reasonAlreadyInTeam, Value: "100003"},
		{Field: "memberIds", Code: reasonDepartmentMismatch, Value: "100004"},
		{Field: "memberIds", Code: reasonNotRegistered, Value: "100099"},
	}, p.Errors, "in the order the leader listed them; the valid ID is not mentioned")
	require.NoError(t, f.mock.ExpectationsWereMet(), "no insert was attempted")
}

func TestTEAM_008_FailureIsAuditedAfterTheRollback(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLookup(userRows(rowLeader), teamedRows())
	f.mock.ExpectRollback()

	_, err := f.svc.Create(context.Background(), createdInput("100099"), "10.0.0.9")
	require.Error(t, err)
	assert.Empty(t, f.rec.inTx, "nothing is audited as a success")
	require.Len(t, f.rec.standalone, 1)
	e := f.rec.standalone[0]
	assert.Equal(t, audit.ResultFailure, e.Result)
	assert.Equal(t, "team.create", e.Action)
	assert.Equal(t, "100001", e.ActorEmployeeID)
	assert.Equal(t, map[string]any{
		"reason": auth.CodeValidationFailed,
		"errors": []platform.FieldError{{Field: "memberIds", Code: reasonNotRegistered, Value: "100099"}},
	}, e.Changes)
}

func TestTEAM_008_ShapeProblemsNeverOpenATransaction(t *testing.T) {
	f := newParticipantFixture(t)
	in := createdInput("100001", "100002", "100002")
	in.Name = "x"
	_, err := f.svc.Create(context.Background(), in, "")
	p := problemOf(t, err)
	assert.Len(t, p.Errors, 3)
	require.NoError(t, f.mock.ExpectationsWereMet(), "the database is untouched")
	require.Len(t, f.rec.standalone, 1)
}

// --- TEAM-001: who may create -------------------------------------------

func TestTEAM_001_LeaderAlreadyInATeamIsRefused(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLookup(userRows(rowLeader, rowAnna), teamedRows(1))
	f.mock.ExpectRollback()

	_, err := f.svc.Create(context.Background(), createdInput("100002"), "")
	p := problemOf(t, err)
	assert.Equal(t, http.StatusConflict, p.Status)
	assert.Equal(t, CodeAlreadyInTeam, p.Code)
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestTEAM_001_AdminCannotCreateATeam(t *testing.T) {
	f := newFixture(t, fakeIdentity{user: auth.SessionUser{EmployeeID: "000001", FullName: "Admin", Role: "ADMIN"}})
	_, err := f.svc.Create(context.Background(), createdInput(), "")
	p := problemOf(t, err)
	assert.Equal(t, http.StatusForbidden, p.Status)
	assert.Equal(t, CodeForbidden, p.Code)
	require.NoError(t, f.mock.ExpectationsWereMet(), "the database is untouched")
	require.Len(t, f.rec.standalone, 1)
	assert.Equal(t, "ADMIN", f.rec.standalone[0].ActorRole)
}

func TestTEAM_001_RequiresALoggedInUser(t *testing.T) {
	f := newFixture(t, fakeIdentity{err: auth.ErrUnauthenticated()})
	_, err := f.svc.Create(context.Background(), createdInput(), "")
	assert.Equal(t, auth.CodeUnauthenticated, problemOf(t, err).Code)
	require.NoError(t, f.mock.ExpectationsWereMet())
	assert.Empty(t, f.rec.standalone, "no actor to attribute a record to")
}

func TestTEAM_001_SessionUserWhoseAccountVanishedIsUnauthenticated(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLookup(userRows(rowAnna), teamedRows())
	f.mock.ExpectRollback()
	_, err := f.svc.Create(context.Background(), createdInput(), "")
	assert.Equal(t, auth.CodeUnauthenticated, problemOf(t, err).Code)
}

// --- deadline (DDL-003, DDL-005) -----------------------------------------

func TestDDL_003_NoTeamCanBeCreatedAfterTheDeadline(t *testing.T) {
	f := newParticipantFixture(t)
	f.event.errs = []error{problemFor(http.StatusForbidden, "deadline_passed")}
	_, err := f.svc.Create(context.Background(), createdInput("100002"), "")
	assert.Equal(t, "deadline_passed", problemOf(t, err).Code)
	require.NoError(t, f.mock.ExpectationsWereMet(), "the database is untouched")
	require.Len(t, f.rec.standalone, 1)
}

func TestDDL_005_DeadlinePassingDuringTheRequestRejectsIt(t *testing.T) {
	f := newParticipantFixture(t)
	f.event.errs = []error{nil, problemFor(http.StatusForbidden, "deadline_passed")}
	f.expectLookup(userRows(rowLeader), teamedRows())
	f.mock.ExpectRollback()

	_, err := f.svc.Create(context.Background(), createdInput(), "")
	assert.Equal(t, "deadline_passed", problemOf(t, err).Code)
	require.NoError(t, f.mock.ExpectationsWereMet(), "rolled back before any insert")
}

func problemFor(status int, code string) *platform.Problem {
	return platform.NewProblem(status, code, "x")
}

// --- unique-index backstops (TEAM-004, EVT-02) --------------------------

func TestTEAM_004_NameTakenThisYearIsAConflict(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLookup(userRows(rowLeader), teamedRows())
	f.mock.ExpectExec(insertTeamSQ).WillReturnError(&mysql.MySQLError{
		Number: 1062, Message: "Duplicate entry '2026-green wave' for key 'uq_team_name_active'"})
	f.mock.ExpectRollback()

	_, err := f.svc.Create(context.Background(), createdInput(), "")
	p := problemOf(t, err)
	assert.Equal(t, http.StatusConflict, p.Status)
	assert.Equal(t, CodeTeamNameTaken, p.Code)
	require.NoError(t, f.mock.ExpectationsWereMet())
	require.Len(t, f.rec.standalone, 1)
}

func TestTEAM_007_PersonClaimedByAConcurrentTeamIsAConflictNotAnError(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLookup(userRows(rowLeader, rowAnna), teamedRows())
	f.mock.ExpectExec(insertTeamSQ).WillReturnResult(sqlmock.NewResult(7, 1))
	f.mock.ExpectExec(insertMemSQL).WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectExec(insertMemSQL).WillReturnError(&mysql.MySQLError{
		Number: 1062, Message: "Duplicate entry '2026-2' for key 'uq_team_member_year_user'"})
	f.mock.ExpectRollback()

	_, err := f.svc.Create(context.Background(), createdInput("100002"), "")
	p := problemOf(t, err)
	assert.Equal(t, http.StatusConflict, p.Status)
	assert.Equal(t, CodeAlreadyInTeam, p.Code)
	require.NoError(t, f.mock.ExpectationsWereMet(), "the team insert was rolled back too")
}

func TestTEAM_004_OtherDuplicateKeysAreNotMistakenForANameClash(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLookup(userRows(rowLeader), teamedRows())
	f.mock.ExpectExec(insertTeamSQ).WillReturnError(&mysql.MySQLError{
		Number: 1062, Message: "Duplicate entry '7' for key 'PRIMARY'"})
	f.mock.ExpectRollback()

	_, err := f.svc.Create(context.Background(), createdInput(), "")
	assert.Equal(t, "internal_error", platform.AsProblem(err).Code)
}

// --- failure handling -----------------------------------------------------

func TestCreate_DatabaseFailureIsA500WithoutALeak(t *testing.T) {
	f := newParticipantFixture(t)
	f.mock.ExpectBegin()
	f.mock.ExpectQuery(lockUsersSQL).WillReturnError(errors.New("connection refused to db.internal:3306"))
	f.mock.ExpectRollback()

	_, err := f.svc.Create(context.Background(), createdInput(), "")
	require.Error(t, err)
	p := platform.AsProblem(err)
	assert.Equal(t, http.StatusInternalServerError, p.Status)
	assert.NotContains(t, p.Detail, "db.internal")
	assert.Empty(t, f.rec.standalone, "an internal error is not a business refusal")
}

func TestCreate_AuditFailureRollsTheTeamBack(t *testing.T) {
	f := newParticipantFixture(t)
	f.svc.audit = failingAuditor{}
	f.expectLookup(userRows(rowLeader), teamedRows())
	f.mock.ExpectExec(insertTeamSQ).WillReturnResult(sqlmock.NewResult(7, 1))
	f.mock.ExpectExec(insertMemSQL).WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectRollback()

	_, err := f.svc.Create(context.Background(), createdInput(), "")
	require.Error(t, err)
	require.NoError(t, f.mock.ExpectationsWereMet(), "an unaudited team must not commit (AUD-001)")
}

type failingAuditor struct{}

func (failingAuditor) Record(context.Context, sqlx.ExecerContext, audit.Entry) error {
	return errors.New("audit table unavailable")
}
func (failingAuditor) RecordStandalone(context.Context, audit.Entry) error {
	return errors.New("audit table unavailable")
}

func TestCreate_FailedFailureAuditDoesNotChangeTheAnswer(t *testing.T) {
	f := newFixture(t, fakeIdentity{user: auth.SessionUser{EmployeeID: "000001", Role: "ADMIN"}})
	f.svc.audit = failingAuditor{}
	_, err := f.svc.Create(context.Background(), createdInput(), "")
	assert.Equal(t, CodeForbidden, problemOf(t, err).Code)
}
