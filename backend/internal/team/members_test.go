package team

import (
	"context"
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/auth"
)

const (
	lockTeamSQL   = "SELECT id, event_year, name, track, award_category, department FROM team WHERE id"
	rosterSQL     = "SELECT m.user_id, u.employee_id, u.full_name, m.role FROM team_member m"
	deleteMemberS = "DELETE FROM team_member WHERE team_id"
)

func teamRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "event_year", "name", "track", "award_category", "department"}).
		AddRow(int64(7), 2026, "Green Wave", "smart_innovation", "engineering", "TSID")
}

// rosterOf builds the roster result. Each entry is {user_id, employee_id, full_name, role}.
func rosterOf(rows ...[]driver.Value) *sqlmock.Rows {
	r := sqlmock.NewRows([]string{"user_id", "employee_id", "full_name", "role"})
	for _, v := range rows {
		r.AddRow(v...)
	}
	return r
}

var (
	memLeader = []driver.Value{int64(1), "100001", "Lee Leader", "LEADER"}
	memAnna   = []driver.Value{int64(2), "100002", "Anna", "MEMBER"}
	memBen    = []driver.Value{int64(3), "100003", "Ben", "MEMBER"}
	memCy     = []driver.Value{int64(6), "100006", "Cy", "MEMBER"}
	memDi     = []driver.Value{int64(8), "100008", "Di", "MEMBER"}
)

// expectLeadership queues begin, team lock and roster read of a roster change.
func (f *fixture) expectLeadership(roster *sqlmock.Rows) {
	f.mock.ExpectBegin()
	f.mock.ExpectQuery(lockTeamSQL).WillReturnRows(teamRows())
	f.mock.ExpectQuery(rosterSQL).WillReturnRows(roster)
}

// --- add: TEAM-010 -------------------------------------------------------

func TestTEAM_010_LeaderAddsAMemberAndItIsAudited(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.mock.ExpectQuery(lockUsersSQL).WillReturnRows(userRows(rowBen))
	f.mock.ExpectQuery(teamedSQL).WillReturnRows(teamedRows())
	f.mock.ExpectExec(insertMemSQL).WithArgs(int64(7), int64(3), 2026, "MEMBER").WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectCommit()

	got, err := f.svc.AddMember(context.Background(), 7, "100003", "10.0.0.9")
	require.NoError(t, err)
	require.NoError(t, f.mock.ExpectationsWereMet())

	assert.Equal(t, []Member{
		{EmployeeID: "100001", FullName: "Lee Leader", Role: RoleLeader},
		{EmployeeID: "100002", FullName: "Anna", Role: RoleMember},
		{EmployeeID: "100003", FullName: "Ben", Role: RoleMember},
	}, got.Members)
	assert.Equal(t, "Green Wave", got.Name)

	require.Len(t, f.rec.inTx, 1)
	e := f.rec.inTx[0]
	assert.Equal(t, "team.member_add", e.Action)
	assert.Equal(t, "7", e.TargetID)
	assert.Equal(t, "10.0.0.9", e.SourceIP)
	assert.Equal(t, map[string]any{
		"before": map[string]any{"members": []string{"100001", "100002"}},
		"after":  map[string]any{"members": []string{"100001", "100002", "100003"}},
	}, e.Changes)
	assert.Empty(t, f.rec.standalone)
}

func TestTEAM_006_TeamOfFiveCannotGrow(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader, memAnna, memBen, memCy, memDi))
	f.mock.ExpectRollback()

	_, err := f.svc.AddMember(context.Background(), 7, "100009", "")
	p := problemOf(t, err)
	assert.Equal(t, http.StatusConflict, p.Status)
	assert.Equal(t, CodeTeamFull, p.Code)
	require.NoError(t, f.mock.ExpectationsWereMet())
	require.Len(t, f.rec.standalone, 1, "the refusal is audited")
}

func TestTEAM_006_TeamOfFourCanTakeTheFifth(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader, memAnna, memBen, memCy))
	f.mock.ExpectQuery(lockUsersSQL).WillReturnRows(userRows(rowAnna))
	f.mock.ExpectQuery(teamedSQL).WillReturnRows(teamedRows())
	f.mock.ExpectExec(insertMemSQL).WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectCommit()

	got, err := f.svc.AddMember(context.Background(), 7, "100002", "")
	require.NoError(t, err)
	assert.Len(t, got.Members, 5)
}

func TestTEAM_007_AddUsesTheCreationRulesForTheNewPerson(t *testing.T) {
	cases := map[string]struct {
		users  *sqlmock.Rows
		teamed *sqlmock.Rows
		id     string
		reason string
	}{
		"unknown":             {userRows(), nil, "100099", "not_registered"},
		"admin looks unknown": {userRows(rowAdmin), nil, "100005", "not_registered"},
		"other department":    {userRows(rowOther), nil, "100004", "department_mismatch"},
		"already in a team":   {userRows(rowBen), teamedRows(3), "100003", "already_in_team"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newParticipantFixture(t)
			f.expectLeadership(rosterOf(memLeader))
			f.mock.ExpectQuery(lockUsersSQL).WillReturnRows(c.users)
			if c.teamed != nil {
				f.mock.ExpectQuery(teamedSQL).WillReturnRows(c.teamed)
			}
			f.mock.ExpectRollback()

			_, err := f.svc.AddMember(context.Background(), 7, c.id, "")
			p := problemOf(t, err)
			assert.Equal(t, http.StatusBadRequest, p.Status)
			require.Len(t, p.Errors, 1)
			assert.Equal(t, "employeeId", p.Errors[0].Field)
			assert.Equal(t, c.reason, p.Errors[0].Code)
			assert.Equal(t, c.id, p.Errors[0].Value)
			require.NoError(t, f.mock.ExpectationsWereMet())
		})
	}
}

func TestTEAM_007_AddRejectsBadShapesWithoutTouchingTheDatabase(t *testing.T) {
	for raw, reason := range map[string]string{
		"12345":        "invalid_format",
		" 100002":      "invalid_format",
		"abcdef":       "invalid_format",
		"":             "invalid_format",
		"100001":       "is_leader",
		"1234567890ab": "invalid_format",
	} {
		t.Run(raw, func(t *testing.T) {
			f := newParticipantFixture(t)
			_, err := f.svc.AddMember(context.Background(), 7, raw, "")
			p := problemOf(t, err)
			require.Len(t, p.Errors, 1)
			assert.Equal(t, reason, p.Errors[0].Code)
			require.NoError(t, f.mock.ExpectationsWereMet())
		})
	}
}

func TestTEAM_010_OnlyTheLeaderMayChangeTheRoster(t *testing.T) {
	t.Run("a member is forbidden", func(t *testing.T) {
		f := newFixture(t, fakeIdentity{user: auth.SessionUser{EmployeeID: "100002", Role: "PARTICIPANT", Department: "TSID"}})
		f.expectLeadership(rosterOf(memLeader, memAnna))
		f.mock.ExpectRollback()
		_, err := f.svc.AddMember(context.Background(), 7, "100003", "")
		p := problemOf(t, err)
		assert.Equal(t, http.StatusForbidden, p.Status)
		assert.Equal(t, CodeForbidden, p.Code)
		require.NoError(t, f.mock.ExpectationsWereMet())
	})
	t.Run("a stranger gets not found", func(t *testing.T) {
		f := newFixture(t, fakeIdentity{user: auth.SessionUser{EmployeeID: "100009", Role: "PARTICIPANT", Department: "TSID"}})
		f.expectLeadership(rosterOf(memLeader, memAnna))
		f.mock.ExpectRollback()
		_, err := f.svc.AddMember(context.Background(), 7, "100003", "")
		assert.Equal(t, CodeTeamNotFound, problemOf(t, err).Code)
	})
	t.Run("an unknown or dissolved team is not found", func(t *testing.T) {
		f := newParticipantFixture(t)
		f.mock.ExpectBegin()
		f.mock.ExpectQuery(lockTeamSQL).WillReturnRows(sqlmock.NewRows([]string{"id", "event_year", "name", "track", "award_category", "department"}))
		f.mock.ExpectRollback()
		_, err := f.svc.AddMember(context.Background(), 99, "100003", "")
		p := problemOf(t, err)
		assert.Equal(t, http.StatusNotFound, p.Status)
		assert.Equal(t, CodeTeamNotFound, p.Code)
	})
	t.Run("an Admin is forbidden before any query", func(t *testing.T) {
		f := newFixture(t, fakeIdentity{user: auth.SessionUser{EmployeeID: "000001", Role: "ADMIN"}})
		_, err := f.svc.AddMember(context.Background(), 7, "100003", "")
		assert.Equal(t, http.StatusForbidden, problemOf(t, err).Status)
		require.NoError(t, f.mock.ExpectationsWereMet())
		require.Len(t, f.rec.standalone, 1)
	})
}

func TestTEAM_010_RacedInsertIsAConflictNotAServerError(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader))
	f.mock.ExpectQuery(lockUsersSQL).WillReturnRows(userRows(rowBen))
	f.mock.ExpectQuery(teamedSQL).WillReturnRows(teamedRows())
	f.mock.ExpectExec(insertMemSQL).WillReturnError(&mysql.MySQLError{
		Number: 1062, Message: "Duplicate entry '2026-3' for key 'uq_team_member_year_user'"})
	f.mock.ExpectRollback()

	_, err := f.svc.AddMember(context.Background(), 7, "100003", "")
	p := problemOf(t, err)
	assert.Equal(t, http.StatusConflict, p.Status)
	assert.Equal(t, CodeAlreadyInTeam, p.Code)
}

// --- remove: TEAM-011 ----------------------------------------------------

func TestTEAM_011_LeaderRemovesAMemberAndItIsAudited(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader, memAnna, memBen))
	f.mock.ExpectExec(deleteMemberS).WithArgs(int64(7), int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	f.mock.ExpectCommit()

	require.NoError(t, f.svc.RemoveMember(context.Background(), 7, "100002", "10.0.0.9"))
	require.NoError(t, f.mock.ExpectationsWereMet())

	require.Len(t, f.rec.inTx, 1)
	e := f.rec.inTx[0]
	assert.Equal(t, "team.member_remove", e.Action)
	assert.Equal(t, map[string]any{
		"before": map[string]any{"members": []string{"100001", "100002", "100003"}},
		"after":  map[string]any{"members": []string{"100001", "100003"}},
	}, e.Changes)
}

func TestTEAM_013_LeaderCannotBeRemoved(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.mock.ExpectRollback()

	p := problemOf(t, f.svc.RemoveMember(context.Background(), 7, "100001", ""))
	assert.Equal(t, http.StatusConflict, p.Status)
	assert.Equal(t, CodeLeaderCannotBeRemoved, p.Code)
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestTEAM_011_RemovingSomeoneNotOnTheTeamIsNotFound(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.mock.ExpectRollback()

	p := problemOf(t, f.svc.RemoveMember(context.Background(), 7, "100003", ""))
	assert.Equal(t, http.StatusNotFound, p.Status)
	assert.Equal(t, CodeMemberNotFound, p.Code)
}

func TestTEAM_011_RowGoneBeforeDeleteIsNotFound(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.mock.ExpectExec(deleteMemberS).WillReturnResult(sqlmock.NewResult(0, 0))
	f.mock.ExpectRollback()

	assert.Equal(t, CodeMemberNotFound, problemOf(t, f.svc.RemoveMember(context.Background(), 7, "100002", "")).Code)
	assert.Empty(t, f.rec.inTx)
}

func TestTEAM_011_OnlyTheLeaderMayRemove(t *testing.T) {
	f := newFixture(t, fakeIdentity{user: auth.SessionUser{EmployeeID: "100002", Role: "PARTICIPANT", Department: "TSID"}})
	f.expectLeadership(rosterOf(memLeader, memAnna, memBen))
	f.mock.ExpectRollback()
	p := problemOf(t, f.svc.RemoveMember(context.Background(), 7, "100003", ""))
	assert.Equal(t, http.StatusForbidden, p.Status)
}

func TestTEAM_011_RemoveRejectsAMalformedID(t *testing.T) {
	f := newParticipantFixture(t)
	p := problemOf(t, f.svc.RemoveMember(context.Background(), 7, "12x", ""))
	require.Len(t, p.Errors, 1)
	assert.Equal(t, "invalid_format", p.Errors[0].Code)
	require.NoError(t, f.mock.ExpectationsWereMet())
}

// --- deadline: DDL-003, DDL-005 -----------------------------------------

func TestDDL_003_RosterCannotChangeAfterTheDeadline(t *testing.T) {
	f := newParticipantFixture(t)
	f.event.errs = []error{problemFor(http.StatusForbidden, "deadline_passed"), problemFor(http.StatusForbidden, "deadline_passed")}
	_, err := f.svc.AddMember(context.Background(), 7, "100003", "")
	assert.Equal(t, "deadline_passed", problemOf(t, err).Code)
	assert.Equal(t, "deadline_passed", problemOf(t, f.svc.RemoveMember(context.Background(), 7, "100002", "")).Code)
	require.NoError(t, f.mock.ExpectationsWereMet(), "the database is untouched")
	assert.Len(t, f.rec.standalone, 2)
}

func TestDDL_005_DeadlinePassingDuringAddOrRemoveRejectsIt(t *testing.T) {
	t.Run("add", func(t *testing.T) {
		f := newParticipantFixture(t)
		f.event.errs = []error{nil, problemFor(http.StatusForbidden, "deadline_passed")}
		f.expectLeadership(rosterOf(memLeader))
		f.mock.ExpectQuery(lockUsersSQL).WillReturnRows(userRows(rowBen))
		f.mock.ExpectQuery(teamedSQL).WillReturnRows(teamedRows())
		f.mock.ExpectRollback()
		_, err := f.svc.AddMember(context.Background(), 7, "100003", "")
		assert.Equal(t, "deadline_passed", problemOf(t, err).Code)
		require.NoError(t, f.mock.ExpectationsWereMet(), "rolled back before the insert")
	})
	t.Run("remove", func(t *testing.T) {
		f := newParticipantFixture(t)
		f.event.errs = []error{nil, problemFor(http.StatusForbidden, "deadline_passed")}
		f.expectLeadership(rosterOf(memLeader, memAnna))
		f.mock.ExpectRollback()
		assert.Equal(t, "deadline_passed", problemOf(t, f.svc.RemoveMember(context.Background(), 7, "100002", "")).Code)
		require.NoError(t, f.mock.ExpectationsWereMet(), "rolled back before the delete")
	})
}

func TestAUD_001_RosterFailureIsAuditedWithTeamAndReason(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.mock.ExpectRollback()
	_ = f.svc.RemoveMember(context.Background(), 7, "100003", "10.0.0.9")

	require.Len(t, f.rec.standalone, 1)
	e := f.rec.standalone[0]
	assert.Equal(t, "team.member_remove", e.Action)
	assert.Equal(t, "7", e.TargetID)
	assert.Equal(t, "10.0.0.9", e.SourceIP)
	assert.Equal(t, "failure", string(e.Result))
	assert.Equal(t, map[string]any{"reason": CodeMemberNotFound}, e.Changes)
}

func TestTEAM_010_InternalErrorsAreNotAuditedAsRefusals(t *testing.T) {
	f := newParticipantFixture(t)
	f.mock.ExpectBegin().WillReturnError(assert.AnError)
	_, err := f.svc.AddMember(context.Background(), 7, "100003", "")
	require.ErrorIs(t, err, assert.AnError)
	assert.Empty(t, f.rec.standalone)
}

// --- handlers ------------------------------------------------------------

func serveMembers(t *testing.T, f *fixture, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewHandler(f.svc, quiet())
	r.POST("/teams/:id/members", func(c *gin.Context) { h.AddTeamMember(c, 7) })
	r.DELETE("/teams/:id/members/:eid", func(c *gin.Context) { h.RemoveTeamMember(c, 7, c.Param("eid")) })
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestTEAM_010_HandlerAddReturns201WithTheTeam(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader))
	f.mock.ExpectQuery(lockUsersSQL).WillReturnRows(userRows(rowBen))
	f.mock.ExpectQuery(teamedSQL).WillReturnRows(teamedRows())
	f.mock.ExpectExec(insertMemSQL).WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectCommit()

	w := serveMembers(t, f, http.MethodPost, "/teams/7/members", `{"employeeId":"100003"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	assert.Contains(t, w.Body.String(), `"employeeId":"100003"`)
}

func TestTEAM_010_HandlerRejectsMalformedBodies(t *testing.T) {
	for name, body := range map[string]string{
		"not json": `employeeId=100003`,
		"number":   `{"employeeId":100003}`,
		"empty":    ``,
		"oversize": `{"employeeId":"` + strings.Repeat("1", maxMemberBody) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newParticipantFixture(t)
			w := serveMembers(t, f, http.MethodPost, "/teams/7/members", body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), `"code":"invalid_request"`)
			require.NoError(t, f.mock.ExpectationsWereMet())
		})
	}
}

func TestTEAM_011_HandlerRemoveReturns204(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.mock.ExpectExec(deleteMemberS).WillReturnResult(sqlmock.NewResult(0, 1))
	f.mock.ExpectCommit()

	w := serveMembers(t, f, http.MethodDelete, "/teams/7/members/100002", "")
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Empty(t, w.Body.String())
}

func TestTEAM_011_HandlerRemoveMapsProblemsAndHidesInternalErrors(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader))
	f.mock.ExpectRollback()
	w := serveMembers(t, f, http.MethodDelete, "/teams/7/members/100001", "")
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), CodeLeaderCannotBeRemoved)

	f = newParticipantFixture(t)
	f.mock.ExpectBegin().WillReturnError(assert.AnError)
	w = serveMembers(t, f, http.MethodDelete, "/teams/7/members/100002", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), assert.AnError.Error())
}
