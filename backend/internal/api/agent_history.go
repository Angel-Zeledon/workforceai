package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/agenthistory"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/orgchart"
	"aiworkforce/backend/internal/skills"
)

// F4: the configuration history of every agent, with "go back to how it was" (docs/architecture/agent-history.md).
//
//	GET  /agents/{id}/versions                    the timeline (newest first, without the snapshots)
//	GET  /agents/{id}/versions/{v}                one version with its changes against the previous one
//	GET  /agents/{id}/versions/{v}/restore        what restoring it would do and what not, and why (preview)
//	POST /agents/{id}/versions/{v}/restore        restore it as a NEW version
//
// Reading needs agents:read, restoring agents:write; the agent of a private department answers 404 to whoever is
// not a member (workAgent). Every field is restored through the service that owns it, with the person's context.

// NewAgentHistory builds the history service over the dependencies of the API: its sources read each part of an
// agent's configuration from the service or store that owns it. The server binds the event observer to it and
// passes it back in Deps.History (cmd/server/main.go); tests do the same.
func NewAgentHistory(d Deps, store agenthistory.Store) *agenthistory.Service {
	s := &server{Deps: d}
	h := &agenthistory.Service{Store: store, Log: d.Log, Sources: s.historySources(),
		Agents: func(ctx context.Context, org string) ([]string, error) {
			as, err := d.Store.ListAgents(ctx, org)
			ids := make([]string, 0, len(as))
			for _, a := range as {
				ids = append(ids, a.ID)
			}
			return ids, err
		}}
	if d.Rec != nil {
		h.Audit = func(ctx context.Context, org string, a domain.AuditLog) { d.Rec.Audit(application.WithOrg(ctx, org), a) }
	}
	return h
}

func (s *server) mountAgentHistory(r chi.Router) {
	if s.History == nil {
		return
	}
	read, write := s.can(auth.PermAgentsRead), s.can(auth.PermAgentsWrite)
	r.With(read, s.workAgent).Get("/agents/{id}/versions", s.listAgentVersions)
	r.With(read, s.workAgent).Get("/agents/{id}/versions/{v}", s.getAgentVersion)
	r.With(read, s.workAgent).Get("/agents/{id}/versions/{v}/restore", s.previewAgentRestore)
	r.With(write, s.workAgent).Post("/agents/{id}/versions/{v}/restore", s.restoreAgentVersion)
}

// ---- the common hook: every change of an agent leaves a version ----

// orgWideConfigRoutes change agents without naming one in the path: after them every agent of the organization is
// recorded (an agent whose configuration did not change gets no version).
var orgWideConfigRoutes = []string{
	"/api/v1/onboarding", "/api/v1/org/settings", "/api/v1/org/departments", "/api/v1/org/industries",
	"/api/v1/offices", "/api/v1/skills/{id}", "/api/v1/approvals/{id}/remember", "/api/v1/trust/proposals",
	"/api/v1/teams", "/api/v1/me/team", "/api/v1/demo/reset",
}

// agentRouteID is the agent of a route that names one ("" otherwise).
func agentRouteID(r *http.Request, pattern string) string {
	if strings.Contains(pattern, "/agents/{id}") {
		return chi.URLParam(r, "id")
	}
	return ""
}

// agentInPath reads the agent id of a path before the route is known ("/api/v1/agents/{id}/..." and
// "/api/v1/governance/agents/{id}/..."); used only to write the baseline before the first change.
func agentInPath(path string) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i := 0; i+2 < len(segs); i++ {
		if segs[i] == "agents" {
			return segs[i+1]
		}
	}
	return ""
}

// isOrgWide reports whether a request path is one of orgWideConfigRoutes (segment by segment; "{id}" matches any).
func isOrgWide(path string) bool {
	ps := strings.Split(strings.Trim(path, "/"), "/")
	for _, p := range orgWideConfigRoutes {
		xs := strings.Split(strings.Trim(p, "/"), "/")
		if len(ps) < len(xs) {
			continue
		}
		ok := true
		for i, x := range xs {
			if x != "{id}" && x != ps[i] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(c int) {
	if w.status == 0 {
		w.status = c
	}
	w.ResponseWriter.WriteHeader(c)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// historyHook records the agent a successful change names (or every agent, for the org-wide routes) once the
// handler is done, so no handler has to remember to write history. While it runs, the event observer leaves the
// events of the request alone (one change, one version).
func (s *server) historyHook(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.History == nil || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		org, orgWide := s.org(r), isOrgWide(r.URL.Path)
		// the first change after the history started can be undone: keep how it was before it
		if id := agentInPath(r.URL.Path); id != "" {
			s.History.EnsureBaseline(r.Context(), org, id)
		} else if orgWide {
			s.History.EnsureBaselineAll(r.Context(), org)
		}
		ctx, done := agenthistory.WithHook(r.Context())
		var pending map[string]agenthistory.Cause
		closed := false
		closeHook := func() {
			if !closed {
				pending, closed = done(), true
			}
		}
		defer closeHook() // a panicking handler still closes it: later background events are recorded by the observer
		r = r.WithContext(ctx)
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		closeHook()

		pattern := ""
		if rc := chi.RouteContext(r.Context()); rc != nil {
			pattern = rc.RoutePattern()
		}
		rec := context.WithoutCancel(ctx)
		name, named := "", false
		withName := func(c agenthistory.Cause) agenthistory.Cause {
			if c.ActorName == "" && !agenthistory.IsMachine(c.Actor) && c.Actor != "user" {
				if !named {
					name, named = s.authorName(ctx), true
				}
				c.ActorName = name
			}
			return c
		}
		record := func(id string, c agenthistory.Cause) {
			if _, _, err := s.History.Record(rec, org, id, withName(c)); err != nil && !errors.Is(err, domain.ErrNotFound) {
				s.Log.Warn("agent history: record", "agent", id, "route", pattern, "err", err)
			}
		}
		person := agenthistory.Cause{Actor: s.actor(r), Source: r.Method + " " + strings.TrimPrefix(pattern, "/api/v1")}
		routeAgent := ""
		if sw.status >= 200 && sw.status <= 299 {
			routeAgent = agentRouteID(r, pattern)
		}
		if routeAgent != "" {
			c := person
			if p, ok := pending[routeAgent]; ok && p.Actor == agenthistory.ActorSystem {
				c = p // e.g. the ladder demoted the agent on its own while the person asked to evaluate it
			}
			record(routeAgent, c)
		}
		// the agents the events of this request named (a hire, the ladder): with the cause of their event
		for id, c := range pending {
			if id != routeAgent {
				record(id, c)
			}
		}
		if routeAgent == "" && orgWide && sw.status >= 200 && sw.status <= 299 {
			s.History.RecordAll(rec, org, withName(person))
		}
	})
}

// ---- handlers ----

// versionSummary is one line of the timeline (the snapshot comes with the detail).
type versionSummary struct {
	Version      int       `json:"version"`
	CreatedAt    time.Time `json:"created_at"`
	Actor        string    `json:"actor"`
	ActorKind    string    `json:"actor_kind"`
	ActorName    string    `json:"actor_name,omitempty"`
	Source       string    `json:"source"`
	Fields       []string  `json:"fields"`
	RestoredFrom *int      `json:"restored_from,omitempty"`
}

func (s *server) listAgentVersions(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = n
	}
	vs, err := s.History.List(r.Context(), s.org(r), chi.URLParam(r, "id"), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]versionSummary, 0, len(vs))
	for _, v := range vs {
		f := v.Fields
		if f == nil {
			f = []string{}
		}
		out = append(out, versionSummary{Version: v.Version, CreatedAt: v.CreatedAt, Actor: v.Actor, ActorKind: v.ActorKind,
			ActorName: v.ActorName, Source: v.Source, Fields: f, RestoredFrom: v.RestoredFrom})
	}
	current := 0
	if len(out) > 0 {
		current = out[0].Version
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent_id": chi.URLParam(r, "id"), "current": current, "versions": out,
		"keep": agenthistory.DefaultKeep, "can_restore": s.canRestore(r)})
}

func versionParam(r *http.Request) (int, error) {
	n, err := strconv.Atoi(chi.URLParam(r, "v"))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%w: version must be a positive number", domain.ErrInvalid)
	}
	return n, nil
}

func (s *server) getAgentVersion(w http.ResponseWriter, r *http.Request) {
	n, err := versionParam(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	d, err := s.History.Detail(r.Context(), s.org(r), chi.URLParam(r, "id"), n)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) canRestore(r *http.Request) bool {
	if !s.AuthEnabled {
		return true
	}
	p, ok := auth.PrincipalFrom(r.Context())
	return ok && p.Can(auth.PermAgentsWrite)
}

func (s *server) previewAgentRestore(w http.ResponseWriter, r *http.Request) {
	n, err := versionParam(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	p, err := s.History.Preview(r.Context(), s.org(r), id, n, s.planEnv(r, id))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": n, "apply": p.Apply, "skip": p.Skip, "can_restore": s.canRestore(r)})
}

func (s *server) restoreAgentVersion(w http.ResponseWriter, r *http.Request) {
	n, err := versionParam(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	cause := agenthistory.Cause{Actor: s.actor(r)}
	if !agenthistory.IsMachine(cause.Actor) && cause.Actor != "user" {
		cause.ActorName = s.authorName(r.Context())
	}
	res, err := s.History.Restore(r.Context(), s.org(r), id, n, cause, historyExec{s: s, r: r}, s.planEnv(r, id))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// planEnv are the facts of the plan: which skills still work and the limit above the agent's cap.
func (s *server) planEnv(r *http.Request, agentID string) agenthistory.PlanEnv {
	env := agenthistory.PlanEnv{}
	if s.Skills != nil {
		actor := s.skillActor(r)
		env.SkillUsable = func(id string) bool {
			d, err := s.Skills.Get(r.Context(), actor, id, 0)
			// the same rule as what agents receive: not deleted, an approved version, not disabled
			return err == nil && d.DeletedAt == nil && d.ActiveVersion > 0 && (d.Status == skills.StatusActive || d.Status == skills.StatusInReview)
		}
	}
	if s.Orch != nil && s.Orch.Budget() != nil {
		env.BudgetCeiling = s.Orch.Budget().AgentCeiling(r.Context(), agentID)
	}
	return env
}

// ---- the sources: each part of the configuration from the service that owns it ----

func (s *server) toneStore() application.ConfigStore {
	switch {
	case s.Profiles != nil && s.Profiles.Tones != nil:
		return s.Profiles.Tones
	case s.OrgConfig != nil && s.OrgConfig.Store != nil:
		return s.OrgConfig.Store
	case s.Permissions != nil && s.Permissions.Store != nil:
		return s.Permissions.Store
	}
	return nil
}

func (s *server) historySources() []agenthistory.Source {
	src := []agenthistory.Source{func(ctx context.Context, org, id string, c *agenthistory.Config) error {
		a, err := s.Store.GetAgent(ctx, org, id)
		if err != nil {
			return err
		}
		c.Name, c.Title, c.Role, c.Autonomy, c.Tools = a.Name, a.Title, a.Role, a.Autonomy, a.Tools
		if a.Department != nil {
			c.Department = *a.Department
		}
		if s.OrgChart == nil {
			c.Job = &agenthistory.Job{Responsibilities: a.Responsibilities}
		}
		return nil
	}}
	if s.Profiles != nil {
		src = append(src, func(ctx context.Context, org, id string, c *agenthistory.Config) error {
			p, ok, err := s.Profiles.Store.GetAgentProfile(ctx, org, id)
			if err != nil {
				return err
			}
			if ok {
				c.Instructions, c.Creativity, c.Tags = p.Instructions, p.Creativity, p.Skills
			}
			return nil
		})
	}
	if ts := s.toneStore(); ts != nil {
		src = append(src, func(ctx context.Context, org, id string, c *agenthistory.Config) error {
			m, err := ts.ListAgentTones(ctx, org)
			c.Tone = m[id]
			return err
		})
	}
	if s.Permissions != nil {
		src = append(src, func(ctx context.Context, org, id string, c *agenthistory.Config) error {
			st, err := s.Permissions.Store.GetOrgSettings(ctx, org)
			if err != nil {
				return err
			}
			ap, ok := st.Rules.Permissions.For(id)
			if !ok {
				return nil
			}
			p := &agenthistory.Permissions{}
			for _, e := range ap.Exceptions {
				p.Exceptions = append(p.Exceptions, agenthistory.Exception{Category: e.Category, Mode: e.Mode, If: e.If})
			}
			for _, l := range ap.FloorLifts {
				p.FloorLifted = append(p.FloorLifted, l.Category)
			}
			c.Permissions = p
			return nil
		})
	}
	if s.Skills != nil {
		src = append(src, func(ctx context.Context, org, id string, c *agenthistory.Config) error {
			l, err := s.Skills.AgentSkills(application.WithOrg(ctx, org), id)
			if err != nil {
				return err
			}
			for _, sk := range l {
				if sk.Via == skills.ViaAgent {
					c.Skills = append(c.Skills, agenthistory.SkillRef{ID: sk.ID, Name: sk.Name})
				}
			}
			return nil
		})
	}
	src = append(src, func(ctx context.Context, org, id string, c *agenthistory.Config) error {
		caps, err := s.Store.ListBudgetCaps(ctx, org)
		for _, cp := range caps {
			if cp.Scope == domain.ScopeAgent && cp.ScopeID == id {
				c.BudgetUSD = cp.CapUSD
			}
		}
		return err
	})
	if s.Controls != nil {
		src = append(src, func(ctx context.Context, org, id string, c *agenthistory.Config) error {
			ac, err := s.Controls.Agent(ctx, org, id)
			c.Paused, c.ReadOnly = ac.Control != "" && ac.Control != "active", ac.ReadOnly
			return err
		})
	}
	if s.Rehearsal != nil {
		src = append(src, func(ctx context.Context, org, id string, c *agenthistory.Config) error {
			on, err := s.Rehearsal.AgentEnabled(ctx, org, id)
			c.Rehearsal = on
			return err
		})
	}
	if s.OrgChart != nil {
		src = append(src, func(ctx context.Context, org, id string, c *agenthistory.Config) error {
			j, err := s.OrgChart.Job(application.WithOrg(ctx, org), id)
			if err != nil {
				return err
			}
			sched, _ := json.Marshal(j.Schedule.Normalize())
			c.Job = &agenthistory.Job{Responsibilities: j.Responsibilities, ReportsTo: refKey(j.ReportsTo), Escalation: refKey(j.Escalation), Schedule: sched}
			return nil
		})
	}
	if s.Offices != nil {
		src = append(src, func(ctx context.Context, org, id string, c *agenthistory.Config) error {
			m, err := s.Offices.Model(ctx, org)
			if err != nil || m == nil {
				return err
			}
			ea := m.Agent(id)
			a := &agenthistory.Access{Owner: ea.OwnerKind + ":" + ea.OwnerID, Visibility: ea.Visibility}
			for _, sh := range ea.Shares {
				a.Shares = append(a.Shares, sh.UserID+":"+sh.Level)
			}
			c.Access = a
			return nil
		})
	}
	if s.Drive != nil {
		src = append(src, func(ctx context.Context, org, id string, c *agenthistory.Config) error {
			es, err := s.agentFolders().AgentAccess(application.WithOrg(ctx, org), id)
			if err != nil {
				return nil // the folders are shown when they can be read; they never block the history
			}
			for _, e := range es {
				if e.Source == "manual" {
					c.Folders = append(c.Folders, agenthistory.FolderGrant{Path: e.Path, Level: e.Level})
				}
			}
			return nil
		})
	}
	return src
}

func refKey(r orgchart.Ref) string {
	if r.IsZero() {
		return ""
	}
	return r.Key()
}

func parseRef(k string) orgchart.Ref {
	kind, id, ok := strings.Cut(k, ":")
	if !ok {
		return orgchart.Ref{}
	}
	return orgchart.Ref{Kind: orgchart.Kind(kind), ID: id}
}

// ---- the executor: each field through the service (and the permission) that owns it ----

type historyExec struct {
	s *server
	r *http.Request
}

func (e historyExec) ctx() context.Context { return e.r.Context() }

// may reports whether the person holds perm (always true without accounts, like the routes).
func (e historyExec) may(perm auth.Permission) bool {
	if !e.s.AuthEnabled {
		return true
	}
	p, ok := auth.PrincipalFrom(e.ctx())
	return ok && p.Can(perm)
}

func forbidden(what string) error {
	return fmt.Errorf("%w: you may not change %s", domain.ErrForbidden, what)
}

func (e historyExec) SetProfile(_ context.Context, id string, c agenthistory.Config) error {
	if !e.may(auth.PermAgentsWrite) {
		return forbidden("the work profile")
	}
	if e.s.Profiles != nil {
		tone := c.Tone
		_, err := e.s.Profiles.Update(e.ctx(), id, application.ProfileInput{Instructions: c.Instructions, Creativity: c.Creativity, Skills: c.Tags, Tone: &tone})
		return err
	}
	if e.s.OrgConfig != nil {
		return e.s.OrgConfig.SetAgentTone(e.ctx(), id, c.Tone)
	}
	return agenthistory.ErrUnsupported
}

func (e historyExec) LowerAutonomy(_ context.Context, id, to string) error {
	// through the approval permissions (who may choose a level) and the trust ladder (the only writer of autonomy)
	if e.s.Permissions != nil {
		_, err := e.s.Permissions.ChooseLevel(e.ctx(), id, application.PlainLevel(to))
		return err
	}
	if e.s.Trust != nil {
		if !e.may(auth.PermApprovalsDecide) {
			return forbidden("the autonomy")
		}
		return e.s.Trust.ChooseLevel(e.ctx(), id, to, e.s.actor(e.r))
	}
	return agenthistory.ErrUnsupported
}

func (e historyExec) AdoptPermissions(_ context.Context, id string) error {
	if e.s.Permissions == nil {
		return agenthistory.ErrUnsupported
	}
	a, err := e.s.Store.GetAgent(e.ctx(), e.s.org(e.r), id)
	if err != nil {
		return err
	}
	_, err = e.s.Permissions.ChooseLevel(e.ctx(), id, application.PlainLevel(a.Autonomy))
	return err
}

func (e historyExec) SetException(_ context.Context, id string, x agenthistory.Exception) error {
	if e.s.Permissions == nil {
		return agenthistory.ErrUnsupported
	}
	_, err := e.s.Permissions.SetException(e.ctx(), id, x.Category, application.ExceptionInput{Mode: x.Mode, If: x.If, Source: "restore"})
	return err
}

func (e historyExec) RemoveException(_ context.Context, id, cat string) error {
	if e.s.Permissions == nil {
		return agenthistory.ErrUnsupported
	}
	_, err := e.s.Permissions.RemoveException(e.ctx(), id, cat)
	return err
}

func (e historyExec) RestoreFloor(_ context.Context, id, cat string) error {
	if e.s.Permissions == nil {
		return agenthistory.ErrUnsupported
	}
	_, err := e.s.Permissions.RestoreFloor(e.ctx(), id, cat)
	return err
}

func (e historyExec) AssignSkill(_ context.Context, id, skillID string) error {
	if e.s.Skills == nil {
		return agenthistory.ErrUnsupported
	}
	if !e.may(auth.PermAgentsWrite) {
		return forbidden("the skills")
	}
	return e.s.Skills.Assign(e.ctx(), e.s.skillActor(e.r), id, skillID)
}

func (e historyExec) UnassignSkill(_ context.Context, id, skillID string) error {
	if e.s.Skills == nil {
		return agenthistory.ErrUnsupported
	}
	if !e.may(auth.PermAgentsWrite) {
		return forbidden("the skills")
	}
	return e.s.Skills.Unassign(e.ctx(), e.s.skillActor(e.r), id, skillID)
}

func (e historyExec) SetBudget(_ context.Context, id string, usd float64) error {
	if e.s.Orch == nil || e.s.Orch.Budget() == nil {
		return agenthistory.ErrUnsupported
	}
	if !e.may(auth.PermApprovalsDecide) { // the same permission as PUT /agents/{id}/budget
		return forbidden("the spend cap")
	}
	return e.s.Orch.Budget().SetCap(e.ctx(), domain.ScopeAgent, id, usd)
}

func (e historyExec) SetJob(_ context.Context, id string, j agenthistory.Job, tools []string) error {
	if e.s.OrgChart == nil {
		return agenthistory.ErrUnsupported
	}
	if !e.may(auth.PermOrgManage) { // the same permission as PUT /agents/{id}/job
		return forbidden("the job description")
	}
	cur, err := e.s.OrgChart.Job(e.ctx(), id)
	if err != nil {
		return err
	}
	in := orgchart.JobInput{DepartmentID: cur.DepartmentID, ReportsTo: parseRef(j.ReportsTo), Escalation: parseRef(j.Escalation),
		Responsibilities: j.Responsibilities, AllowedTools: tools, Schedule: cur.Schedule, BudgetUSD: cur.BudgetUSD}
	if len(j.Schedule) > 0 {
		var sc orgchart.Schedule
		if json.Unmarshal(j.Schedule, &sc) == nil {
			in.Schedule = sc
		}
	}
	// the department and the cap are not part of the job restore (they have their own rules); keep the current cap
	if caps, err := e.s.Store.ListBudgetCaps(e.ctx(), e.s.org(e.r)); err == nil {
		in.BudgetUSD = nil
		for _, c := range caps {
			if c.Scope == domain.ScopeAgent && c.ScopeID == id && c.CapUSD > 0 {
				v := c.CapUSD
				in.BudgetUSD = &v
			}
		}
	}
	_, err = e.s.OrgChart.PutJob(e.ctx(), id, in)
	return err
}
