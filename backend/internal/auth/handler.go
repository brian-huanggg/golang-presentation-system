package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/api"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// maxRegisterBody bounds an unauthenticated request body; a valid one is well under 1 KiB.
const maxRegisterBody = 8 << 10

// Handler implements the auth operations of the generated api.ServerInterface.
type Handler struct {
	svc *Service
	log *slog.Logger
}

func NewHandler(svc *Service, log *slog.Logger) *Handler { return &Handler{svc: svc, log: log} }

// Register handles POST /auth/register.
func (h *Handler) Register(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRegisterBody)
	var req api.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, ErrInvalidRequest())
		return
	}
	in := RegisterInput{
		EmployeeID: req.EmployeeId, FullName: req.FullName, Department: string(req.Department),
		Password: req.Password, PreferredLanguage: string(req.PreferredLanguage),
	}
	if req.Email != nil {
		in.Email = *req.Email
	}

	u, err := h.svc.Register(c.Request.Context(), in, c.ClientIP())
	if err != nil {
		h.fail(c, err)
		return
	}
	out := api.RegisteredUser{
		EmployeeId: u.EmployeeID, FullName: u.FullName, Department: api.Department(u.Department),
		PreferredLanguage: api.Language(u.PreferredLanguage),
	}
	if u.Email != "" {
		out.Email = &u.Email
	}
	c.JSON(http.StatusCreated, out)
}

// fail renders err as Problem Details. Unexpected errors are logged here (they
// are hidden from the client by AsProblem) and never include request content.
func (h *Handler) fail(c *gin.Context, err error) {
	var p *platform.Problem
	if !errors.As(err, &p) {
		h.log.ErrorContext(c.Request.Context(), "register failed", "error", err)
	}
	platform.WriteProblem(c.Writer, c.Request.URL.Path, platform.AsProblem(err))
}
