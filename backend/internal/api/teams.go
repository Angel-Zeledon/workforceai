package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/orgchart"
	"aiworkforce/backend/internal/policy"
	"aiworkforce/backend/internal/roles"
)

// E1 "Alta y agentes por rol" (docs/architecture/equipos-oficinas.md §4 and §6). A person's profile (profession and
// team, written by the sign-up, an invitation or the verified-domain join) becomes, on their first request:
//   - the agents of their profession that the organization does not have yet (always with the assistant), within the
//     office cap and the plan's agents;
//   - their team (department), created when it was only a name, with them as its head when they are its responsible;
//   - their place in that team, and the new agents working in it.
//
// Nothing here grants access: a team is still a department of the org chart (E2 turns it into an office).
// Provisioning is best-effort and never fails the request that triggered it.

// memberLocks serializes the provisioning of one person (two first requests at once must not hire twice).
var memberLocks sync.Map // org|user -> *sync.Mutex

func (s *server) teamsOn() bool { return s.AuthEnabled && s.Auth != nil }

// ensureMember provisions the person behind the request once per process.
func (s *server) ensureMember(ctx context.Context, org, user string) {
	if !s.teamsOn() || user == "" {
		return
	}
	key := org + "|" + user
	if _, ok := s.provisioned.Load(key); ok {
		return
	}
	mu, _ := memberLocks.LoadOrStore(key, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if _, ok := s.provisioned.Load(key); ok {
		return
	}
	p, err := s.Auth.MemberProfile(ctx, org, user)
	if errors.Is(err, auth.ErrNotFound) {
		s.provisioned.Store(key, struct{}{})
		return
	}
	if err != nil {
		s.Log.Warn("teams: profile lookup failed", "err", err)
		return
	}
	if !p.ProvisionedAt.IsZero() {
		s.provisioned.Store(key, struct{}{})
		return
	}
	if err := s.provisionMember(ctx, p); err != nil {
		s.Log.Warn("teams: provisioning failed", "err", err)
		return
	}
	s.provisioned.Store(key, struct{}{})
}

// teamLocale is the language the organization's new agents and team names are written in.
func (s *server) teamLocale(ctx context.Context, org string) string {
	if st, err := s.Auth.OrgSetup(ctx, org); err == nil && st.Locale != "" {
		return st.Locale
	}
	return "" // HireForRole falls back to the organization's language
}

// provisionMember sets up the agents, team and place of a person and marks the profile provisioned.
func (s *server) provisionMember(ctx context.Context, p auth.MemberProfile) error {
	org := p.OrgID
	loc := s.teamLocale(ctx, org)
	if p.DepartmentID == "" && p.TeamName != "" && s.OrgChart != nil {
		d, err := s.OrgChart.CreateDepartment(ctx, orgchart.DepartmentInput{Name: p.TeamName, Head: orgchart.Ref{Kind: orgchart.KindPerson, ID: p.UserID}})
		if err != nil {
			return err
		}
		p.DepartmentID, p.TeamName = d.ID, ""
	}
	created, err := s.hireForProfession(ctx, org, p.Profession, loc)
	if err != nil {
		return err
	}
	if p.DepartmentID != "" && s.OrgChart != nil {
		s.placeInTeam(ctx, p.UserID, p.DepartmentID, p.Lead)
		for _, id := range created {
			if err := s.OrgChart.AssignAgent(ctx, id, p.DepartmentID); err != nil {
				s.Log.Warn("teams: agent not placed in the team", "agent", id, "err", err)
			}
		}
	}
	p.ProvisionedAt = time.Now().UTC()
	return s.Auth.SaveMemberProfile(ctx, p)
}

// hireForProfession adds the suggested agents of a profession that the organization lacks (one per role), within the
// office cap and the plan's agents. It returns the ids it created.
func (s *server) hireForProfession(ctx context.Context, org, profession, loc string) ([]string, error) {
	if s.Orch == nil {
		return nil, nil
	}
	agents, err := s.Store.ListAgents(ctx, org)
	if err != nil {
		return nil, err
	}
	limit := maxAgentsPerOrg
	if t := s.planLimits(ctx, org); t.MaxAgents > 0 && t.MaxAgents < limit {
		limit = t.MaxAgents
	}
	var created []string
	for _, role := range roles.SuggestedRoles(profession) {
		if slices.ContainsFunc(agents, func(a domain.Agent) bool { return a.Role == role }) {
			continue
		}
		if policy.AtLimit(len(agents), limit) {
			s.Log.Info("teams: suggested agent skipped, the office is full", "role", role, "limit", limit)
			continue
		}
		a, err := s.Orch.HireForRole(ctx, role, loc)
		if err != nil {
			return created, err
		}
		agents = append(agents, a)
		created = append(created, a.ID)
	}
	return created, nil
}

// placeInTeam puts a person in a department (keeping their title and boss) and makes them its head when they are its
// responsible and it has none. Failures are logged: the profile still says which team is theirs.
func (s *server) placeInTeam(ctx context.Context, userID, deptID string, lead bool) {
	org := application.OrgFrom(ctx, "")
	in := orgchart.PlacementInput{DepartmentID: deptID}
	if ps, err := s.OrgChart.Store.ListPlacements(ctx, org); err == nil {
		for _, x := range ps {
			if x.UserID == userID {
				in.Title, in.ReportsTo = x.Title, x.ReportsTo
			}
		}
	}
	if _, err := s.OrgChart.PutPlacement(ctx, userID, in); err != nil {
		s.Log.Warn("teams: person not placed in the team", "err", err)
	}
	if !lead {
		return
	}
	deps, err := s.OrgChart.Departments(ctx)
	if err != nil {
		return
	}
	for _, d := range deps {
		if d.ID == deptID && d.Head.IsZero() {
			if _, err := s.OrgChart.UpdateDepartment(ctx, d.ID, orgchart.DepartmentInput{Name: d.Name, ParentID: d.ParentID, CostCenter: d.CostCenter,
				Head: orgchart.Ref{Kind: orgchart.KindPerson, ID: userID}}); err != nil {
				s.Log.Warn("teams: responsible not set as head", "err", err)
			}
		}
	}
}

// ---- HTTP ----

// mountTeams: the welcome of each person and the team set-up (E1).
//
//	GET  /me/welcome        any member: which welcome to show (solo, owner, lead, member, classic) and its contents
//	POST /me/team           any member: {department_id?, profession?} pick a team (only while having none) and a profession
//	POST /teams             org:manage: {name?, profession, head_me?} a team = a department + the agents of its work
//	POST /teams/setup/done  org:manage: the responsible finished (or skipped) setting up the teams
func (s *server) mountTeams(r chi.Router) {
	if !s.teamsOn() {
		return
	}
	r.Get("/me/welcome", s.getWelcome)
	r.Post("/me/team", s.pickTeam)
	r.With(s.can(auth.PermOrgManage)).Post("/teams", s.createTeam)
	r.With(s.can(auth.PermOrgManage)).Post("/teams/setup/done", s.teamSetupDone)
}

type welcomeTeam struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	HeadName string `json:"head_name"`
	HeadIsMe bool   `json:"head_is_me"`
}

type welcomeAgent struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Title string `json:"title"`
	Role  string `json:"role"`
}

type welcomePerson struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

type teamRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Welcome is what GET /me/welcome answers.
type Welcome struct {
	// Kind picks the welcome: solo (personal office), owner (responsible of the company), lead (responsible of a
	// team), member (joined a team), classic (an organization from before E1: the usual welcome).
	Kind         string          `json:"kind"`
	Audience     string          `json:"audience"`
	OrgName      string          `json:"org_name"`
	SetupPending bool            `json:"setup_pending"`
	Profession   string          `json:"profession"`
	Team         *welcomeTeam    `json:"team"`
	InvitedBy    string          `json:"invited_by"`
	Agents       []welcomeAgent  `json:"agents"`
	Teammates    []welcomePerson `json:"teammates"`
	CanPickTeam  bool            `json:"can_pick_team"`
	Teams        []teamRef       `json:"teams"`
}

func (s *server) welcomeFor(ctx context.Context, org string, p auth.Principal) (Welcome, error) {
	w := Welcome{Agents: []welcomeAgent{}, Teammates: []welcomePerson{}, Teams: []teamRef{}}
	setup, serr := s.Auth.OrgSetup(ctx, org)
	prof, perr := s.Auth.MemberProfile(ctx, org, p.UserID)
	if serr != nil && !errors.Is(serr, auth.ErrNotFound) {
		return w, serr
	}
	if perr != nil && !errors.Is(perr, auth.ErrNotFound) {
		return w, perr
	}
	hasSetup, hasProfile := serr == nil, perr == nil
	if orgs, err := s.Auth.Orgs(ctx, p.UserID); err == nil {
		for _, o := range orgs {
			if o.OrgID == org {
				w.OrgName = o.Name
			}
		}
	}
	w.Audience = string(setup.Audience)
	switch {
	case !hasSetup && !hasProfile:
		w.Kind = "classic"
		return w, nil
	case hasSetup && p.Role == auth.RoleOwner && setup.Audience == auth.AudienceSolo:
		w.Kind = "solo"
	case hasSetup && p.Role == auth.RoleOwner:
		w.Kind, w.SetupPending = "owner", setup.SetupDoneAt.IsZero()
	case prof.Lead:
		w.Kind = "lead"
	default:
		w.Kind = "member"
	}
	w.Profession = prof.Profession
	members, _ := s.Auth.Directory(ctx, org)
	nameOf := func(id string) string {
		for _, m := range members {
			if m.UserID == id {
				return m.Name
			}
		}
		return ""
	}
	if prof.InvitedBy != "" {
		w.InvitedBy = nameOf(prof.InvitedBy)
	}
	agents, err := s.Store.ListAgents(ctx, org)
	if err != nil {
		return w, err
	}
	for _, role := range roles.SuggestedRoles(prof.Profession) {
		for _, a := range agents {
			if a.Role == role {
				w.Agents = append(w.Agents, welcomeAgent{ID: a.ID, Name: a.Name, Title: a.Title, Role: a.Role})
				break
			}
		}
	}
	if s.OrgChart == nil {
		return w, nil
	}
	deps, err := s.OrgChart.Departments(ctx)
	if err != nil {
		return w, err
	}
	for _, d := range deps {
		w.Teams = append(w.Teams, teamRef{ID: d.ID, Name: d.Name})
		if d.ID != prof.DepartmentID {
			continue
		}
		t := &welcomeTeam{ID: d.ID, Name: d.Name, HeadIsMe: d.Head == orgchart.Ref{Kind: orgchart.KindPerson, ID: p.UserID}}
		switch d.Head.Kind {
		case orgchart.KindPerson:
			t.HeadName = nameOf(d.Head.ID)
		case orgchart.KindAgent:
			for _, a := range agents {
				if a.ID == d.Head.ID {
					t.HeadName = a.Name
				}
			}
		}
		w.Team = t
	}
	if w.Team != nil {
		if ps, err := s.OrgChart.Store.ListPlacements(ctx, org); err == nil {
			for _, x := range ps {
				if x.DepartmentID == w.Team.ID && x.UserID != p.UserID {
					if n := nameOf(x.UserID); n != "" {
						w.Teammates = append(w.Teammates, welcomePerson{UserID: x.UserID, Name: n})
					}
				}
			}
		}
	}
	w.CanPickTeam = (w.Kind == "member" || w.Kind == "lead") && prof.DepartmentID == ""
	return w, nil
}

func (s *server) getWelcome(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	out, err := s.welcomeFor(r.Context(), s.org(r), p)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// pickTeam: a person who came in without a team (verified domain, single sign-on, a plain invitation) joins one and/or
// says what they do. A team does not give access to anything in E1, so the person joins directly and it is audited;
// with E2 (offices) joining will ask the responsible.
func (s *server) pickTeam(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DepartmentID string `json:"department_id"`
		Profession   string `json:"profession"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	ctx, org := r.Context(), s.org(r)
	p, _ := auth.PrincipalFrom(ctx)
	body.DepartmentID, body.Profession = strings.TrimSpace(body.DepartmentID), strings.TrimSpace(body.Profession)
	if body.Profession != "" && !roles.ValidProfession(body.Profession) {
		s.fail(w, fmt.Errorf("%w: unknown profession %q", domain.ErrInvalid, body.Profession))
		return
	}
	prof, err := s.Auth.MemberProfile(ctx, org, p.UserID)
	if errors.Is(err, auth.ErrNotFound) {
		prof, err = auth.MemberProfile{OrgID: org, UserID: p.UserID, Via: auth.ViaSelf}, nil
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	if body.DepartmentID != "" {
		if prof.DepartmentID != "" {
			s.fail(w, fmt.Errorf("%w: you already have an office", domain.ErrConflict))
			return
		}
		if s.OrgChart == nil {
			s.fail(w, fmt.Errorf("%w: offices are not available", domain.ErrInvalid))
			return
		}
		deps, err := s.OrgChart.Departments(ctx)
		if err != nil {
			s.fail(w, err)
			return
		}
		if !slices.ContainsFunc(deps, func(d orgchart.Department) bool { return d.ID == body.DepartmentID }) {
			s.fail(w, fmt.Errorf("%w: unknown office %q", domain.ErrInvalid, body.DepartmentID))
			return
		}
		prof.DepartmentID = body.DepartmentID
	}
	if body.Profession != "" {
		prof.Profession = body.Profession
	}
	if err := s.provisionMember(ctx, prof); err != nil {
		s.fail(w, err)
		return
	}
	s.provisioned.Store(org+"|"+p.UserID, struct{}{})
	if s.Rec != nil {
		s.Rec.Audit(ctx, domain.AuditLog{Actor: p.UserID, Action: "team.joined", Entity: "person", EntityID: p.UserID,
			Details: map[string]any{"department_id": prof.DepartmentID, "profession": prof.Profession}})
	}
	out, err := s.welcomeFor(ctx, org, p)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// createTeam: a team is a department plus the agents of its work (the ones the organization lacks). With head_me the
// caller becomes its head and, if they have no team yet, a member of it.
func (s *server) createTeam(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       string `json:"name"`
		Profession string `json:"profession"`
		HeadMe     bool   `json:"head_me"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if s.OrgChart == nil {
		s.fail(w, fmt.Errorf("%w: offices are not available", domain.ErrInvalid))
		return
	}
	ctx, org := r.Context(), s.org(r)
	p, _ := auth.PrincipalFrom(ctx)
	if body.Profession == "" {
		body.Profession = roles.ProfessionOther
	}
	prof, ok := roles.ProfessionOf(body.Profession)
	if !ok {
		s.fail(w, fmt.Errorf("%w: unknown profession %q", domain.ErrInvalid, body.Profession))
		return
	}
	name := strings.Join(strings.Fields(body.Name), " ")
	if name == "" {
		name = prof.TeamName(s.teamLocale(ctx, org))
	}
	if utf8.RuneCountInString(name) > 80 {
		s.fail(w, fmt.Errorf("%w: the office name is too long (max 80)", domain.ErrInvalid))
		return
	}
	in := orgchart.DepartmentInput{Name: name}
	if body.HeadMe {
		in.Head = orgchart.Ref{Kind: orgchart.KindPerson, ID: p.UserID}
	}
	d, err := s.OrgChart.CreateDepartment(ctx, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	created, err := s.hireForProfession(ctx, org, prof.Key, s.teamLocale(ctx, org))
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, id := range created {
		if err := s.OrgChart.AssignAgent(ctx, id, d.ID); err != nil {
			s.Log.Warn("teams: agent not placed in the team", "agent", id, "err", err)
		}
	}
	if body.HeadMe {
		if mp, err := s.Auth.MemberProfile(ctx, org, p.UserID); err == nil && mp.DepartmentID == "" {
			mp.DepartmentID, mp.Lead = d.ID, true
			if err := s.Auth.SaveMemberProfile(ctx, mp); err == nil {
				s.placeInTeam(ctx, p.UserID, d.ID, false)
			}
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"team": teamRef{ID: d.ID, Name: d.Name}, "profession": prof.Key, "agents": list(created)})
}

func (s *server) teamSetupDone(w http.ResponseWriter, r *http.Request) {
	if err := s.Auth.MarkOrgSetupDone(r.Context(), s.org(r)); err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			s.fail(w, fmt.Errorf("%w: this organization has no office set-up", domain.ErrNotFound))
			return
		}
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
