package team

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serve(t *testing.T, f *fixture, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/teams", NewHandler(f.svc, quiet()).CreateTeam)
	req := httptest.NewRequest(http.MethodPost, "/teams", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestTEAM_001_HandlerReturns201WithTheTeamAndRoster(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLookup(userRows(rowLeader, rowAnna), teamedRows())
	f.mock.ExpectExec(insertTeamSQ).WillReturnResult(sqlmock.NewResult(7, 1))
	f.mock.ExpectExec(insertMemSQL).WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectExec(insertMemSQL).WillReturnResult(sqlmock.NewResult(2, 1))
	f.mock.ExpectCommit()

	w := serve(t, f, `{"name":"Green Wave","track":"smart_innovation","awardCategory":"engineering","memberIds":["100002"]}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, map[string]any{
		"id": float64(7), "eventYear": float64(2026), "name": "Green Wave",
		"track": "smart_innovation", "awardCategory": "engineering", "department": "TSID",
		"members": []any{
			map[string]any{"employeeId": "100001", "fullName": "Lee Leader", "role": "LEADER"},
			map[string]any{"employeeId": "100002", "fullName": "Anna", "role": "MEMBER"},
		},
	}, got)
}

func TestTEAM_002_HandlerAcceptsARequestWithoutMemberIds(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLookup(userRows(rowLeader), teamedRows())
	f.mock.ExpectExec(insertTeamSQ).WillReturnResult(sqlmock.NewResult(7, 1))
	f.mock.ExpectExec(insertMemSQL).WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectCommit()

	w := serve(t, f, `{"name":"Solo","track":"sustainable_development","awardCategory":"public_safety"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
}

func TestTEAM_008_HandlerListsEachInvalidIDWithItsReason(t *testing.T) {
	f := newParticipantFixture(t)
	f.expectLookup(userRows(rowLeader, rowOther), teamedRows())
	f.mock.ExpectRollback()

	w := serve(t, f, `{"name":"Green Wave","track":"smart_innovation","awardCategory":"engineering","memberIds":["100004","100099"]}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
	var got struct {
		Code   string `json:"code"`
		Errors []struct {
			Field string `json:"field"`
			Code  string `json:"code"`
			Value string `json:"value"`
		} `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "validation_failed", got.Code)
	require.Len(t, got.Errors, 2)
	assert.Equal(t, "100004", got.Errors[0].Value)
	assert.Equal(t, "department_mismatch", got.Errors[0].Code)
	assert.Equal(t, "memberIds", got.Errors[0].Field)
	assert.Equal(t, "100099", got.Errors[1].Value)
	assert.Equal(t, "not_registered", got.Errors[1].Code)
}

func TestTEAM_001_HandlerRejectsMalformedBodiesBeforeTheService(t *testing.T) {
	big := `{"name":"` + strings.Repeat("a", maxCreateBody) + `","track":"smart_innovation","awardCategory":"engineering"}`
	for name, body := range map[string]string{
		"not json":        `name=Green`,
		"wrong type":      `{"name":42,"track":"smart_innovation","awardCategory":"engineering"}`,
		"member not text": `{"name":"Green","track":"smart_innovation","awardCategory":"engineering","memberIds":[100002]}`,
		"oversized":       big,
		"empty":           ``,
	} {
		t.Run(name, func(t *testing.T) {
			f := newParticipantFixture(t)
			w := serve(t, f, body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), `"code":"invalid_request"`)
			require.NoError(t, f.mock.ExpectationsWereMet(), "the database is untouched")
		})
	}
}

func TestTEAM_001_HandlerHidesInternalErrors(t *testing.T) {
	f := newParticipantFixture(t)
	f.mock.ExpectBegin().WillReturnError(assert.AnError)
	w := serve(t, f, `{"name":"Green Wave","track":"smart_innovation","awardCategory":"engineering"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), assert.AnError.Error())
}
