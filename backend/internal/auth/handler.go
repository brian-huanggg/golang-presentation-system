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

// maxLoginBody leaves room for a 128 byte password plus an ID, and no more.
const maxLoginBody = 2 << 10

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

// Login handles POST /auth/login. The session cookie is written by the scs
// middleware wrapping the router, not here.
func (h *Handler) Login(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxLoginBody)
	var req api.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, ErrInvalidRequest())
		return
	}
	u, err := h.svc.Login(c.Request.Context(), req.EmployeeId, req.Password, c.ClientIP())
	if err != nil {
		h.fail(c, err)
		return
	}
	noStore(c)
	c.JSON(http.StatusOK, toAPIUser(u))
}

// Logout handles POST /auth/logout.
func (h *Handler) Logout(c *gin.Context) {
	if err := h.svc.Logout(c.Request.Context(), c.ClientIP()); err != nil {
		h.fail(c, err)
		return
	}
	noStore(c)
	c.Status(http.StatusNoContent)
}

// GetCurrentUser handles GET /auth/me.
func (h *Handler) GetCurrentUser(c *gin.Context) {
	u, err := h.svc.CurrentUser(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	noStore(c)
	c.JSON(http.StatusOK, toAPIUser(u))
}

// maxChangePasswordBody leaves room for two 128 byte passwords.
const maxChangePasswordBody = 2 << 10

// maxPreferencesBody: a language tag only.
const maxPreferencesBody = 1 << 10

// ChangePassword handles POST /auth/password.
func (h *Handler) ChangePassword(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxChangePasswordBody)
	var req api.ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, ErrInvalidRequest())
		return
	}
	if err := h.svc.ChangePassword(c.Request.Context(), req.CurrentPassword, req.NewPassword, c.ClientIP()); err != nil {
		h.fail(c, err)
		return
	}
	noStore(c)
	c.Status(http.StatusNoContent)
}

// UpdatePreferences handles PATCH /auth/me.
func (h *Handler) UpdatePreferences(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPreferencesBody)
	var req api.UpdatePreferencesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, ErrInvalidRequest())
		return
	}
	u, err := h.svc.UpdatePreferences(c.Request.Context(), string(req.PreferredLanguage))
	if err != nil {
		h.fail(c, err)
		return
	}
	noStore(c)
	c.JSON(http.StatusOK, toAPIUser(u))
}

// noStore keeps identity responses out of browser and proxy caches.
func noStore(c *gin.Context) { c.Header("Cache-Control", "no-store") }

func toAPIUser(u SessionUser) api.SessionUser {
	out := api.SessionUser{
		EmployeeId: u.EmployeeID, FullName: u.FullName, Role: api.SessionUserRole(u.Role),
		PreferredLanguage: api.Language(u.PreferredLanguage), MustChangePassword: u.MustChangePassword,
	}
	if u.Department != "" {
		d := api.Department(u.Department)
		out.Department = &d
	}
	return out
}

// fail renders err as Problem Details. Unexpected errors are logged here (they
// are hidden from the client by AsProblem) and never include request content.
func (h *Handler) fail(c *gin.Context, err error) {
	var p *platform.Problem
	if !errors.As(err, &p) {
		h.log.ErrorContext(c.Request.Context(), "auth request failed", "error", err)
	}
	platform.WriteProblem(c.Writer, c.Request.URL.Path, platform.AsProblem(err))
}
