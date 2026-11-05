package team

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/auth"
)

// Dissolving a team: TEAM-014 (leader, while Open), TEAM-015 (soft delete, release),
// TEAM-016 (former roster in the audit record), TEAM-017 (blocked after the deadline).

const (
	softDeleteS    = "UPDATE team SET deleted_at"
	deleteAllMemsS = "DELETE FROM team_member WHERE team_id = ?"
)

type fakeCascade struct {
	calls []int64
	err   error
}

func (c *fakeCascade) SoftDeleteByTeam(_ context.Context, _ sqlx.ExecerContext, id int64) error {
	c.calls = append(c.calls, id)
	return c.err
}

func (f *fixture) expectDissolve() {
	f.mock.ExpectExec(softDeleteS).WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	f.mock.ExpectExec(deleteAllMemsS).WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 2))
}

func TestTEAM_014_LeaderDissolvesAndRosterGoesToAudit(t *testing.T) {
	f := newParticipantFixture(t)
	cascade := &fakeCascade{}
	f.svc.WithIdeaCascade(cascade)
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.expectDissolve()
	f.mock.ExpectCommit()

	require.NoError(t, f.svc.Dissolve(context.Background(), 7, "10.0.0.9"))
	require.NoError(t, f.mock.ExpectationsWereMet())
	assert.Equal(t, []int64{7}, cascade.calls, "the Idea is soft-deleted in the same transaction")

	require.Len(t, f.rec.inTx, 1)
	e := f.rec.inTx[0]
	assert.Equal(t, "team.dissolve", e.Action)
	assert.Equal(t, "7", e.TargetID)
	assert.Equal(t, "10.0.0.9", e.SourceIP)
	changes := e.Changes.(map[string]any)
	before := changes["before"].(map[string]any)
	assert.Equal(t, "Green Wave", before["name"])
	assert.Equal(t, []map[string]string{
		{"employeeId": "100001", "role": "LEADER"},
		{"employeeId": "100002", "role": "MEMBER"},
	}, before["roster"], "TEAM-016: the former roster is recorded")
	assert.Equal(t, map[string]any{"deleted": true}, changes["after"])
	assert.Empty(t, f.rec.standalone)
}

func TestTEAM_015_DissolveWorksWithoutAnIdeaCascade(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader))
	f.expectDissolve()
	f.mock.ExpectCommit()
	require.NoError(t, f.svc.Dissolve(context.Background(), 7, ""))
}

func TestTEAM_015_CascadeFailureRollsEverythingBack(t *testing.T) {
	f := newParticipantFixture(t)
	f.svc.WithIdeaCascade(&fakeCascade{err: errors.New("boom")})
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.expectDissolve()
	f.mock.ExpectRollback()

	require.Error(t, f.svc.Dissolve(context.Background(), 7, ""))
	require.NoError(t, f.mock.ExpectationsWereMet())
	assert.Empty(t, f.rec.inTx, "no audit record without the change")
}

func TestTEAM_014_MemberCannotDissolve(t *testing.T) {
	f := memberFixture(t)
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.mock.ExpectRollback()

	p := problemOf(t, f.svc.Dissolve(context.Background(), 7, ""))
	assert.Equal(t, http.StatusForbidden, p.Status)
	assert.Equal(t, CodeForbidden, p.Code)
	assert.Len(t, f.rec.standalone, 1, "the refusal is audited")
}

func TestTEAM_014_StrangerGetsNotFound(t *testing.T) {
	f := newFixture(t, fakeIdentity{user: auth.SessionUser{EmployeeID: "100009", Role: "PARTICIPANT", Department: "TSID"}})
	f.expectLeadership(rosterOf(memLeader, memAnna))
	f.mock.ExpectRollback()
	assert.Equal(t, CodeTeamNotFound, problemOf(t, f.svc.Dissolve(context.Background(), 7, "")).Code)
}

func TestTEAM_014_UnknownTeamIsNotFound(t *testing.T) {
	f := newParticipantFixture(t)
	f.mock.ExpectBegin()
	f.mock.ExpectQuery(lockTeamSQL).WillReturnRows(sqlmock.NewRows([]string{"id", "event_year", "name", "track", "award_category", "department"}))
	f.mock.ExpectRollback()
	assert.Equal(t, CodeTeamNotFound, problemOf(t, f.svc.Dissolve(context.Background(), 99, "")).Code)
}

func TestTEAM_014_RowAlreadyHiddenIsNotFound(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader))
	f.mock.ExpectExec(softDeleteS).WillReturnResult(sqlmock.NewResult(0, 0))
	f.mock.ExpectRollback()
	assert.Equal(t, CodeTeamNotFound, problemOf(t, f.svc.Dissolve(context.Background(), 7, "")).Code)
}

func TestTEAM_014_AdminCannotDissolve(t *testing.T) {
	f := newFixture(t, fakeIdentity{user: auth.SessionUser{EmployeeID: "000001", Role: "ADMIN"}})
	p := problemOf(t, f.svc.Dissolve(context.Background(), 7, ""))
	assert.Equal(t, http.StatusForbidden, p.Status)
	require.NoError(t, f.mock.ExpectationsWereMet(), "the database is untouched")
}

func TestTEAM_017_CannotDissolveAfterTheDeadline(t *testing.T) {
	f := newParticipantFixture(t)
	f.event.errs = []error{problemFor(http.StatusForbidden, "deadline_passed")}
	assert.Equal(t, "deadline_passed", problemOf(t, f.svc.Dissolve(context.Background(), 7, "")).Code)
	require.NoError(t, f.mock.ExpectationsWereMet(), "the database is untouched")
}

func TestDDL_005_DeadlinePassingDuringDissolveRejectsIt(t *testing.T) {
	f := newParticipantFixture(t)
	f.event.errs = []error{nil, problemFor(http.StatusForbidden, "deadline_passed")}
	f.expectLeadership(rosterOf(memLeader))
	f.mock.ExpectRollback()
	assert.Equal(t, "deadline_passed", problemOf(t, f.svc.Dissolve(context.Background(), 7, "")).Code)
	require.NoError(t, f.mock.ExpectationsWereMet(), "rolled back before any write")
}

func TestTEAM_014_HandlerReturns204(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newParticipantFixture(t)
	f.expectLeadership(rosterOf(memLeader))
	f.expectDissolve()
	f.mock.ExpectCommit()
	r := gin.New()
	r.DELETE("/teams/:id", func(c *gin.Context) { NewHandler(f.svc, quiet()).DissolveTeam(c, 7) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/teams/7", nil))
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}
