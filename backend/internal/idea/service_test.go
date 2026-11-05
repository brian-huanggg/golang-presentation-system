package idea

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/event"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/team"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/upload"
)

// --- fakes -------------------------------------------------------------

type fakeIdentity struct {
	actor Actor
	err   error
}

func (f fakeIdentity) CurrentUser(context.Context) (Actor, error) { return f.actor, f.err }

// fakeTeams answers MembershipOf from a script: the n-th call returns scripted[n]; once
// the script runs out the last answer repeats. Two calls happen per upload: the cheap
// pre-check and the one inside the transaction.
type fakeTeams struct {
	scripted []team.Membership
	err      error
	calls    int
}

func (f *fakeTeams) MembershipOf(context.Context, sqlx.QueryerContext, int64, string) (team.Membership, error) {
	defer func() { f.calls++ }()
	if f.err != nil {
		return team.Membership{}, f.err
	}
	i := min(f.calls, len(f.scripted)-1)
	return f.scripted[i], nil
}

// fakeEvent answers the deadline check from a script, like the one in package team: the
// n-th call returns errs[n] (nil once the script runs out) and the isAdmin flag it was
// called with is kept, so a test can fail exactly the check it cares about.
type fakeEvent struct {
	errs   []error
	admins []bool
}

func (f *fakeEvent) CheckActor(isAdmin bool) error {
	n := len(f.admins)
	f.admins = append(f.admins, isAdmin)
	if n < len(f.errs) {
		return f.errs[n]
	}
	return nil
}

type recorder struct {
	inTx       []audit.Entry
	standalone []audit.Entry
}

func (r *recorder) Record(_ context.Context, _ sqlx.ExecerContext, e audit.Entry) error {
	r.inTx = append(r.inTx, e)
	return nil
}
func (r *recorder) RecordStandalone(_ context.Context, e audit.Entry) error {
	r.standalone = append(r.standalone, e)
	return nil
}

// memStore is an in-memory upload.Store, so a test can see exactly which objects exist.
type memStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	putErr  error
}

func (m *memStore) Put(_ context.Context, key string, data []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.putErr != nil {
		return m.putErr
	}
	if m.objects == nil {
		m.objects = map[string][]byte{}
	}
	m.objects[key] = bytes.Clone(data)
	return nil
}

func (m *memStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func (m *memStore) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.objects)
}

type fakeScanner struct{ err error }

func (f fakeScanner) Scan(context.Context, []byte) error { return f.err }

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

var (
	leader = Actor{EmployeeID: "100001", Role: "PARTICIPANT"}
	member = Actor{EmployeeID: "100002", Role: "PARTICIPANT"}
	admin  = Actor{EmployeeID: "000001", Role: "ADMIN"}

	asLeader = team.Membership{Exists: true, Role: team.RoleLeader}
	asMember = team.Membership{Exists: true, Role: team.RoleMember}
	outsider = team.Membership{Exists: true}
	noTeam   = team.Membership{}
)

const (
	insertIdeaSQL    = "INSERT INTO idea \\(team_id"
	insertVersionSQL = "INSERT INTO idea_version"
)

var pdfBytes = []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")

type fixture struct {
	svc    *Service
	mock   sqlmock.Sqlmock
	rec    *recorder
	event  *fakeEvent
	teams  *fakeTeams
	store  *memStore
	scanFn fakeScanner // read when the pipeline runs, so a test may set it after setup
}

func newFixture(t *testing.T, actor Actor, memberships ...team.Membership) *fixture {
	t.Helper()
	raw, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	f := &fixture{
		mock: mock, rec: &recorder{}, event: &fakeEvent{},
		teams: &fakeTeams{scripted: memberships}, store: &memStore{},
	}
	if len(memberships) == 0 {
		f.teams.scripted = []team.Membership{asLeader}
	}
	f.svc = NewService(sqlx.NewDb(raw, "mysql"), fakeIdentity{actor: actor}, f.teams, f.event,
		&upload.Pipeline{Scanner: scannerFunc(func(ctx context.Context, d []byte) error { return f.scanFn.Scan(ctx, d) }), Store: f.store, Log: quiet()},
		f.rec, platform.NewManualClock(time.Date(2026, 11, 5, 4, 0, 0, 0, time.UTC)), quiet())
	return f
}

type scannerFunc func(context.Context, []byte) error

func (s scannerFunc) Scan(ctx context.Context, d []byte) error { return s(ctx, d) }

func validInput() UploadInput {
	return UploadInput{
		TeamID: 7, Title: "Solar Roofs", Summary: "Panels on every roof.",
		FileName: "deck.pdf", ContentType: "application/pdf", Data: pdfBytes,
	}
}

// expectCreate scripts the two inserts of a successful transaction.
func (f *fixture) expectCreate() {
	f.mock.ExpectBegin()
	f.mock.ExpectExec(insertIdeaSQL).WillReturnResult(sqlmock.NewResult(42, 1))
	f.mock.ExpectExec(insertVersionSQL).WillReturnResult(sqlmock.NewResult(1, 1))
}

func problemOf(t *testing.T, err error) *platform.Problem {
	t.Helper()
	var p *platform.Problem
	require.ErrorAs(t, err, &p, "want a Problem, got %v", err)
	return p
}

// assertNothingVisible is IDEA-006: no object in the store and no database change.
func (f *fixture) assertNothingVisible(t *testing.T) {
	t.Helper()
	assert.Zero(t, f.store.count(), "an object was left in the store")
	assert.Empty(t, f.rec.inTx, "an audit record was written inside a transaction")
	require.NoError(t, f.mock.ExpectationsWereMet())
}

// --- tests -------------------------------------------------------------

func TestIDEA_001_LeaderCreatesTheIdeaWithVersionOne(t *testing.T) {
	f := newFixture(t, leader)
	f.expectCreate()
	f.mock.ExpectCommit()

	got, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")
	require.NoError(t, err)

	assert.Equal(t, int64(42), got.ID)
	assert.Equal(t, int64(7), got.TeamID)
	assert.Equal(t, "Solar Roofs", got.Title)
	assert.Equal(t, int64(1), got.Revision)
	assert.Equal(t, 1, got.CurrentVersion.No)
	assert.Equal(t, "100001", got.CurrentVersion.UploadedBy)
	assert.Equal(t, "deck.pdf", got.CurrentVersion.OriginalFileName)
	assert.Equal(t, int64(len(pdfBytes)), got.CurrentVersion.SizeBytes)
	assert.Equal(t, 1, f.store.count())
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestIDEA_020_VersionRowRecordsUploaderNameSizeAndHash(t *testing.T) {
	f := newFixture(t, leader)
	f.mock.ExpectBegin()
	f.mock.ExpectExec(insertIdeaSQL).WithArgs(int64(7), "Solar Roofs", "Panels on every roof.", 1).
		WillReturnResult(sqlmock.NewResult(42, 1))
	f.mock.ExpectExec(insertVersionSQL).
		WithArgs(int64(42), 1, sqlmock.AnyArg(), "pdf", "deck.pdf", int64(len(pdfBytes)),
			sqlmock.AnyArg(), time.Date(2026, 11, 5, 4, 0, 0, 0, time.UTC), "100001").
		WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectCommit()

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")
	require.NoError(t, err)
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestAUD_001_SuccessIsAuditedInTheTransactionWithoutFileContent(t *testing.T) {
	f := newFixture(t, leader)
	f.expectCreate()
	f.mock.ExpectCommit()

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")
	require.NoError(t, err)

	require.Len(t, f.rec.inTx, 1)
	e := f.rec.inTx[0]
	assert.Equal(t, "idea.upload", e.Action)
	assert.Equal(t, audit.ResultSuccess, e.Result)
	assert.Equal(t, "100001", e.ActorEmployeeID)
	assert.Equal(t, "PARTICIPANT", e.ActorRole)
	assert.Equal(t, "idea", e.TargetType)
	assert.Equal(t, "42", e.TargetID)
	assert.Equal(t, "10.0.0.9", e.SourceIP)
	after := e.Changes.(map[string]any)["after"].(map[string]any)
	assert.Equal(t, "Solar Roofs", after["title"])
	assert.Equal(t, 1, after["versionNo"])
	assert.Equal(t, "pdf", after["fileKind"])
	assert.Len(t, after["sha256"], 64)
	assert.Empty(t, f.rec.standalone)
	// AUD-006: the content itself is never in the record.
	assert.NotContains(t, strings.ToLower(stringify(e.Changes)), "%pdf")
}

func TestIDEA_011_AdminMayUploadForAnyTeamAndIsExemptFromTheDeadlineCheck(t *testing.T) {
	f := newFixture(t, admin, outsider) // an Admin is on no roster
	f.expectCreate()
	f.mock.ExpectCommit()

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")

	require.NoError(t, err)
	// DDL-006 is applied by event.Policy; the service must say who is asking each time.
	assert.Equal(t, []bool{true, true, true}, f.event.admins, "pre-check, before store, in transaction")
}

func TestDDL_003_ParticipantIsHeldToTheDeadlineAtEveryCheck(t *testing.T) {
	f := newFixture(t, leader)
	f.expectCreate()
	f.mock.ExpectCommit()

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")

	require.NoError(t, err)
	assert.Equal(t, []bool{false, false, false}, f.event.admins)
}

func TestIDEA_011_TeamMemberCannotUpload(t *testing.T) {
	f := newFixture(t, member, asMember)

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")

	p := problemOf(t, err)
	assert.Equal(t, http.StatusForbidden, p.Status)
	assert.Equal(t, CodeForbidden, p.Code)
	f.assertNothingVisible(t)
	require.Len(t, f.rec.standalone, 1)
	assert.Equal(t, audit.ResultFailure, f.rec.standalone[0].Result)
	assert.Equal(t, CodeForbidden, f.rec.standalone[0].Changes.(map[string]any)["reason"])
}

func TestIDEA_011_OutsiderAndUnknownTeamAreNotFound(t *testing.T) {
	for name, m := range map[string]team.Membership{"outsider": outsider, "no such team": noTeam} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, member, m)
			_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")
			p := problemOf(t, err)
			assert.Equal(t, http.StatusNotFound, p.Status)
			assert.Equal(t, CodeTeamNotFound, p.Code)
			f.assertNothingVisible(t)
		})
	}
}

func TestIDEA_001_SecondIdeaForTheTeamIsRejectedAndLeavesNoObject(t *testing.T) {
	f := newFixture(t, leader)
	f.mock.ExpectBegin()
	f.mock.ExpectExec(insertIdeaSQL).WillReturnError(&mysql.MySQLError{
		Number: 1062, Message: "Duplicate entry '7' for key 'uq_idea_active_team'"})
	f.mock.ExpectRollback()

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")

	p := problemOf(t, err)
	assert.Equal(t, http.StatusConflict, p.Status)
	assert.Equal(t, CodeIdeaExists, p.Code)
	f.assertNothingVisible(t)
	assert.Len(t, f.rec.standalone, 1)
}

func TestIDEA_010_AllFieldProblemsAreReportedTogether(t *testing.T) {
	f := newFixture(t, leader)
	in := validInput()
	in.Title = "   "
	in.Summary = strings.Repeat("あ", maxSummaryRunes+1)
	in.FileName = ""

	_, err := f.svc.UploadFirst(context.Background(), in, "10.0.0.9")

	p := problemOf(t, err)
	assert.Equal(t, http.StatusBadRequest, p.Status)
	assert.Equal(t, CodeValidationFailed, p.Code)
	assert.ElementsMatch(t, []platform.FieldError{
		{Field: "title", Code: "required"},
		{Field: "summary", Code: "too_long"},
		{Field: "file", Code: "invalid_file_name"},
	}, p.Errors)
	f.assertNothingVisible(t)
}

func TestIDEA_010_TitleLimitIsCountedInCharactersNotBytes(t *testing.T) {
	f := newFixture(t, leader)
	f.expectCreate()
	f.mock.ExpectCommit()
	in := validInput()
	in.Title = strings.Repeat("永", maxTitleRunes) // 300 bytes, 100 characters
	in.Summary = ""                               // optional

	got, err := f.svc.UploadFirst(context.Background(), in, "10.0.0.9")
	require.NoError(t, err)
	assert.Equal(t, in.Title, got.Title)

	f2 := newFixture(t, leader)
	in.Title += "永"
	_, err = f2.svc.UploadFirst(context.Background(), in, "10.0.0.9")
	assert.Equal(t, []platform.FieldError{{Field: "title", Code: "too_long"}}, problemOf(t, err).Errors)
}

func TestIDEA_010_MissingFilePartIsAFieldError(t *testing.T) {
	f := newFixture(t, leader)
	in := validInput()
	in.Data = nil

	_, err := f.svc.UploadFirst(context.Background(), in, "10.0.0.9")

	assert.Equal(t, []platform.FieldError{{Field: "file", Code: "required"}}, problemOf(t, err).Errors)
	f.assertNothingVisible(t)
}

func TestIDEA_002_003_004_RejectedFilesLeaveNothingBehind(t *testing.T) {
	tooBig := append([]byte("%PDF-1.7\n"), make([]byte, upload.MaxFileSize)...)
	cases := []struct {
		name   string
		mutate func(*UploadInput)
		status int
		code   string
	}{
		{"IDEA-002 wrong format", func(in *UploadInput) { in.FileName = "deck.exe" }, 415, upload.CodeFormatRejected},
		{"IDEA-002 macro-enabled name", func(in *UploadInput) { in.FileName = "deck.pptm" }, 415, upload.CodeFormatRejected},
		{"IDEA-003 over 5 MB", func(in *UploadInput) { in.Data = tooBig }, 413, upload.CodeFileTooLarge},
		{"IDEA-003 empty", func(in *UploadInput) { in.Data = []byte{} }, 422, upload.CodeFileEmpty},
		{"IDEA-004 declared type differs", func(in *UploadInput) { in.ContentType = "text/plain" }, 415, upload.CodeTypeMismatch},
		{"IDEA-004 content is not a PDF", func(in *UploadInput) { in.Data = []byte("MZ not a pdf at all") }, 422, upload.CodeContentMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, leader)
			in := validInput()
			c.mutate(&in)

			_, err := f.svc.UploadFirst(context.Background(), in, "10.0.0.9")

			p := problemOf(t, err)
			assert.Equal(t, c.status, p.Status)
			assert.Equal(t, c.code, p.Code)
			f.assertNothingVisible(t)
			require.Len(t, f.rec.standalone, 1, "the refusal is audited")
			assert.Equal(t, c.code, f.rec.standalone[0].Changes.(map[string]any)["reason"])
		})
	}
}

func TestIDEA_005_InfectedFileIsRejectedAndNeverStored(t *testing.T) {
	f := newFixture(t, leader)
	f.scanFn = fakeScanner{err: platform.NewProblem(http.StatusUnprocessableEntity, upload.CodeMalware, "rejected")}

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")

	assert.Equal(t, upload.CodeMalware, problemOf(t, err).Code)
	f.assertNothingVisible(t)
	assert.Len(t, f.rec.standalone, 1)
}

func TestIDEA_005_ScannerOutageFailsClosed(t *testing.T) {
	f := newFixture(t, leader)
	f.scanFn = fakeScanner{err: platform.NewProblem(http.StatusServiceUnavailable, upload.CodeScanUnavailable, "later")}

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")

	p := problemOf(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, p.Status)
	f.assertNothingVisible(t)
}

func TestDDL_003_UploadAfterTheDeadlineIsRejectedBeforeScanning(t *testing.T) {
	f := newFixture(t, leader)
	f.event.errs = []error{event.ErrDeadlinePassed()}
	scanned := false
	f.scanFn = fakeScanner{}
	f.svc.files = &upload.Pipeline{Scanner: scannerFunc(func(context.Context, []byte) error { scanned = true; return nil }), Store: f.store}

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")

	assert.Equal(t, event.CodeDeadlinePassed, problemOf(t, err).Code)
	assert.False(t, scanned, "an upload that cannot succeed must not cost a scan")
	f.assertNothingVisible(t)
}

func TestDDL_005_DeadlineThatPassesDuringTheScanStopsTheUploadBeforeStoring(t *testing.T) {
	f := newFixture(t, leader)
	f.event.errs = []error{nil, event.ErrDeadlinePassed()} // pre-check ok, re-check after scan fails

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")

	assert.Equal(t, event.CodeDeadlinePassed, problemOf(t, err).Code)
	f.assertNothingVisible(t)
	assert.Len(t, f.event.admins, 2, "the transaction was never reached")
}

func TestDDL_005_DeadlineThatPassesBeforeTheCommitRollsBackAndRemovesTheObject(t *testing.T) {
	f := newFixture(t, leader)
	f.event.errs = []error{nil, nil, event.ErrDeadlinePassed()} // fails inside the transaction
	f.mock.ExpectBegin()
	f.mock.ExpectRollback()

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")

	assert.Equal(t, event.CodeDeadlinePassed, problemOf(t, err).Code)
	f.assertNothingVisible(t)
}

func TestIDEA_011_TeamDissolvedWhileScanningLeavesNothingBehind(t *testing.T) {
	// The pre-check sees the team; the locking read inside the transaction does not.
	f := newFixture(t, leader, asLeader, noTeam)
	f.mock.ExpectBegin()
	f.mock.ExpectRollback()

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")

	assert.Equal(t, CodeTeamNotFound, problemOf(t, err).Code)
	f.assertNothingVisible(t)
}

func TestIDEA_006_DatabaseFailureAfterStoringRemovesTheObject(t *testing.T) {
	steps := map[string]func(f *fixture){
		"idea insert": func(f *fixture) {
			f.mock.ExpectBegin()
			f.mock.ExpectExec(insertIdeaSQL).WillReturnError(errors.New("disk full"))
			f.mock.ExpectRollback()
		},
		"version insert": func(f *fixture) {
			f.mock.ExpectBegin()
			f.mock.ExpectExec(insertIdeaSQL).WillReturnResult(sqlmock.NewResult(42, 1))
			f.mock.ExpectExec(insertVersionSQL).WillReturnError(errors.New("disk full"))
			f.mock.ExpectRollback()
		},
		"commit": func(f *fixture) {
			f.expectCreate()
			f.mock.ExpectCommit().WillReturnError(errors.New("connection lost"))
		},
	}
	for name, script := range steps {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, leader)
			script(f)

			_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")

			require.Error(t, err)
			assert.Equal(t, http.StatusInternalServerError, platform.AsProblem(err).Status)
			assert.NotContains(t, platform.AsProblem(err).Detail, "disk full", "internals must not leak")
			assert.Zero(t, f.store.count(), "the stored object must be removed")
			require.NoError(t, f.mock.ExpectationsWereMet())
		})
	}
}

func TestIDEA_006_StoreFailureCreatesNoRows(t *testing.T) {
	f := newFixture(t, leader)
	f.store.putErr = errors.New("minio down")

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")

	require.Error(t, err)
	f.assertNothingVisible(t) // no Begin was expected, so none may have happened
}

func TestIDEA_020_UploaderAccountGoneRollsBack(t *testing.T) {
	f := newFixture(t, leader)
	f.mock.ExpectBegin()
	f.mock.ExpectExec(insertIdeaSQL).WillReturnResult(sqlmock.NewResult(42, 1))
	f.mock.ExpectExec(insertVersionSQL).WillReturnResult(sqlmock.NewResult(0, 0))
	f.mock.ExpectRollback()

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")

	assert.Equal(t, http.StatusUnauthorized, problemOf(t, err).Status)
	assert.Zero(t, f.store.count())
	require.NoError(t, f.mock.ExpectationsWereMet())
}

func TestUploadFirst_UnauthenticatedCallerGetsTheIdentityError(t *testing.T) {
	f := newFixture(t, leader)
	f.svc.ident = fakeIdentity{err: platform.NewProblem(http.StatusUnauthorized, "unauthenticated", "no")}

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")

	assert.Equal(t, http.StatusUnauthorized, problemOf(t, err).Status)
	assert.Empty(t, f.rec.standalone, "no actor, nothing to attribute")
	f.assertNothingVisible(t)
}

func TestAUD_001_FailureAuditNeverContainsTitleOrSummary(t *testing.T) {
	f := newFixture(t, leader)
	in := validInput()
	in.Title = "secret plan"
	in.FileName = "deck.exe"

	_, err := f.svc.UploadFirst(context.Background(), in, "10.0.0.9")
	require.Error(t, err)

	require.Len(t, f.rec.standalone, 1)
	assert.NotContains(t, stringify(f.rec.standalone[0].Changes), "secret plan")
	assert.Equal(t, "idea.upload", f.rec.standalone[0].Action)
	assert.Equal(t, "10.0.0.9", f.rec.standalone[0].SourceIP)
}

func TestIDEA_020_FileNameIsReducedToAPlainName(t *testing.T) {
	cases := []struct{ in, want, code string }{
		{"deck.pdf", "deck.pdf", ""},
		{"../../etc/passwd.pdf", "passwd.pdf", ""},
		{`C:\Users\me\My Deck.pdf`, "My Deck.pdf", ""},
		{"dec\x00k\n.pdf", "deck.pdf", ""},
		{"  spaced.pdf  ", "spaced.pdf", ""},
		{"簡報.pdf", "簡報.pdf", ""},
		{"", "", "invalid_file_name"},
		{"..", "", "invalid_file_name"},
		{"/", "", "invalid_file_name"},
		{"\x01\x02", "", "invalid_file_name"},
		{strings.Repeat("a", 256), "", "too_long"},
		{strings.Repeat("a", 251) + ".pdf", strings.Repeat("a", 251) + ".pdf", ""},
	}
	for _, c := range cases {
		got, code := cleanFileName(c.in)
		assert.Equal(t, c.want, got, "name for %q", c.in)
		assert.Equal(t, c.code, code, "code for %q", c.in)
	}
}

func TestIDEA_011_AuthorizationErrorFromTeamIsNotMaskedAsNotFound(t *testing.T) {
	f := newFixture(t, leader)
	f.teams.err = errors.New("db down")

	_, err := f.svc.UploadFirst(context.Background(), validInput(), "10.0.0.9")

	require.Error(t, err)
	assert.Equal(t, http.StatusInternalServerError, platform.AsProblem(err).Status)
	f.assertNothingVisible(t)
}

// stringify renders audit changes for substring checks.
func stringify(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
