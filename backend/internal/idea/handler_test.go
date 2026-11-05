package idea

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/upload"
)

// formPart is one part of a test multipart body.
type formPart struct {
	name     string
	value    string // text field
	filename string // when set the part is a file
	ctype    string
	data     []byte
}

func field(name, value string) formPart { return formPart{name: name, value: value} }
func filePart(filename, ctype string, data []byte) formPart {
	return formPart{name: "file", filename: filename, ctype: ctype, data: data}
}

func buildForm(t *testing.T, parts ...formPart) (body []byte, contentType string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range parts {
		if p.filename == "" {
			require.NoError(t, w.WriteField(p.name, p.value))
			continue
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="`+p.name+`"; filename="`+p.filename+`"`)
		h.Set("Content-Type", p.ctype)
		pw, err := w.CreatePart(h)
		require.NoError(t, err)
		_, err = pw.Write(p.data)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes(), w.FormDataContentType()
}

func serve(t *testing.T, f *fixture, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewHandler(f.svc, quiet())
	r.POST("/teams/:teamId/idea", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("teamId"), 10, 64)
		require.NoError(t, err)
		h.CreateIdea(c, id)
	})
	req := httptest.NewRequest(http.MethodPost, "/teams/7/idea", body)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func serveForm(t *testing.T, f *fixture, parts ...formPart) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := buildForm(t, parts...)
	return serve(t, f, bytes.NewReader(body), ct)
}

func decodeProblem(t *testing.T, w *httptest.ResponseRecorder) platform.Problem {
	t.Helper()
	var p platform.Problem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p), w.Body.String())
	return p
}

func TestIDEA_001_HandlerReturns201WithTheIdeaAndAnETag(t *testing.T) {
	f := newFixture(t, leader)
	f.expectCreate()
	f.mock.ExpectCommit()

	w := serveForm(t, f,
		field("title", "Solar Roofs"), field("summary", "Panels on every roof."),
		filePart("deck.pdf", "application/pdf", pdfBytes))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Equal(t, `"1"`, w.Header().Get("ETag"))
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	ver := got["currentVersion"].(map[string]any)
	// 04:00 UTC is 12:00 in Asia/Taipei (I18N-004).
	assert.Regexp(t, `^2026-11-05T12:00:00\+08:00$`, ver["uploadedAt"])
	delete(ver, "uploadedAt")
	assert.Equal(t, map[string]any{
		"id": float64(42), "teamId": float64(7), "title": "Solar Roofs", "summary": "Panels on every roof.",
		"currentVersion": map[string]any{
			"versionNo": float64(1), "uploadedBy": "100001",
			"originalFileName": "deck.pdf", "sizeBytes": float64(len(pdfBytes)),
		},
	}, got)
}

func TestIDEA_010_HandlerAcceptsTextFieldsAfterTheFileAndNoSummary(t *testing.T) {
	f := newFixture(t, leader)
	f.expectCreate()
	f.mock.ExpectCommit()

	w := serveForm(t, f, filePart("deck.pdf", "application/pdf", pdfBytes), field("title", "Solar Roofs"))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Equal(t, "", decodeIdea(t, w)["summary"])
}

func decodeIdea(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	return got
}

func TestIDEA_010_HandlerReportsAMissingFilePart(t *testing.T) {
	f := newFixture(t, leader)

	w := serveForm(t, f, field("title", "Solar Roofs"))

	require.Equal(t, http.StatusBadRequest, w.Code)
	p := decodeProblem(t, w)
	assert.Equal(t, CodeValidationFailed, p.Code)
	assert.Equal(t, []platform.FieldError{{Field: "file", Code: "required"}}, p.Errors)
	f.assertNothingVisible(t)
}

func TestIDEA_006_HandlerRejectsABodyThatEndsBeforeItIsComplete(t *testing.T) {
	f := newFixture(t, leader)
	body, ct := buildForm(t, field("title", "Solar Roofs"), filePart("deck.pdf", "application/pdf", pdfBytes))

	// The connection drops in the middle of the file part.
	w := serve(t, f, bytes.NewReader(body[:len(body)-len(pdfBytes)/2-40]), ct)

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Equal(t, CodeInvalidRequest, decodeProblem(t, w).Code)
	f.assertNothingVisible(t)
}

func TestIDEA_006_HandlerRejectsAReadErrorMidBody(t *testing.T) {
	f := newFixture(t, leader)
	body, ct := buildForm(t, field("title", "Solar Roofs"), filePart("deck.pdf", "application/pdf", pdfBytes))
	broken := io.MultiReader(bytes.NewReader(body[:len(body)/2]), errReader{errors.New("connection reset")})

	w := serve(t, f, broken, ct)

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Equal(t, CodeInvalidRequest, decodeProblem(t, w).Code)
	f.assertNothingVisible(t)
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestIDEA_006_HandlerRejectsABodyThatIsNotMultipart(t *testing.T) {
	f := newFixture(t, leader)

	w := serve(t, f, strings.NewReader(`{"title":"x"}`), "application/json")

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, CodeInvalidRequest, decodeProblem(t, w).Code)
	f.assertNothingVisible(t)
}

func TestIDEA_001_HandlerRejectsTwoFileParts(t *testing.T) {
	f := newFixture(t, leader)

	w := serveForm(t, f, field("title", "x"),
		filePart("a.pdf", "application/pdf", pdfBytes), filePart("b.pdf", "application/pdf", pdfBytes))

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, CodeInvalidRequest, decodeProblem(t, w).Code)
	f.assertNothingVisible(t)
}

func TestIDEA_003_HandlerAnswersTooLargeForAFileJustOverTheLimit(t *testing.T) {
	f := newFixture(t, leader)
	big := append([]byte("%PDF-1.7\n"), make([]byte, upload.MaxFileSize)...) // 5 MB + 9 bytes

	w := serveForm(t, f, field("title", "Solar Roofs"), filePart("deck.pdf", "application/pdf", big))

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.Equal(t, upload.CodeFileTooLarge, decodeProblem(t, w).Code)
	f.assertNothingVisible(t)
	assert.Len(t, f.rec.standalone, 1, "AC-IDEA-003: rejected with a size reason, and audited")
}

func TestIDEA_003_HandlerStopsReadingAHugeBody(t *testing.T) {
	f := newFixture(t, leader)
	huge := bytes.Repeat([]byte{'x'}, upload.MaxFileSize+maxFormOverhead+4096)

	w := serveForm(t, f, field("title", "Solar Roofs"), filePart("deck.pdf", "application/pdf", huge))

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.Equal(t, upload.CodeFileTooLarge, decodeProblem(t, w).Code)
	f.assertNothingVisible(t)
}

func TestIDEA_003_HandlerAcceptsAFileOfExactlyTheLimit(t *testing.T) {
	f := newFixture(t, leader)
	f.expectCreate()
	f.mock.ExpectCommit()
	exact := append([]byte("%PDF-1.7\n"), make([]byte, upload.MaxFileSize-9)...)

	w := serveForm(t, f, field("title", "Solar Roofs"), filePart("deck.pdf", "application/pdf", exact))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
}

func TestIDEA_011_HandlerMapsAMemberTo403AndAProblemBody(t *testing.T) {
	f := newFixture(t, member, asMember)

	w := serveForm(t, f, field("title", "x"), filePart("deck.pdf", "application/pdf", pdfBytes))

	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, platform.ProblemContentType, w.Header().Get("Content-Type"))
	p := decodeProblem(t, w)
	assert.Equal(t, CodeForbidden, p.Code)
	assert.Equal(t, "/teams/7/idea", p.Instance)
}

func TestIDEA_001_HandlerHidesInternalErrors(t *testing.T) {
	f := newFixture(t, leader)
	f.mock.ExpectBegin().WillReturnError(errors.New("secret dsn root:pw@tcp"))

	w := serveForm(t, f, field("title", "x"), filePart("deck.pdf", "application/pdf", pdfBytes))

	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "secret")
	assert.Zero(t, f.store.count(), "the object is removed when the transaction cannot start")
	require.NoError(t, f.mock.ExpectationsWereMet())
}
