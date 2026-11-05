package idea

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/upload"
)

// removeTimeout bounds the cleanup of trimmed objects, which must run even when the
// request context is already cancelled.
const removeTimeout = 10 * time.Second

// ReplaceFile adds a new version to the Idea and makes it the official entry (IDEA-020,
// IDEA-024). When that makes more than MaxVersions, the oldest versions are deleted from
// the database in the same transaction and from the object store right after it
// (IDEA-021). Nothing here can delete one chosen version (IDEA-023).
//
// It follows the same order as UploadFirst so a rejected file leaves nothing behind
// (IDEA-006). The caller's If-Match revision is checked under the Idea's row lock, so two
// concurrent replacements cannot both succeed (IDEA-030).
func (s *Service) ReplaceFile(ctx context.Context, in ReplaceInput, sourceIP string) (Idea, error) {
	actor, err := s.ident.CurrentUser(ctx)
	if err != nil {
		return Idea{}, err
	}
	targetID := strconv.FormatInt(in.IdeaID, 10)
	reject := func(teamID int64, err error) (Idea, error) {
		var p *platform.Problem
		if errors.As(err, &p) {
			s.auditFailure(ctx, actor, actionReplace, targetID, teamID, sourceIP, p)
		}
		return Idea{}, err
	}

	// Cheap checks first; the transaction repeats them under locks.
	teamID, err := teamOfIdea(ctx, s.db, in.IdeaID)
	if err != nil {
		return reject(0, ideaProblem(err))
	}
	if err := s.authorizeIdea(ctx, s.db, actor, teamID); err != nil {
		return reject(teamID, err)
	}
	if err := s.event.CheckActor(actor.isAdmin()); err != nil { // DDL-003
		return reject(teamID, err)
	}
	if in.Data == nil {
		return reject(teamID, ErrValidation([]platform.FieldError{{Field: fieldFile, Code: codeRequired}}))
	}
	fileName, nameCode := cleanFileName(in.FileName)
	if nameCode != "" {
		return reject(teamID, ErrValidation([]platform.FieldError{{Field: fieldFile, Code: nameCode}}))
	}

	sum := sha256.Sum256(in.Data)
	var replaced Idea
	var trimmed []trimmedVersion
	_, err = s.files.AcceptWith(ctx, fileName, in.ContentType, in.Data,
		func(context.Context) error { return s.event.CheckActor(actor.isAdmin()) },
		func(ctx context.Context, res upload.Result) error {
			return platform.WithTx(ctx, s.db, func(tx *sqlx.Tx) error {
				var err error
				replaced, trimmed, err = s.replaceInTx(ctx, tx, actor, in, sourceIP, uploaded{
					fileName: fileName, res: res, sha256: hex.EncodeToString(sum[:]),
				})
				return err
			})
		})
	switch {
	case errors.Is(err, errUploaderGone):
		return Idea{}, errUnauthenticated()
	case err != nil:
		return reject(teamID, ideaProblem(err))
	}
	// The rows are gone for good, so the objects are unreachable; removing them is best
	// effort, like the compensating delete (Architecture 8.2, accepted orphan).
	s.removeObjects(ctx, trimmed)
	return replaced, nil
}

// ideaProblem turns the repo's sentinel errors into the problems the caller sees.
func ideaProblem(err error) error {
	switch {
	case errors.Is(err, errIdeaGone):
		return ErrIdeaNotFound()
	case errors.Is(err, errStaleRevision):
		return ErrIdeaChanged()
	}
	return err
}

// authorizeIdea is authorize with the Idea-level wording: an outsider or a dissolved team
// is "Idea not found".
func (s *Service) authorizeIdea(ctx context.Context, q sqlx.QueryerContext, actor Actor, teamID int64) error {
	err := s.authorize(ctx, q, actor, teamID)
	var p *platform.Problem
	if errors.As(err, &p) && p.Code == CodeTeamNotFound {
		return ErrIdeaNotFound()
	}
	return err
}

// replaceInTx is the body of the transaction. Lock order is team, then Idea, as in
// creation and dissolution.
func (s *Service) replaceInTx(ctx context.Context, tx *sqlx.Tx, actor Actor, in ReplaceInput, sourceIP string,
	u uploaded) (Idea, []trimmedVersion, error) {
	teamID, err := teamOfIdea(ctx, tx, in.IdeaID)
	if err != nil {
		return Idea{}, nil, err
	}
	if err := s.authorizeIdea(ctx, tx, actor, teamID); err != nil {
		return Idea{}, nil, err
	}
	// DDL-005: the action completes here, so this is the check that counts.
	if err := s.event.CheckActor(actor.isAdmin()); err != nil {
		return Idea{}, nil, err
	}
	row, err := lockIdea(ctx, tx, in.IdeaID)
	if err != nil {
		return Idea{}, nil, err
	}
	if row.Revision != in.ExpectedRevision {
		return Idea{}, nil, errStaleRevision
	}

	newNo := row.CurrentVersionNo + 1
	now := s.clock.Now()
	if err := insertVersion(ctx, tx, newVersion{
		IdeaID: in.IdeaID, No: newNo, ObjectKey: u.res.Key, Kind: string(u.res.Kind),
		FileName: u.fileName, SizeBytes: u.res.Size, SHA256: u.sha256,
		UploadedBy: actor.EmployeeID, UploadedAt: now,
	}); err != nil {
		return Idea{}, nil, err
	}
	if err := advanceIdea(ctx, tx, in.IdeaID, row.Revision, newNo); err != nil {
		return Idea{}, nil, err
	}
	trimmed, err := trimVersions(ctx, tx, in.IdeaID, newNo-MaxVersions)
	if err != nil {
		return Idea{}, nil, err
	}

	removed := make([]int, len(trimmed))
	for i, v := range trimmed {
		removed[i] = v.No
	}
	// AUD-001, AUD-002, AUD-006: metadata only, never the file's content.
	err = s.audit.Record(ctx, tx, audit.Entry{
		ActorEmployeeID: actor.EmployeeID, ActorRole: actor.Role,
		Action: actionReplace, TargetType: targetIdea, TargetID: strconv.FormatInt(in.IdeaID, 10),
		Result: audit.ResultSuccess, SourceIP: sourceIP,
		Changes: map[string]any{
			"before": map[string]any{"versionNo": row.CurrentVersionNo},
			"after": map[string]any{
				"teamId": teamID, "versionNo": newNo, "fileName": u.fileName, "fileKind": string(u.res.Kind),
				"sizeBytes": u.res.Size, "sha256": u.sha256, "removedVersions": removed,
			},
		},
	})
	if err != nil {
		return Idea{}, nil, err
	}
	return Idea{
		ID: in.IdeaID, TeamID: teamID, Title: row.Title, Summary: row.Summary, Revision: row.Revision + 1,
		CurrentVersion: Version{
			No: newNo, UploadedBy: actor.EmployeeID, UploadedAt: now,
			OriginalFileName: u.fileName, SizeBytes: u.res.Size,
		},
	}, trimmed, nil
}

// removeObjects deletes the files of trimmed versions after their rows are committed away.
func (s *Service) removeObjects(ctx context.Context, trimmed []trimmedVersion) {
	if len(trimmed) == 0 {
		return
	}
	// WithoutCancel: the request may already be over; the rows are gone either way.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), removeTimeout)
	defer cancel()
	for _, v := range trimmed {
		if err := s.remove.Delete(ctx, v.ObjectKey); err != nil {
			s.logger.ErrorContext(ctx, "removing trimmed version object failed; object orphaned",
				"key", v.ObjectKey, "versionNo", v.No, "error", err)
		}
	}
}
