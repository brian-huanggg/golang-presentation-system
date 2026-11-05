package team

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/api"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/auth"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// maxCreateBody: a name, two codes and at most a handful of 6-digit IDs fit far below this.
// Anything bigger is malformed (or hostile) and is refused before parsing.
const maxCreateBody = 4 << 10

// Handler implements the team operations of the generated api.ServerInterface.
type Handler struct {
	svc *Service
	log *slog.Logger
}

func NewHandler(svc *Service, log *slog.Logger) *Handler { return &Handler{svc: svc, log: log} }

// CreateTeam handles POST /teams.
func (h *Handler) CreateTeam(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCreateBody)
	var req api.CreateTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, auth.ErrInvalidRequest())
		return
	}
	in := CreateInput{Name: req.Name, Track: string(req.Track), AwardCategory: string(req.AwardCategory)}
	if req.MemberIds != nil {
		in.MemberIDs = *req.MemberIds
	}
	t, err := h.svc.Create(c.Request.Context(), in, c.ClientIP())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, toAPITeam(t))
}

// maxMemberBody: one Employee ID in a JSON object.
const maxMemberBody = 1 << 10

// AddTeamMember handles POST /teams/{teamId}/members.
func (h *Handler) AddTeamMember(c *gin.Context, teamID api.TeamId) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxMemberBody)
	var req api.AddTeamMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, auth.ErrInvalidRequest())
		return
	}
	t, err := h.svc.AddMember(c.Request.Context(), teamID, req.EmployeeId, c.ClientIP())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, toAPITeam(t))
}

// RemoveTeamMember handles DELETE /teams/{teamId}/members/{employeeId}.
func (h *Handler) RemoveTeamMember(c *gin.Context, teamID api.TeamId, employeeID string) {
	if err := h.svc.RemoveMember(c.Request.Context(), teamID, employeeID, c.ClientIP()); err != nil {
		h.fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusNoContent)
}

// LeaveTeam handles POST /teams/{teamId}/leave.
func (h *Handler) LeaveTeam(c *gin.Context, teamID api.TeamId) {
	if err := h.svc.Leave(c.Request.Context(), teamID, c.ClientIP()); err != nil {
		h.fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusNoContent)
}

func toAPITeam(t Team) api.Team {
	members := make([]api.TeamMember, 0, len(t.Members))
	for _, m := range t.Members {
		members = append(members, api.TeamMember{EmployeeId: m.EmployeeID, FullName: m.FullName, Role: api.TeamMemberRole(m.Role)})
	}
	return api.Team{
		Id: t.ID, EventYear: t.EventYear, Name: t.Name, Track: api.Track(t.Track),
		AwardCategory: api.AwardCategory(t.AwardCategory), Department: api.Department(t.Department),
		Members: members,
	}
}

// fail renders err as Problem Details. Unexpected errors are logged here (they are hidden
// from the client by AsProblem) and never include request content.
func (h *Handler) fail(c *gin.Context, err error) {
	var p *platform.Problem
	if !errors.As(err, &p) {
		h.log.ErrorContext(c.Request.Context(), "team request failed", "error", err)
	}
	platform.WriteProblem(c.Writer, c.Request.URL.Path, platform.AsProblem(err))
}
