package idea

import (
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/api"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/event"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/upload"
)

const (
	// maxFormOverhead is what the multipart framing and the two text fields may add to a
	// file of upload.MaxFileSize. A body above the sum is refused while reading.
	maxFormOverhead = 64 << 10
	// maxFieldBytes bounds one text field: 1,000 characters of 4 bytes each fit with room.
	// A longer value is read only up to this size and then rejected as too long.
	maxFieldBytes = 8 << 10
)

// Handler implements the Idea operations of the generated api.ServerInterface.
type Handler struct {
	svc *Service
	log *slog.Logger
}

func NewHandler(svc *Service, log *slog.Logger) *Handler { return &Handler{svc: svc, log: log} }

// CreateIdea handles POST /teams/{teamId}/idea.
func (h *Handler) CreateIdea(c *gin.Context, teamID int64) {
	in, err := readUploadForm(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	in.TeamID = teamID
	created, err := h.svc.UploadFirst(c.Request.Context(), in, c.ClientIP())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("ETag", etag(created.Revision))
	c.JSON(http.StatusCreated, toAPIIdea(created))
}

// ReplaceIdeaFile handles PUT /ideas/{ideaId}/file.
func (h *Handler) ReplaceIdeaFile(c *gin.Context, ideaID int64, params api.ReplaceIdeaFileParams) {
	if params.IfMatch == nil || strings.TrimSpace(*params.IfMatch) == "" {
		h.fail(c, ErrPreconditionRequired())
		return
	}
	rev, ok := parseETag(*params.IfMatch)
	if !ok {
		h.fail(c, ErrInvalidRequest())
		return
	}
	in, err := readUploadForm(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	replaced, err := h.svc.ReplaceFile(c.Request.Context(), ReplaceInput{
		IdeaID: ideaID, ExpectedRevision: rev,
		FileName: in.FileName, ContentType: in.ContentType, Data: in.Data,
	}, c.ClientIP())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("ETag", etag(replaced.Revision))
	c.JSON(http.StatusOK, toAPIIdea(replaced))
}

// parseETag reads the revision out of an If-Match value: a strong or weak ETag as sent
// by etag(). Lists and "*" are not supported and count as invalid.
func parseETag(v string) (int64, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "W/")
	v = strings.Trim(v, `"`)
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil && n > 0
}

// readUploadForm reads the multipart body into memory. The whole file is held in memory
// and nothing touches the disk (Architecture 8.2), so the body is capped first. A body
// that ends early or is not multipart is a plain 400 and nothing has been stored yet
// (IDEA-006).
func readUploadForm(c *gin.Context) (UploadInput, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, upload.MaxFileSize+maxFormOverhead)
	mr, err := c.Request.MultipartReader()
	if err != nil {
		return UploadInput{}, ErrInvalidRequest()
	}
	var in UploadInput
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return in, nil
		}
		if err != nil {
			return UploadInput{}, bodyError(err)
		}
		switch part.FormName() {
		case "title":
			in.Title, err = readField(part)
		case "summary":
			in.Summary, err = readField(part)
		case "file":
			if in.Data != nil {
				return UploadInput{}, ErrInvalidRequest() // exactly one file part
			}
			err = readFile(part, &in)
		}
		if err != nil {
			return UploadInput{}, bodyError(err)
		}
	}
}

func readField(p *multipart.Part) (string, error) {
	b, err := io.ReadAll(io.LimitReader(p, maxFieldBytes+1))
	return string(b), err
}

// readFile reads at most one byte more than allowed: enough for upload.Validate to answer
// "too large" without buffering what a hostile client keeps sending.
func readFile(p *multipart.Part, in *UploadInput) error {
	b, err := io.ReadAll(io.LimitReader(p, upload.MaxFileSize+1))
	if err != nil {
		return err
	}
	in.Data = b
	in.FileName = p.FileName()
	in.ContentType = p.Header.Get("Content-Type")
	return nil
}

// bodyError maps a read failure to the problem the client sees: a body over the cap is
// "too large", anything else (a dropped connection, broken framing) is a bad request.
func bodyError(err error) error {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return platform.NewProblem(http.StatusRequestEntityTooLarge, upload.CodeFileTooLarge, "The file exceeds 5 MB.")
	}
	return ErrInvalidRequest()
}

// etag renders the optimistic-lock revision as a strong ETag (IDEA-030).
func etag(revision int64) string { return `"` + strconv.FormatInt(revision, 10) + `"` }

func toAPIIdea(i Idea) api.Idea {
	return api.Idea{
		Id: i.ID, TeamId: i.TeamID, Title: i.Title, Summary: i.Summary,
		CurrentVersion: api.IdeaVersion{
			VersionNo: i.CurrentVersion.No, UploadedBy: i.CurrentVersion.UploadedBy,
			// I18N-004: Asia/Taipei with its offset.
			UploadedAt:       i.CurrentVersion.UploadedAt.In(event.Taipei).Format("2006-01-02T15:04:05-07:00"),
			OriginalFileName: i.CurrentVersion.OriginalFileName, SizeBytes: i.CurrentVersion.SizeBytes,
		},
	}
}

// fail renders err as Problem Details. Unexpected errors are logged here (they are hidden
// from the client by AsProblem) and never include request content.
func (h *Handler) fail(c *gin.Context, err error) {
	var p *platform.Problem
	if !errors.As(err, &p) {
		h.log.ErrorContext(c.Request.Context(), "idea request failed", "error", err)
	}
	platform.WriteProblem(c.Writer, c.Request.URL.Path, platform.AsProblem(err))
}
