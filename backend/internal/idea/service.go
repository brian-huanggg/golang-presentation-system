package idea

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/team"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/upload"
)

// Identity resolves the logged-in user from the request's session. main.go adapts
// *auth.Service to it, because idea may not import auth (ADR 0003).
type Identity interface {
	CurrentUser(ctx context.Context) (Actor, error)
}

// TeamReader is what idea needs from team: whether a team is active and the caller's role
// in it. *team.Service satisfies it (ADR 0003, rule 2).
type TeamReader interface {
	MembershipOf(ctx context.Context, q sqlx.QueryerContext, teamID int64, employeeID string) (team.Membership, error)
}

// EventPolicy is the part of event.Policy that idea uses: the deadline check, which skips
// Admins (DDL-006).
type EventPolicy interface {
	CheckActor(isAdmin bool) error
}

// Auditor is the part of audit.Recorder that idea uses.
type Auditor interface {
	Record(ctx context.Context, tx sqlx.ExecerContext, e audit.Entry) error
	RecordStandalone(ctx context.Context, e audit.Entry) error
}

// Acceptor is the part of upload.Pipeline that idea uses: validate, scan, store, then run
// the caller's commit; a failing commit removes the stored object again (IDEA-006).
type Acceptor interface {
	AcceptWith(ctx context.Context, filename, declaredType string, data []byte,
		beforeStore func(ctx context.Context) error,
		commit func(ctx context.Context, res upload.Result) error) (upload.Result, error)
}

// ObjectRemover deletes stored files. *upload.MinIO satisfies it; idea needs it to drop the
// files of versions trimmed beyond MaxVersions (IDEA-021).
type ObjectRemover interface {
	Delete(ctx context.Context, key string) error
}

// Service holds the Idea business rules. Construct it once in main.go.
type Service struct {
	db     *sqlx.DB
	ident  Identity
	teams  TeamReader
	event  EventPolicy
	files  Acceptor
	remove ObjectRemover
	audit  Auditor
	clock  platform.Clock
	logger *slog.Logger
}

func NewService(db *sqlx.DB, ident Identity, teams TeamReader, event EventPolicy, files Acceptor, remove ObjectRemover,
	auditor Auditor, clock platform.Clock, log *slog.Logger) *Service {
	return &Service{db: db, ident: ident, teams: teams, event: event, files: files, remove: remove, audit: auditor, clock: clock, logger: log}
}

// UploadFirst creates the team's Idea with its first file as version 1 (IDEA-001, IDEA-010,
// IDEA-011, IDEA-020). Only the Team Leader or an Admin may do it.
//
// The order is what keeps failures invisible (IDEA-006): the caller is authorised and the
// deadline checked before any scanning; the file is validated (IDEA-002..004) and scanned
// (IDEA-005); the deadline is checked again; only then is the object stored. The Idea,
// its version and the audit record are written in one transaction, and if that fails the
// stored object is deleted. A rejected upload therefore leaves no row and no object.
func (s *Service) UploadFirst(ctx context.Context, in UploadInput, sourceIP string) (Idea, error) {
	actor, err := s.ident.CurrentUser(ctx)
	if err != nil {
		return Idea{}, err
	}
	// reject audits the refusal (AUD-001: every Idea action, failures included) and
	// returns the problem unchanged. The write is outside any business transaction.
	reject := func(err error) (Idea, error) {
		var p *platform.Problem
		if errors.As(err, &p) {
			s.auditFailure(ctx, actor, actionUpload, "", in.TeamID, sourceIP, p)
		}
		return Idea{}, err
	}

	// Cheap checks first, so nobody who may not upload can make us scan a file. The
	// transaction repeats the authorisation, which is the check that counts.
	if err := s.authorize(ctx, s.db, actor, in.TeamID); err != nil {
		return reject(err)
	}
	// DDL-003. Checked again before storing and inside the transaction (DDL-005).
	if err := s.event.CheckActor(actor.isAdmin()); err != nil {
		return reject(err)
	}
	title, summary, fileName, fieldErrs := validateUpload(in)
	if len(fieldErrs) > 0 {
		return reject(ErrValidation(fieldErrs))
	}

	sum := sha256.Sum256(in.Data)
	var created Idea
	_, err = s.files.AcceptWith(ctx, fileName, in.ContentType, in.Data,
		func(context.Context) error { return s.event.CheckActor(actor.isAdmin()) },
		func(ctx context.Context, res upload.Result) error {
			return platform.WithTx(ctx, s.db, func(tx *sqlx.Tx) error {
				var err error
				created, err = s.createInTx(ctx, tx, actor, in.TeamID, sourceIP, uploaded{
					title: title, summary: summary, fileName: fileName,
					res: res, sha256: hex.EncodeToString(sum[:]),
				})
				return err
			})
		})
	switch {
	case errors.Is(err, errDuplicateIdea):
		return reject(ErrIdeaExists())
	case errors.Is(err, errUploaderGone):
		// The account vanished after the session check (an engineer deleted it).
		return Idea{}, errUnauthenticated()
	case err != nil:
		return reject(err)
	}
	return created, nil
}

// uploaded bundles what createInTx needs besides the actor and the team.
type uploaded struct {
	title, summary, fileName string
	res                      upload.Result
	sha256                   string
}

// authorize applies IDEA-011 and the permission matrix. An unknown or dissolved team and
// a Participant outside the roster look the same (404), so the existence of other teams
// is not revealed; a Team Member is told they may not change the Idea (403). It uses a
// locking read, so inside a transaction it also holds the team against a concurrent
// dissolution.
func (s *Service) authorize(ctx context.Context, q sqlx.QueryerContext, actor Actor, teamID int64) error {
	m, err := s.teams.MembershipOf(ctx, q, teamID, actor.EmployeeID)
	if err != nil {
		return err
	}
	switch {
	case !m.Exists:
		return ErrTeamNotFound()
	case actor.isAdmin(), m.Role == team.RoleLeader:
		return nil
	case m.Role == team.RoleMember:
		return ErrForbidden()
	default:
		return ErrTeamNotFound()
	}
}

// createInTx is the body of the transaction. Returning an error rolls everything back.
func (s *Service) createInTx(ctx context.Context, tx *sqlx.Tx, actor Actor, teamID int64, sourceIP string, u uploaded) (Idea, error) {
	if err := s.authorize(ctx, tx, actor, teamID); err != nil {
		return Idea{}, err
	}
	// DDL-005: the action completes here, so this is the check that counts.
	if err := s.event.CheckActor(actor.isAdmin()); err != nil {
		return Idea{}, err
	}

	const firstVersion = 1
	ideaID, err := insertIdea(ctx, tx, teamID, u.title, u.summary, firstVersion)
	if err != nil {
		return Idea{}, err
	}
	now := s.clock.Now()
	if err := insertVersion(ctx, tx, newVersion{
		IdeaID: ideaID, No: firstVersion, ObjectKey: u.res.Key, Kind: string(u.res.Kind),
		FileName: u.fileName, SizeBytes: u.res.Size, SHA256: u.sha256,
		UploadedBy: actor.EmployeeID, UploadedAt: now,
	}); err != nil {
		return Idea{}, err
	}

	// AUD-001, AUD-002, AUD-006: committed or rolled back together with the Idea; holds
	// metadata only, never the file's content.
	err = s.audit.Record(ctx, tx, audit.Entry{
		ActorEmployeeID: actor.EmployeeID, ActorRole: actor.Role,
		Action: actionUpload, TargetType: targetIdea, TargetID: strconv.FormatInt(ideaID, 10),
		Result: audit.ResultSuccess, SourceIP: sourceIP,
		Changes: map[string]any{"after": map[string]any{
			"teamId": teamID, "title": u.title, "summary": u.summary, "versionNo": firstVersion,
			"fileName": u.fileName, "fileKind": string(u.res.Kind), "sizeBytes": u.res.Size, "sha256": u.sha256,
		}},
	})
	if err != nil {
		return Idea{}, err
	}
	return Idea{
		ID: ideaID, TeamID: teamID, Title: u.title, Summary: u.summary, Revision: 1,
		CurrentVersion: Version{
			No: firstVersion, UploadedBy: actor.EmployeeID, UploadedAt: now,
			OriginalFileName: u.fileName, SizeBytes: u.res.Size,
		},
	}, nil
}

// auditFailure records a refused upload. It runs after the transaction rolled back, so the
// record is not lost with it. A failure to write it is logged and does not change the
// answer the caller gets.
func (s *Service) auditFailure(ctx context.Context, actor Actor, action, targetID string, teamID int64, sourceIP string, p *platform.Problem) {
	changes := map[string]any{"reason": p.Code}
	if teamID != 0 {
		changes["teamId"] = teamID
	}
	if len(p.Errors) > 0 {
		// Field and code only; the title and summary are arbitrary text and stay out.
		changes["errors"] = p.Errors
	}
	err := s.audit.RecordStandalone(ctx, audit.Entry{
		ActorEmployeeID: actor.EmployeeID, ActorRole: actor.Role,
		Action: action, TargetType: targetIdea, TargetID: targetID,
		Result: audit.ResultFailure, SourceIP: sourceIP, Changes: changes,
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "audit idea failure", "action", action, "error", err)
	}
}

// validateUpload normalizes the text fields and file name and reports every problem at
// once (IDEA-010). The file itself is judged by package upload.
func validateUpload(in UploadInput) (title, summary, fileName string, errs []platform.FieldError) {
	bad := func(field, code string) { errs = append(errs, platform.FieldError{Field: field, Code: code}) }

	title = strings.TrimSpace(in.Title)
	switch {
	case title == "":
		bad(fieldTitle, codeRequired)
	case utf8.RuneCountInString(title) > maxTitleRunes:
		bad(fieldTitle, codeTooLong)
	}
	summary = strings.TrimSpace(in.Summary)
	if utf8.RuneCountInString(summary) > maxSummaryRunes {
		bad(fieldSummary, codeTooLong)
	}
	// A nil Data means the form had no file part at all; an empty file part is the
	// upload package's call (upload_file_empty).
	if in.Data == nil {
		bad(fieldFile, codeRequired)
		return title, summary, "", errs
	}
	fileName, nameCode := cleanFileName(in.FileName)
	if nameCode != "" {
		bad(fieldFile, nameCode)
	}
	return title, summary, fileName, errs
}

// cleanFileName reduces what the client sent to a plain file name: no directories (either
// separator), no control characters. The name is only ever stored and shown, never used as
// a path (object keys are random), but a clean value keeps it safe to display. The second
// result is a problem code, or "" when the name is usable.
func cleanFileName(raw string) (string, string) {
	name := path.Base(strings.ReplaceAll(raw, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || name == "/" {
		return "", codeBadName
	}
	if utf8.RuneCountInString(name) > maxFileNameRunes {
		return "", codeTooLong
	}
	return name, ""
}
