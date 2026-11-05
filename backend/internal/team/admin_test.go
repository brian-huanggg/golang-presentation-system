package team

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/api"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/auth"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// ADM-001: Admins list and search teams by name, Track, Award Category, department and
// whether an Idea exists. Also AUD-001 (the view is audited) and DDL-006 (no deadline).

const (
	countSQL = "SELECT COUNT\\(\\*\\) FROM team t WHERE"
	listSQL  = "SELECT t.id, t.event_year, t.name"
)

var summaryCols = []string{"id", "event_year", "name", "track", "award_category", "department",
	"leader_employee_id", "leader_full_name", "member_count", "has_idea"}

func newAdminFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t, fakeIdentity{user: auth.SessionUser{EmployeeID: "000001", FullName: "Admin", Role: "ADMIN"}})
	f.mock.ExpectBegin()
	return f
}

func strp(s string) *string { return &s }
func intp(i int) *int       { return &i }
func boolp(b bool) *bool    { return &b }

func TestADM_001_ListsTeamsWithDefaultsAndAuditsTheView(t *testing.T) {
	f := newAdminFixture(t)
	f.mock.ExpectQuery(countSQL).WithArgs(2026).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))
	f.mock.ExpectQuery(listSQL).WithArgs(2026, 20, 0).
		WillReturnRows(sqlmock.NewRows(summaryCols).
			AddRow(7, 2026, "Green Wave", "smart_innovation", "engineering", "TSID", "100001", "Lee Leader", 3, true))
	f.mock.ExpectCommit()

	page, err := f.svc.ListForAdmin(context.Background(), AdminListInput{}, "10.0.0.9")
	require.NoError(t, err)
	assert.Equal(t, int64(1), page.Total)
	assert.Equal(t, 1, page.Page)
	assert.Equal(t, 20, page.Size)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "Green Wave", page.Items[0].Name)
	assert.Equal(t, 3, page.Items[0].MemberCount)
	assert.True(t, page.Items[0].HasIdea)

	// AUD-001: the view is recorded in the same transaction.
	require.Len(t, f.rec.inTx, 1)
	assert.Equal(t, "admin.team_list", f.rec.inTx[0].Action)
	assert.Equal(t, "ADMIN", f.rec.inTx[0].ActorRole)
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestADM_001_AppliesEveryFilterAsABoundArgument(t *testing.T) {
	f := newAdminFixture(t)
	// q is "50%_ x" with % and _ escaped by '|', so it is matched literally.
	f.mock.ExpectQuery(countSQL+".*ESCAPE.*NOT EXISTS").WithArgs(2026, "%50|%|_ x%", "sustainable_development", "public_safety", "BCID").
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
	f.mock.ExpectCommit()

	page, err := f.svc.ListForAdmin(context.Background(), AdminListInput{
		Q: strp("  50%_ x "), Track: strp("sustainable_development"), AwardCategory: strp("public_safety"),
		Department: strp("BCID"), HasIdea: boolp(false),
	}, "10.0.0.9")
	require.NoError(t, err)
	assert.Empty(t, page.Items)
	assert.NotNil(t, page.Items, "an empty page is [] in JSON, not null")
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestADM_001_HasIdeaTrueUsesExists(t *testing.T) {
	f := newAdminFixture(t)
	f.mock.ExpectQuery("WHERE t.event_year = \\? AND t.deleted_at IS NULL AND EXISTS").WithArgs(2026).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
	f.mock.ExpectCommit()
	_, err := f.svc.ListForAdmin(context.Background(), AdminListInput{HasIdea: boolp(true)}, "")
	require.NoError(t, err)
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestADM_001_PagesAndSorts(t *testing.T) {
	f := newAdminFixture(t)
	f.mock.ExpectQuery(countSQL).WithArgs(2026).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(45))
	f.mock.ExpectQuery("ORDER BY t.name DESC, t.id ASC LIMIT \\? OFFSET \\?").WithArgs(2026, 10, 20).
		WillReturnRows(sqlmock.NewRows(summaryCols))
	f.mock.ExpectCommit()

	page, err := f.svc.ListForAdmin(context.Background(), AdminListInput{Page: intp(3), Size: intp(10), Sort: strp("-name")}, "")
	require.NoError(t, err)
	assert.Equal(t, int64(45), page.Total)
	assert.Equal(t, 3, page.Page)
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestADM_001_RejectsInvalidParametersTogether(t *testing.T) {
	f := newFixture(t, fakeIdentity{user: auth.SessionUser{EmployeeID: "000001", Role: "ADMIN"}})
	long := make([]byte, 51)
	for i := range long {
		long[i] = 'a'
	}
	_, err := f.svc.ListForAdmin(context.Background(), AdminListInput{
		Q: strp(string(long)), Track: strp("x"), AwardCategory: strp("x"), Department: strp("x"),
		Page: intp(0), Size: intp(101), Sort: strp("DROP TABLE"),
	}, "10.0.0.9")
	var p *platform.Problem
	require.ErrorAs(t, err, &p)
	assert.Equal(t, http.StatusBadRequest, p.Status)
	assert.Len(t, p.Errors, 7)

	// Every bad parameter is named, and the refusal is audited (AUD-001).
	require.Len(t, f.rec.standalone, 1)
	assert.Equal(t, "failure", string(f.rec.standalone[0].Result))
	errs := f.rec.standalone[0].Changes.(map[string]any)["errors"]
	b, _ := json.Marshal(errs)
	for _, name := range []string{"q", "track", "awardCategory", "department", "page", "size", "sort"} {
		assert.Contains(t, string(b), `"field":"`+name+`"`)
	}
}

func TestADM_001_ParticipantIsForbidden(t *testing.T) {
	f := newParticipantFixture(t)
	_, err := f.svc.ListForAdmin(context.Background(), AdminListInput{}, "10.0.0.9")
	assert.Equal(t, ErrForbidden(), err)
	require.Len(t, f.rec.standalone, 1)
	assert.Equal(t, "admin.team_list", f.rec.standalone[0].Action)
	require.NoError(t, f.mock.ExpectationsWereMet(), "no transaction is opened for a non-Admin")
}

func TestADM_001_DDL_006_AdminIsNotBlockedByTheDeadline(t *testing.T) {
	f := newAdminFixture(t)
	f.event.errs = []error{assert.AnError}
	f.mock.ExpectQuery(countSQL).WithArgs(2026).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
	f.mock.ExpectCommit()
	_, err := f.svc.ListForAdmin(context.Background(), AdminListInput{}, "")
	require.NoError(t, err)
	assert.Zero(t, f.event.calls, "the deadline is not consulted for a read")
}

func TestADM_001_UnauthenticatedIsRejected(t *testing.T) {
	f := newFixture(t, fakeIdentity{err: auth.ErrUnauthenticated()})
	_, err := f.svc.ListForAdmin(context.Background(), AdminListInput{}, "")
	assert.Equal(t, auth.ErrUnauthenticated(), err)
}

func TestADM_001_AuditFailureRollsTheViewBack(t *testing.T) {
	f := newAdminFixture(t)
	f.mock.ExpectQuery(countSQL).WithArgs(2026).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
	f.mock.ExpectCommit()
	f.rec.err = assert.AnError
	_, err := f.svc.ListForAdmin(context.Background(), AdminListInput{}, "")
	assert.Error(t, err)
}

func serveAdminList(t *testing.T, f *fixture, params api.ListAdminTeamsParams) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewHandler(f.svc, quiet())
	// Query parsing belongs to the generated wrapper; the test hands the handler its result.
	r.GET("/admin/teams", func(c *gin.Context) { h.ListAdminTeams(c, params) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/teams", nil))
	return w
}

func TestADM_001_HandlerReturnsThePage(t *testing.T) {
	f := newAdminFixture(t)
	f.mock.ExpectQuery(countSQL).WithArgs(2026, "smart_innovation").
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))
	f.mock.ExpectQuery(listSQL).WithArgs(2026, "smart_innovation", 20, 0).
		WillReturnRows(sqlmock.NewRows(summaryCols).
			AddRow(7, 2026, "Green Wave", "smart_innovation", "engineering", "TSID", "100001", "Lee Leader", 2, false))
	f.mock.ExpectCommit()

	track := api.Track("smart_innovation")
	w := serveAdminList(t, f, api.ListAdminTeamsParams{Track: &track})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, map[string]any{
		"total": float64(1), "page": float64(1), "size": float64(20),
		"items": []any{map[string]any{
			"id": float64(7), "eventYear": float64(2026), "name": "Green Wave",
			"track": "smart_innovation", "awardCategory": "engineering", "department": "TSID",
			"leader":      map[string]any{"employeeId": "100001", "fullName": "Lee Leader", "role": "LEADER"},
			"memberCount": float64(2), "hasIdea": false,
		}},
	}, got)
}

func TestADM_001_HandlerRendersAProblemForAParticipant(t *testing.T) {
	f := newParticipantFixture(t)
	w := serveAdminList(t, f, api.ListAdminTeamsParams{})
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"forbidden"`)
}
