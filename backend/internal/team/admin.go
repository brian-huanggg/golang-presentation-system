package team

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/audit"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/auth"
	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

const (
	actionAdminList = "admin.team_list"

	defaultPageSize = 20
	maxPageSize     = 100
	maxSearchRunes  = 50 // a team name is at most 50 characters (TEAM-004)
)

// Query parameter names, as in the API.
const (
	paramQ             = "q"
	paramTrack         = "track"
	paramAwardCategory = "awardCategory"
	paramDepartment    = "department"
	paramPage          = "page"
	paramSize          = "size"
	paramSort          = "sort"
)

// Sort keys the API accepts. Each maps to a fixed ORDER BY fragment, so request text
// never reaches the SQL.
var sortClauses = map[string]string{
	"id":    "t.id ASC",
	"-id":   "t.id DESC",
	"name":  "t.name ASC, t.id ASC",
	"-name": "t.name DESC, t.id ASC",
}

// AdminListInput is the raw query of the Admin team list (ADM-001). Pointers are nil for
// an absent parameter; values stay strings so every problem is reported at once.
type AdminListInput struct {
	Q             *string
	Track         *string
	AwardCategory *string
	Department    *string
	HasIdea       *bool
	Page          *int
	Size          *int
	Sort          *string
}

// adminFilter is a validated AdminListInput with defaults applied.
type adminFilter struct {
	q             string
	track         string
	awardCategory string
	department    string
	hasIdea       *bool
	page, size    int
	sort          string
}

// TeamSummary is one row of the Admin list.
type TeamSummary struct {
	ID            int64  `db:"id"`
	EventYear     int    `db:"event_year"`
	Name          string `db:"name"`
	Track         string `db:"track"`
	AwardCategory string `db:"award_category"`
	Department    string `db:"department"`
	LeaderID      string `db:"leader_employee_id"`
	LeaderName    string `db:"leader_full_name"`
	MemberCount   int    `db:"member_count"`
	HasIdea       bool   `db:"has_idea"`
}

// AdminPage is one page of the Admin list.
type AdminPage struct {
	Items []TeamSummary
	Total int64
	Page  int
	Size  int
}

// ListForAdmin lists and searches the teams of the current event year (ADM-001). Only an
// Admin may call it, and the Deadline does not apply (DDL-006). The read and its audit
// record share one transaction, because viewing other teams is itself audited (AUD-001).
func (s *Service) ListForAdmin(ctx context.Context, in AdminListInput, sourceIP string) (AdminPage, error) {
	actor, err := s.ident.CurrentUser(ctx)
	if err != nil {
		return AdminPage{}, err
	}
	if actor.Role != roleAdmin {
		p := ErrForbidden()
		s.auditFailure(ctx, actor, actionAdminList, "", sourceIP, p)
		return AdminPage{}, p
	}
	f, fieldErrs := validateAdminList(in)
	if len(fieldErrs) > 0 {
		p := auth.ErrValidation(fieldErrs)
		s.auditFailure(ctx, actor, actionAdminList, "", sourceIP, p)
		return AdminPage{}, p
	}

	year := s.event.Year()
	var page AdminPage
	err = platform.WithTx(ctx, s.db, func(tx *sqlx.Tx) error {
		var err error
		page, err = queryAdminTeams(ctx, tx, year, f)
		if err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			ActorEmployeeID: actor.EmployeeID, ActorRole: actor.Role,
			Action: actionAdminList, TargetType: targetTeam, TargetID: "",
			Result: audit.ResultSuccess, SourceIP: sourceIP,
			Changes: map[string]any{"filters": auditFilters(f), "total": page.Total},
		})
	})
	if err != nil {
		return AdminPage{}, err
	}
	return page, nil
}

func auditFilters(f adminFilter) map[string]any {
	m := map[string]any{"page": f.page, "size": f.size, "sort": f.sort}
	for k, v := range map[string]string{paramQ: f.q, paramTrack: f.track, paramAwardCategory: f.awardCategory, paramDepartment: f.department} {
		if v != "" {
			m[k] = v
		}
	}
	if f.hasIdea != nil {
		m["hasIdea"] = *f.hasIdea
	}
	return m
}

// validateAdminList checks the query without the database and reports every problem.
func validateAdminList(in AdminListInput) (adminFilter, []platform.FieldError) {
	var errs []platform.FieldError
	bad := func(field, code string) { errs = append(errs, platform.FieldError{Field: field, Code: code}) }
	f := adminFilter{page: 1, size: defaultPageSize, sort: "id", hasIdea: in.HasIdea}

	if in.Q != nil {
		f.q = strings.TrimSpace(*in.Q)
		if utf8.RuneCountInString(f.q) > maxSearchRunes {
			bad(paramQ, codeTooLong)
		}
	}
	if in.Track != nil {
		if t, err := platform.ParseTrack(*in.Track); err != nil {
			bad(paramTrack, codeInvalidValue)
		} else {
			f.track = t.String()
		}
	}
	if in.AwardCategory != nil {
		if c, err := platform.ParseAwardCategory(*in.AwardCategory); err != nil {
			bad(paramAwardCategory, codeInvalidValue)
		} else {
			f.awardCategory = c.String()
		}
	}
	if in.Department != nil {
		if d, err := platform.ParseDepartment(*in.Department); err != nil {
			bad(paramDepartment, codeInvalidValue)
		} else {
			f.department = d.String()
		}
	}
	if in.Page != nil {
		if *in.Page < 1 {
			bad(paramPage, codeInvalidValue)
		} else {
			f.page = *in.Page
		}
	}
	if in.Size != nil {
		if *in.Size < 1 || *in.Size > maxPageSize {
			bad(paramSize, codeInvalidValue)
		} else {
			f.size = *in.Size
		}
	}
	if in.Sort != nil {
		if _, ok := sortClauses[*in.Sort]; !ok {
			bad(paramSort, codeInvalidValue)
		} else {
			f.sort = *in.Sort
		}
	}
	return f, errs
}

// likeEscaper makes a search term literal inside LIKE ... ESCAPE '|'.
var likeEscaper = strings.NewReplacer("|", "||", "%", "|%", "_", "|_")

// where builds the WHERE clause. Only fixed SQL fragments are concatenated; every
// request value is a bound argument.
func (f adminFilter) where(year int) (string, []any) {
	conds := []string{"t.event_year = ?", "t.deleted_at IS NULL"}
	args := []any{year}
	if f.q != "" {
		conds = append(conds, "t.name LIKE ? ESCAPE '|'")
		args = append(args, "%"+likeEscaper.Replace(f.q)+"%")
	}
	if f.track != "" {
		conds = append(conds, "t.track = ?")
		args = append(args, f.track)
	}
	if f.awardCategory != "" {
		conds = append(conds, "t.award_category = ?")
		args = append(args, f.awardCategory)
	}
	if f.department != "" {
		conds = append(conds, "t.department = ?")
		args = append(args, f.department)
	}
	if f.hasIdea != nil {
		not := ""
		if !*f.hasIdea {
			not = "NOT "
		}
		conds = append(conds, not+"EXISTS (SELECT 1 FROM idea i WHERE i.team_id = t.id AND i.deleted_at IS NULL)")
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

const adminSelect = "SELECT t.id, t.event_year, t.name, t.track, t.award_category, t.department, " +
	"lu.employee_id AS leader_employee_id, lu.full_name AS leader_full_name, " +
	"(SELECT COUNT(*) FROM team_member c WHERE c.team_id = t.id) AS member_count, " +
	"EXISTS (SELECT 1 FROM idea i WHERE i.team_id = t.id AND i.deleted_at IS NULL) AS has_idea " +
	"FROM team t " +
	"JOIN team_member lm ON lm.team_id = t.id AND lm.role = 'LEADER' " +
	"JOIN `user` lu ON lu.id = lm.user_id"

func queryAdminTeams(ctx context.Context, tx sqlx.QueryerContext, year int, f adminFilter) (AdminPage, error) {
	where, args := f.where(year)

	var total int64
	if err := sqlx.GetContext(ctx, tx, &total, "SELECT COUNT(*) FROM team t"+where, args...); err != nil {
		return AdminPage{}, fmt.Errorf("count teams: %w", err)
	}
	page := AdminPage{Items: []TeamSummary{}, Total: total, Page: f.page, Size: f.size}
	if total == 0 {
		return page, nil
	}
	q := adminSelect + where + " ORDER BY " + sortClauses[f.sort] + " LIMIT ? OFFSET ?"
	args = append(args, f.size, (f.page-1)*f.size)
	if err := sqlx.SelectContext(ctx, tx, &page.Items, q, args...); err != nil {
		return AdminPage{}, fmt.Errorf("list teams: %w", err)
	}
	return page, nil
}
