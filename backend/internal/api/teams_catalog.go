package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/policy"
)

// Ready-to-hire teams (F2, docs/architecture/team-catalog.md): GET /teams, GET /teams/{key} and
// POST /teams/{key}/install. The catalog is data (catalog/data/teams); the preview is computed for this organization
// and the install goes through the same path as hiring a role (Orchestrator.HireForRole).
func (s *server) mountTeamCatalog(r chi.Router) {
	r.With(s.can(auth.PermAgentsRead)).Get("/teams", s.listTeamCatalog)
	r.With(s.can(auth.PermAgentsRead)).Get("/teams/{key}", s.getTeamCatalog)
	r.With(s.can(auth.PermAgentsWrite)).Post("/teams/{key}/install", s.installTeamCatalog)
}

// teamInstallLocks serializes the installs of one organization: two at once must not hire the same role twice.
var teamInstallLocks sync.Map // org -> *sync.Mutex

func (s *server) teamCatalog() *catalog.Catalog {
	if s.OrgConfig != nil {
		return s.OrgConfig.Catalog()
	}
	return catalog.Default()
}

// teamTextLocale is the language of the team's texts: the caller's (?locale=) or the organization's.
func (s *server) teamTextLocale(r *http.Request) string {
	if s.OrgConfig != nil {
		return s.locale(r)
	}
	return catalog.NormalizeLocale(templateLocale(r))
}

// teamDest is where a team would be hired: an office (General or a department) and how agents map to offices.
type teamDest struct {
	ID        string
	Name      string
	Suggested string
	officeOf  func(agentID string) string
}

// visibleOffice reports whether the viewer may know the office exists: General, an open one or one they belong to.
// A private office the viewer is not in is hidden from everyone, admins included (D3).
func visibleOffice(m *offices.Model, v offices.Viewer, o offices.Office) bool {
	return o.General || o.Visibility != offices.Private || m.IsMember(v.UserID, o.ID)
}

// teamDestination resolves the destination of a team for the request. requested "" means the suggested department
// (the one named like the team's, if the viewer can see and manage it) or General.
func (s *server) teamDestination(r *http.Request, m *offices.Model, t catalog.TeamView, requested string) (teamDest, error) {
	d := teamDest{ID: offices.GeneralID, Name: "General"}
	if m == nil {
		if requested != "" && requested != offices.GeneralID {
			return d, fmt.Errorf("%w: department %q", domain.ErrNotFound, requested)
		}
		return d, nil
	}
	v := s.viewer(r)
	d.officeOf = func(agentID string) string { return m.Agent(agentID).OfficeID }
	name := strings.ToLower(strings.TrimSpace(t.Department))
	for _, o := range m.Offices() {
		if !o.General && visibleOffice(m, v, o) && m.CanManage(v, o.ID) && strings.ToLower(strings.TrimSpace(o.Name)) == name {
			d.Suggested = o.ID
			break
		}
	}
	id := requested
	if id == "" {
		id = d.Suggested
	}
	if id == "" || id == offices.GeneralID {
		return d, nil
	}
	o, ok := m.Office(id)
	if !ok || !visibleOffice(m, v, o) {
		return d, fmt.Errorf("%w: department %q", domain.ErrNotFound, requested) // hidden = absent
	}
	if !m.CanManage(v, id) {
		return d, fmt.Errorf("%w: you do not manage this department", domain.ErrForbidden)
	}
	d.ID, d.Name = o.ID, o.Name
	return d, nil
}

type teamWeekItem struct {
	catalog.TeamWeekView
	Name        string `json:"name"`
	Description string `json:"description"`
}

type teamPreviewView struct {
	Team        catalog.TeamView `json:"team"`
	Destination struct {
		OfficeID          string `json:"office_id"`
		OfficeName        string `json:"office_name"`
		SuggestedOfficeID string `json:"suggested_office_id"`
	} `json:"destination"`
	application.TeamPreview
	Week []teamWeekItem `json:"week"`
	// CanInstall is false when there is nothing to hire or the office or the plan has no room for the new people.
	CanInstall    bool   `json:"can_install"`
	BlockedReason string `json:"blocked_reason,omitempty"` // "nothing_new", "office_full" or "plan_limit"
}

// agentRoom is how many more agents the office and the plan allow (-1 = no limit known).
func (s *server) agentRoom(r *http.Request, total int) (room int, reason string) {
	room, reason = maxAgentsPerOrg-total, "office_full"
	if t := s.planLimits(r.Context(), s.org(r)); t.MaxAgents > 0 && t.MaxAgents-total < room {
		room, reason = t.MaxAgents-total, "plan_limit"
	}
	return room, reason
}

// teamModel is the offices model read fresh (nil without the offices service): a team that was just installed must
// show as installed at once.
func (s *server) teamModel(r *http.Request) (*offices.Model, error) {
	if s.Offices == nil {
		return nil, nil
	}
	return s.Offices.Fresh(r.Context(), s.org(r))
}

func (s *server) teamPreview(r *http.Request, m *offices.Model, agents []domain.Agent, t catalog.TeamView, requested string) (teamPreviewView, error) {
	var out teamPreviewView
	d, err := s.teamDestination(r, m, t, requested)
	if err != nil {
		return out, err
	}
	out.Team = t
	out.Destination.OfficeID, out.Destination.OfficeName, out.Destination.SuggestedOfficeID = d.ID, d.Name, d.Suggested
	out.TeamPreview = application.PreviewTeam(t, agents, d.officeOf, d.ID, s.orgLang(r.Context()))
	s.maskHiddenExisting(r, &out.TeamPreview)
	for _, w := range t.Week {
		it := teamWeekItem{TeamWeekView: w}
		if wf, ok := s.teamCatalog().Workflow(w.Template, s.teamTextLocale(r)); ok {
			it.Name, it.Description = wf.Name, wf.Description
		}
		out.Week = append(out.Week, it)
	}
	if out.NewCount == 0 {
		out.BlockedReason = "nothing_new"
	} else if room, why := s.agentRoom(r, len(agents)); room < out.NewCount {
		out.BlockedReason = why
	}
	out.CanInstall = out.BlockedReason == ""
	return out, nil
}

// maskHiddenExisting hides the name of an existing agent the viewer may not see (a person's private agent): the
// role still counts as filled, so nothing is hired twice, but who they are stays private.
func (s *server) maskHiddenExisting(r *http.Request, p *application.TeamPreview) {
	m, v, err := s.agentModel(r)
	if err != nil || m == nil {
		return
	}
	for i := range p.Agents {
		if e := p.Agents[i].Existing; e != nil && !m.Rights(v, e.ID).See {
			p.Agents[i].Existing = &application.TeamExistingRef{Title: e.Title}
		}
	}
}

type teamCardView struct {
	Key           string          `json:"key"`
	Version       int             `json:"version"`
	Name          string          `json:"name"`
	For           string          `json:"for"`
	Solves        string          `json:"solves"`
	Department    string          `json:"department"`
	Packs         []string        `json:"packs"`
	Agents        []teamCardAgent `json:"agents"`
	Templates     int             `json:"templates"`
	NewCount      int             `json:"new_count"`
	ExistingCount int             `json:"existing_count"`
}

type teamCardAgent struct {
	Role  string `json:"role"`
	Title string `json:"title"`
}

func (s *server) listTeamCatalog(w http.ResponseWriter, r *http.Request) {
	out := []teamCardView{}
	m, err := s.teamModel(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	agents, err := s.Store.ListAgents(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, t := range s.teamCatalog().Teams(s.teamTextLocale(r), r.URL.Query().Get("pack")) {
		p, err := s.teamPreview(r, m, agents, t, "")
		if err != nil {
			s.fail(w, err)
			return
		}
		c := teamCardView{Key: t.Key, Version: t.Version, Name: t.Name, For: t.For, Solves: t.Solves, Department: t.Department,
			Packs: t.Packs, Agents: []teamCardAgent{}, Templates: len(t.Week), NewCount: p.NewCount, ExistingCount: p.ExistingCount}
		for _, a := range p.Agents {
			c.Agents = append(c.Agents, teamCardAgent{Role: a.Role, Title: a.Title})
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getTeamCatalog(w http.ResponseWriter, r *http.Request) {
	t, ok := s.teamCatalog().Team(chi.URLParam(r, "key"), s.teamTextLocale(r))
	if !ok {
		s.fail(w, fmt.Errorf("%w: team %q", domain.ErrNotFound, chi.URLParam(r, "key")))
		return
	}
	m, err := s.teamModel(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	agents, err := s.Store.ListAgents(r.Context(), s.org(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	p, err := s.teamPreview(r, m, agents, t, r.URL.Query().Get("office_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type teamInstallResult struct {
	Created []domain.Agent  `json:"created"`
	Preview teamPreviewView `json:"preview"`
}

// installTeamCatalog hires the people of the team the destination department lacks. Same guarantees as hiring a role
// by hand (HireForRole): no connection is granted, autonomy starts at the role's default under its ceiling, and each
// hire is audited. The default skills come with the role. Calling it again hires nobody.
func (s *server) installTeamCatalog(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OfficeID string `json:"office_id"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	key := chi.URLParam(r, "key")
	t, ok := s.teamCatalog().Team(key, s.teamTextLocale(r))
	if !ok {
		s.fail(w, fmt.Errorf("%w: team %q", domain.ErrNotFound, key))
		return
	}
	if s.Orch == nil {
		s.fail(w, fmt.Errorf("%w: this deployment cannot hire agents", domain.ErrInvalid))
		return
	}
	org := s.org(r)
	mu, _ := teamInstallLocks.LoadOrStore(org, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	m, err := s.teamModel(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	d, err := s.teamDestination(r, m, t, body.OfficeID)
	if err != nil {
		s.fail(w, err)
		return
	}
	agents, err := s.Store.ListAgents(r.Context(), org)
	if err != nil {
		s.fail(w, err)
		return
	}
	loc := s.orgLang(r.Context())
	pre := application.PreviewTeam(t, agents, d.officeOf, d.ID, loc)
	newRoles := pre.NewRoles()
	// the office and plan caps are checked for the whole team before anyone is hired
	if len(agents)+len(newRoles) > maxAgentsPerOrg {
		s.fail(w, fmt.Errorf("%w: the office is full (%d agents)", domain.ErrConflict, maxAgentsPerOrg))
		return
	}
	if pl := s.planLimits(r.Context(), org); pl.MaxAgents > 0 && len(newRoles) > 0 && policy.AtLimit(len(agents)+len(newRoles)-1, pl.MaxAgents) {
		s.fail(w, &domain.PlanLimitError{Kind: policy.LimitAgents, Limit: pl.MaxAgents, Plan: pl.ID})
		return
	}
	created := []domain.Agent{}
	for _, role := range newRoles {
		a, err := s.Orch.HireForRole(r.Context(), role, loc)
		if err != nil {
			s.fail(w, err)
			return
		}
		created = append(created, a)
		if d.ID != offices.GeneralID && s.OrgChart != nil {
			if err := s.OrgChart.AssignAgent(r.Context(), a.ID, d.ID); err != nil {
				s.fail(w, err)
				return
			}
		}
	}
	if len(created) > 0 && s.Rec != nil {
		ids := make([]string, 0, len(created))
		for _, a := range created {
			ids = append(ids, a.ID)
		}
		s.Rec.Audit(r.Context(), domain.AuditLog{Actor: s.actor(r), Action: "team.installed", Entity: "team", EntityID: key,
			Details: map[string]any{"team_version": t.Version, "office_id": d.ID, "created": ids, "already_there": pre.ExistingCount}})
	}
	// the answer is the preview after the install (everybody is "already there" now)
	m, err = s.teamModel(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if agents, err = s.Store.ListAgents(r.Context(), org); err != nil {
		s.fail(w, err)
		return
	}
	prev, err := s.teamPreview(r, m, agents, t, d.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	status := http.StatusOK
	if len(created) > 0 {
		status = http.StatusCreated
	}
	writeJSON(w, status, teamInstallResult{Created: created, Preview: prev})
}
