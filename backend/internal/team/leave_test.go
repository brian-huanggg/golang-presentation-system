package team

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/auth"
)

// Leaving a team: TEAM-012 (a member may leave while Open), TEAM-013 (the leader may not).

func memberFixture(t *testing.T) *fixture {
	return newFixture(t, fakeIdentity{user: auth.SessionUser{EmployeeID: "100002", Role: "PARTICIPANT", Department: "TSID"}})
}

func TestTEAM_012_MemberLeavesAndItIsAudited(t *testing.T) {
	f := memberFixture(t)
	f.expectLeadership(rosterOf(memLeader, memAnna, memBen))
	f.mock.ExpectExec(deleteMemberS).WithArgs(int64(7), int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	f.mock.ExpectCommit()

	require.NoError(t, f.svc.Leave(context.Background(), 7, "10.0.0.9"))
	require.NoError(t, f.mock.ExpectationsWereMet())

	require.Len(t, f.rec.inTx, 1)
	e := f.rec.inTx[0]
	assert.Equal(t, "team.member_leave", e.Action)
	assert.Equal(t, "100002", e.ActorEmployeeID)
	assert.Equal(t, "7", e.TargetID)
	assert.Equal(t, "10.0.0.9", e.SourceIP)
	assert.Equal(t, map[string]any{
		"before": map[string]any{"members": []string{"100001", "100002", "100003"}},
		"after":  map[string]any{"members": []string{"100001", "100003"}},
	}, e.Changes)
	assert.Empty(t, f.rec.standalone)
}

func TestTEAM_013_LeaderCannotLeave(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.mock.ExpectRollback()

	p := problemOf(t, f.svc.Leave(context.Background(), 7, ""))
	assert.Equal(t, http.StatusConflict, p.Status)
	assert.Equal(t, CodeLeaderCannotLeave, p.Code)
	require.NoError(t, f.mock.ExpectationsWereMet())
	assert.Empty(t, f.rec.inTx)
	require.Len(t, f.rec.standalone, 1, "the refusal is audited")
	assert.Equal(t, "team.member_leave", f.rec.standalone[0].Action)
}

func TestTEAM_012_SomeoneOutsideTheTeamGetsNotFound(t *testing.T) {
	f := newFixture(t, fakeIdentity{user: auth.SessionUser{EmployeeID: "100009", Role: "PARTICIPANT", Department: "TSID"}})
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.mock.ExpectRollback()

	p := problemOf(t, f.svc.Leave(context.Background(), 7, ""))
	assert.Equal(t, http.StatusNotFound, p.Status)
	assert.Equal(t, CodeTeamNotFound, p.Code)
}

func TestTEAM_012_UnknownTeamIsNotFound(t *testing.T) {
	f := memberFixture(t)
	f.mock.ExpectBegin()
	f.mock.ExpectQuery(lockTeamSQL).WillReturnRows(sqlmock.NewRows([]string{"id", "event_year", "name", "track", "award_category", "department"}))
	f.mock.ExpectRollback()

	assert.Equal(t, CodeTeamNotFound, problemOf(t, f.svc.Leave(context.Background(), 99, "")).Code)
}

func TestTEAM_012_RowGoneBeforeDeleteIsNotFound(t *testing.T) {
	f := memberFixture(t)
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.mock.ExpectExec(deleteMemberS).WillReturnResult(sqlmock.NewResult(0, 0))
	f.mock.ExpectRollback()

	assert.Equal(t, CodeTeamNotFound, problemOf(t, f.svc.Leave(context.Background(), 7, "")).Code)
	assert.Empty(t, f.rec.inTx)
}

func TestTEAM_012_AdminCannotLeave(t *testing.T) {
	f := newFixture(t, fakeIdentity{user: auth.SessionUser{EmployeeID: "000001", Role: "ADMIN"}})
	p := problemOf(t, f.svc.Leave(context.Background(), 7, ""))
	assert.Equal(t, http.StatusForbidden, p.Status)
	assert.Equal(t, CodeForbidden, p.Code)
	require.NoError(t, f.mock.ExpectationsWereMet(), "the database is untouched")
}

func TestDDL_003_MemberCannotLeaveAfterTheDeadline(t *testing.T) {
	f := memberFixture(t)
	f.event.errs = []error{problemFor(http.StatusForbidden, "deadline_passed")}
	assert.Equal(t, "deadline_passed", problemOf(t, f.svc.Leave(context.Background(), 7, "")).Code)
	require.NoError(t, f.mock.ExpectationsWereMet(), "the database is untouched")
	assert.Len(t, f.rec.standalone, 1)
}

func TestDDL_005_DeadlinePassingDuringLeaveRejectsIt(t *testing.T) {
	f := memberFixture(t)
	f.event.errs = []error{nil, problemFor(http.StatusForbidden, "deadline_passed")}
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.mock.ExpectRollback()

	assert.Equal(t, "deadline_passed", problemOf(t, f.svc.Leave(context.Background(), 7, "")).Code)
	require.NoError(t, f.mock.ExpectationsWereMet(), "rolled back before the delete")
}

func TestTEAM_012_HandlerReturns204AndMapsLeaderRefusalTo409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	call := func(f *fixture) *httptest.ResponseRecorder {
		r := gin.New()
		r.POST("/teams/:id/leave", func(c *gin.Context) { NewHandler(f.svc, quiet()).LeaveTeam(c, 7) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/teams/7/leave", nil))
		return w
	}

	f := memberFixture(t)
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.mock.ExpectExec(deleteMemberS).WillReturnResult(sqlmock.NewResult(0, 1))
	f.mock.ExpectCommit()
	w := call(f)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))

	f = newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.mock.ExpectRollback()
	w = call(f)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), CodeLeaderCannotLeave)
}
