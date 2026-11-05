package idea

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/api"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/event"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/team"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/upload"
)

const (
	selectTeamSQL    = "SELECT team_id FROM idea WHERE id"
	lockIdeaSQL      = "SELECT team_id, title, summary, current_version_no, version FROM idea"
	advanceIdeaSQL   = "UPDATE idea SET current_version_no"
	selectOldSQL     = "SELECT version_no, object_key FROM idea_version"
	deleteOldSQL     = "DELETE FROM idea_version"
	replaceRevision  = int64(3)
	replaceCurrentNo = 4
)

func replaceInput() ReplaceInput {
	return ReplaceInput{
		IdeaID: 42, ExpectedRevision: replaceRevision,
		FileName: "deck-v5.pdf", ContentType: "application/pdf", Data: pdfBytes,
	}
}

// expectPreCheck scripts the unlocked read of the owning team that precedes scanning.
func (f *fixture) expectPreCheck() {
	f.mock.ExpectQuery(selectTeamSQL).WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"team_id"}).AddRow(7))
}

// expectReplaceTx scripts a replacement transaction up to the commit. `old` are the
// versions the trim step finds beyond the retention window.
func (f *fixture) expectReplaceTx(revision int64, currentNo int, old ...trimmedVersion) {
	f.mock.ExpectBegin()
	f.mock.ExpectQuery(selectTeamSQL).WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"team_id"}).AddRow(7))
	f.mock.ExpectQuery(lockIdeaSQL).WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"team_id", "title", "summary", "current_version_no", "version"}).
			AddRow(7, "Solar Roofs", "Panels.", currentNo, revision))
	f.mock.ExpectExec(insertVersionSQL).WillReturnResult(sqlmock.NewResult(9, 1))
	f.mock.ExpectExec(advanceIdeaSQL).WillReturnResult(sqlmock.NewResult(0, 1))
	rows := sqlmock.NewRows([]string{"version_no", "object_key"})
	for _, v := range old {
		rows.AddRow(v.No, v.ObjectKey)
	}
	f.mock.ExpectQuery(selectOldSQL).WillReturnRows(rows)
	if len(old) > 0 {
		f.mock.ExpectExec(deleteOldSQL).WillReturnResult(sqlmock.NewResult(0, int64(len(old))))
	}
}

func TestIDEA_020_024_ReplacementAddsTheNextVersionAndItBecomesCurrent(t *testing.T) {
	f := newFixture(t, leader)
	f.expectPreCheck()
	f.expectReplaceTx(replaceRevision, replaceCurrentNo)
	f.mock.ExpectCommit()

	got, err := f.svc.ReplaceFile(context.Background(), replaceInput(), "10.0.0.9")

	require.NoError(t, err)
	assert.Equal(t, 5, got.CurrentVersion.No, "latest version is the official entry")
	assert.Equal(t, "deck-v5.pdf", got.CurrentVersion.OriginalFileName)
	assert.Equal(t, "100001", got.CurrentVersion.UploadedBy)
	assert.Equal(t, replaceRevision+1, got.Revision)
	assert.Equal(t, "Solar Roofs", got.Title)
	assert.Equal(t, 1, f.store.count())
	require.NoError(t, f.mock.ExpectationsWereMet())

	require.Len(t, f.rec.inTx, 1)
	e := f.rec.inTx[0]
	assert.Equal(t, "idea.replace", e.Action)
	assert.Equal(t, audit.ResultSuccess, e.Result)
	assert.Equal(t, "42", e.TargetID)
	assert.Equal(t, 4, e.Changes.(map[string]any)["before"].(map[string]any)["versionNo"])
	assert.NotContains(t, strings.ToLower(stringify(e.Changes)), "%pdf")
}

func TestIDEA_021_EleventhVersionRemovesTheOldestRowAndObject(t *testing.T) {
	f := newFixture(t, leader)
	require.NoError(t, f.store.Put(context.Background(), "oldkey", []byte("x"), ""))
	f.expectPreCheck()
	// Latest is 10; the new one is 11, so version 1 falls out of the window of ten.
	f.mock.ExpectBegin()
	f.mock.ExpectQuery(selectTeamSQL).WillReturnRows(sqlmock.NewRows([]string{"team_id"}).AddRow(7))
	f.mock.ExpectQuery(lockIdeaSQL).WillReturnRows(sqlmock.NewRows(
		[]string{"team_id", "title", "summary", "current_version_no", "version"}).AddRow(7, "T", "", 10, 12))
	f.mock.ExpectExec(insertVersionSQL).WillReturnResult(sqlmock.NewResult(9, 1))
	f.mock.ExpectExec(advanceIdeaSQL).WithArgs(11, int64(42), int64(12)).WillReturnResult(sqlmock.NewResult(0, 1))
	f.mock.ExpectQuery(selectOldSQL).WithArgs(int64(42), 1).
		WillReturnRows(sqlmock.NewRows([]string{"version_no", "object_key"}).AddRow(1, "oldkey"))
	f.mock.ExpectExec(deleteOldSQL).WithArgs(int64(42), 1).WillReturnResult(sqlmock.NewResult(0, 1))
	f.mock.ExpectCommit()
	in := replaceInput()
	in.ExpectedRevision = 12

	got, err := f.svc.ReplaceFile(context.Background(), in, "10.0.0.9")

	require.NoError(t, err)
	assert.Equal(t, 11, got.CurrentVersion.No)
	assert.NotContains(t, f.store.objects, "oldkey", "trimmed object must leave the store")
	assert.Equal(t, 1, f.store.count(), "only the new object remains")
	assert.Equal(t, []int{1}, f.rec.inTx[0].Changes.(map[string]any)["after"].(map[string]any)["removedVersions"])
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestIDEA_021_TenthVersionTrimsNothing(t *testing.T) {
	f := newFixture(t, leader)
	f.expectPreCheck()
	f.mock.ExpectBegin()
	f.mock.ExpectQuery(selectTeamSQL).WillReturnRows(sqlmock.NewRows([]string{"team_id"}).AddRow(7))
	f.mock.ExpectQuery(lockIdeaSQL).WillReturnRows(sqlmock.NewRows(
		[]string{"team_id", "title", "summary", "current_version_no", "version"}).AddRow(7, "T", "", 9, 3))
	f.mock.ExpectExec(insertVersionSQL).WillReturnResult(sqlmock.NewResult(9, 1))
	f.mock.ExpectExec(advanceIdeaSQL).WillReturnResult(sqlmock.NewResult(0, 1))
	f.mock.ExpectQuery(selectOldSQL).WithArgs(int64(42), 0).
		WillReturnRows(sqlmock.NewRows([]string{"version_no", "object_key"}))
	f.mock.ExpectCommit()

	got, err := f.svc.ReplaceFile(context.Background(), replaceInput(), "10.0.0.9")

	require.NoError(t, err)
	assert.Equal(t, 10, got.CurrentVersion.No)
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestIDEA_021_VersionNumbersAreNeverReusedAfterTrimming(t *testing.T) {
	// The next number comes from current_version_no, not from a count of the rows kept.
	f := newFixture(t, leader)
	f.expectPreCheck()
	f.expectReplaceTx(replaceRevision, 25, trimmedVersion{No: 15, ObjectKey: "k15"})
	f.mock.ExpectCommit()

	got, err := f.svc.ReplaceFile(context.Background(), replaceInput(), "10.0.0.9")

	require.NoError(t, err)
	assert.Equal(t, 26, got.CurrentVersion.No)
}

func TestIDEA_021_FailedObjectRemovalDoesNotFailTheReplacement(t *testing.T) {
	f := newFixture(t, leader)
	f.expectPreCheck()
	f.expectReplaceTx(replaceRevision, 10, trimmedVersion{No: 1, ObjectKey: "k1"})
	f.mock.ExpectCommit()
	f.svc.remove = failingRemover{}

	got, err := f.svc.ReplaceFile(context.Background(), replaceInput(), "10.0.0.9")

	require.NoError(t, err, "the rows are already gone; the orphan is only logged")
	assert.Equal(t, 11, got.CurrentVersion.No)
}

type failingRemover struct{}

func (failingRemover) Delete(context.Context, string) error { return errors.New("minio down") }

func TestIDEA_030_StaleRevisionIsRejectedAndLeavesNothing(t *testing.T) {
	f := newFixture(t, leader)
	f.expectPreCheck()
	f.mock.ExpectBegin()
	f.mock.ExpectQuery(selectTeamSQL).WillReturnRows(sqlmock.NewRows([]string{"team_id"}).AddRow(7))
	f.mock.ExpectQuery(lockIdeaSQL).WillReturnRows(sqlmock.NewRows(
		[]string{"team_id", "title", "summary", "current_version_no", "version"}).AddRow(7, "T", "", 4, 5))
	f.mock.ExpectRollback()

	_, err := f.svc.ReplaceFile(context.Background(), replaceInput(), "10.0.0.9") // expects revision 3

	p := problemOf(t, err)
	assert.Equal(t, http.StatusPreconditionFailed, p.Status)
	assert.Equal(t, CodeIdeaChanged, p.Code)
	f.assertNothingVisible(t)
	require.Len(t, f.rec.standalone, 1)
	assert.Equal(t, "idea.replace", f.rec.standalone[0].Action)
	assert.Equal(t, "42", f.rec.standalone[0].TargetID)
}

func TestIDEA_023_ReplacementNeverDeletesAVersionBeyondTheWindow(t *testing.T) {
	// The only delete the service issues is the retention trim, bounded by the cutoff
	// latest-10. No public operation names a version to remove.
	f := newFixture(t, leader)
	f.expectPreCheck()
	f.expectReplaceTx(replaceRevision, replaceCurrentNo)
	f.mock.ExpectCommit()
	_, err := f.svc.ReplaceFile(context.Background(), replaceInput(), "10.0.0.9")
	require.NoError(t, err)
	require.NoError(t, f.mock.ExpectationsWereMet(), "no DELETE was expected for a young Idea")
}

func TestIDEA_011_ReplaceByTeamMemberIsForbidden(t *testing.T) {
	f := newFixture(t, member, asMember)
	f.expectPreCheck()

	_, err := f.svc.ReplaceFile(context.Background(), replaceInput(), "10.0.0.9")

	assert.Equal(t, http.StatusForbidden, problemOf(t, err).Status)
	f.assertNothingVisible(t)
}

func TestIDEA_011_ReplaceByOutsiderOrOnDissolvedTeamIsIdeaNotFound(t *testing.T) {
	for name, m := range map[string]team.Membership{"outsider": outsider, "dissolved": noTeam} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, member, m)
			f.expectPreCheck()
			_, err := f.svc.ReplaceFile(context.Background(), replaceInput(), "10.0.0.9")
			p := problemOf(t, err)
			assert.Equal(t, http.StatusNotFound, p.Status)
			assert.Equal(t, CodeIdeaNotFound, p.Code)
			f.assertNothingVisible(t)
		})
	}
}

func TestIDEA_011_AdminMayReplaceAnyTeamsFile(t *testing.T) {
	f := newFixture(t, admin, outsider)
	f.expectPreCheck()
	f.expectReplaceTx(replaceRevision, replaceCurrentNo)
	f.mock.ExpectCommit()

	_, err := f.svc.ReplaceFile(context.Background(), replaceInput(), "10.0.0.9")

	require.NoError(t, err)
	assert.Equal(t, []bool{true, true, true}, f.event.admins)
}

func TestIDEA_040_UnknownOrDeletedIdeaIsNotFound(t *testing.T) {
	f := newFixture(t, leader)
	f.mock.ExpectQuery(selectTeamSQL).WillReturnRows(sqlmock.NewRows([]string{"team_id"}))

	_, err := f.svc.ReplaceFile(context.Background(), replaceInput(), "10.0.0.9")

	p := problemOf(t, err)
	assert.Equal(t, CodeIdeaNotFound, p.Code)
	f.assertNothingVisible(t)
	require.Len(t, f.rec.standalone, 1)
	assert.Equal(t, audit.ResultFailure, f.rec.standalone[0].Result)
}

func TestDDL_003_ReplaceAfterTheDeadlineIsRejectedBeforeScanning(t *testing.T) {
	f := newFixture(t, leader)
	f.event.errs = []error{event.ErrDeadlinePassed()}
	f.scanFn = fakeScanner{err: errors.New("scanner must not run")}
	f.expectPreCheck()

	_, err := f.svc.ReplaceFile(context.Background(), replaceInput(), "10.0.0.9")

	assert.Equal(t, event.CodeDeadlinePassed, problemOf(t, err).Code)
	f.assertNothingVisible(t)
}

func TestDDL_005_ReplaceDeadlineThatPassesBeforeTheCommitRollsBack(t *testing.T) {
	f := newFixture(t, leader)
	f.event.errs = []error{nil, nil, event.ErrDeadlinePassed()} // pre-check, before store, in tx
	f.expectPreCheck()
	f.mock.ExpectBegin()
	f.mock.ExpectQuery(selectTeamSQL).WillReturnRows(sqlmock.NewRows([]string{"team_id"}).AddRow(7))
	f.mock.ExpectRollback()

	_, err := f.svc.ReplaceFile(context.Background(), replaceInput(), "10.0.0.9")

	assert.Equal(t, event.CodeDeadlinePassed, problemOf(t, err).Code)
	f.assertNothingVisible(t)
}

func TestIDEA_006_RejectedFileLeavesNoVersionAndNoObject(t *testing.T) {
	f := newFixture(t, leader)
	f.expectPreCheck()
	in := replaceInput()
	in.FileName, in.ContentType = "deck.ppt", "application/vnd.ms-powerpoint"

	_, err := f.svc.ReplaceFile(context.Background(), in, "10.0.0.9")

	assert.Equal(t, http.StatusUnsupportedMediaType, problemOf(t, err).Status)
	f.assertNothingVisible(t)
}

func TestIDEA_006_DatabaseFailureAfterStoringRemovesTheNewObject(t *testing.T) {
	f := newFixture(t, leader)
	f.expectPreCheck()
	f.mock.ExpectBegin()
	f.mock.ExpectQuery(selectTeamSQL).WillReturnError(errors.New("db gone"))
	f.mock.ExpectRollback()

	_, err := f.svc.ReplaceFile(context.Background(), replaceInput(), "10.0.0.9")

	require.Error(t, err)
	f.assertNothingVisible(t)
}

func TestIDEA_005_InfectedReplacementIsRejectedAndNeverStored(t *testing.T) {
	f := newFixture(t, leader)
	f.scanFn = fakeScanner{err: platform.NewProblem(http.StatusUnprocessableEntity, upload.CodeMalware, "rejected")}
	f.expectPreCheck()

	_, err := f.svc.ReplaceFile(context.Background(), replaceInput(), "10.0.0.9")

	assert.Equal(t, upload.CodeMalware, problemOf(t, err).Code)
	f.assertNothingVisible(t)
}

func TestIDEA_020_MissingFileAndBadNameAreFieldErrors(t *testing.T) {
	f := newFixture(t, leader)
	f.expectPreCheck()
	in := replaceInput()
	in.Data = nil
	_, err := f.svc.ReplaceFile(context.Background(), in, "10.0.0.9")
	p := problemOf(t, err)
	assert.Equal(t, CodeValidationFailed, p.Code)

	f2 := newFixture(t, leader)
	f2.expectPreCheck()
	in.Data, in.FileName = pdfBytes, ".."
	_, err = f2.svc.ReplaceFile(context.Background(), in, "10.0.0.9")
	assert.Equal(t, CodeValidationFailed, problemOf(t, err).Code)
}

// --- handler -----------------------------------------------------------

func serveReplace(t *testing.T, f *fixture, ifMatch *string, parts ...formPart) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewHandler(f.svc, quiet())
	r.PUT("/ideas/:id/file", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		require.NoError(t, err)
		h.ReplaceIdeaFile(c, id, api.ReplaceIdeaFileParams{IfMatch: ifMatch})
	})
	body, ct := buildForm(t, parts...)
	req := httptest.NewRequest(http.MethodPut, "/ideas/42/file", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func ptr(s string) *string { return &s }

func TestIDEA_024_HandlerReturns200WithTheNewVersionAndETag(t *testing.T) {
	f := newFixture(t, leader)
	f.expectPreCheck()
	f.expectReplaceTx(replaceRevision, replaceCurrentNo)
	f.mock.ExpectCommit()

	w := serveReplace(t, f, ptr(`"3"`), filePart("deck-v5.pdf", "application/pdf", pdfBytes))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, `"4"`, w.Header().Get("ETag"))
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	got := decodeIdea(t, w)
	assert.Equal(t, float64(5), got["currentVersion"].(map[string]any)["versionNo"])
}

func TestIDEA_030_HandlerRequiresIfMatch(t *testing.T) {
	f := newFixture(t, leader)

	w := serveReplace(t, f, nil, filePart("a.pdf", "application/pdf", pdfBytes))

	require.Equal(t, http.StatusPreconditionRequired, w.Code)
	assert.Equal(t, CodePreconditionReq, decodeProblem(t, w).Code)
	f.assertNothingVisible(t)
}

func TestIDEA_030_HandlerRejectsAMalformedIfMatch(t *testing.T) {
	for _, v := range []string{`"abc"`, `*`, `"0"`, `"-2"`} {
		f := newFixture(t, leader)
		w := serveReplace(t, f, ptr(v), filePart("a.pdf", "application/pdf", pdfBytes))
		assert.Equal(t, http.StatusBadRequest, w.Code, v)
	}
}

func TestIDEA_030_HandlerMapsAStaleETagTo412(t *testing.T) {
	f := newFixture(t, leader)
	f.expectPreCheck()
	f.mock.ExpectBegin()
	f.mock.ExpectQuery(selectTeamSQL).WillReturnRows(sqlmock.NewRows([]string{"team_id"}).AddRow(7))
	f.mock.ExpectQuery(lockIdeaSQL).WillReturnRows(sqlmock.NewRows(
		[]string{"team_id", "title", "summary", "current_version_no", "version"}).AddRow(7, "T", "", 4, 9))
	f.mock.ExpectRollback()

	w := serveReplace(t, f, ptr(`W/"3"`), filePart("a.pdf", "application/pdf", pdfBytes))

	require.Equal(t, http.StatusPreconditionFailed, w.Code)
	assert.Equal(t, CodeIdeaChanged, decodeProblem(t, w).Code)
}

func TestIDEA_006_HandlerMissingFilePartCreatesNothing(t *testing.T) {
	f := newFixture(t, leader)
	f.expectPreCheck()

	w := serveReplace(t, f, ptr(`"3"`), field("title", "x"))

	require.Equal(t, http.StatusBadRequest, w.Code)
	f.assertNothingVisible(t)
}
