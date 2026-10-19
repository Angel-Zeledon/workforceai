package orgchart

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/roles"
)

// AgentSource lists the agents of an organization (the application Store satisfies it).
type AgentSource interface {
	ListAgents(ctx context.Context, orgID string) ([]domain.Agent, error)
}

// AgentWriter changes what the organization model owns of an agent. The postgres and memory stores
// implement it; a store without it makes the write operations answer ErrInvalid.
type AgentWriter interface {
	CreateAgent(ctx context.Context, orgID string, a domain.Agent) error
	SetAgentDepartment(ctx context.Context, orgID, id string, dept *string) error
	SetAgentTools(ctx context.Context, orgID, id string, tools []string) error
	SetAgentResponsibilities(ctx context.Context, orgID, id string, resp []string) error
}

// CapSetter writes the per-agent monthly budget cap the budget guard already enforces.
type CapSetter interface {
	SetBudgetCap(ctx context.Context, orgID string, scope domain.BudgetScope, scopeID string, capUSD float64) error
}

// TemplateCreator creates organization project templates (projects.Service).
type TemplateCreator interface {
	CreateTemplate(ctx context.Context, in projects.TemplateInput) (projects.Template, error)
}

// PeopleFunc lists the members of an organization (auth). nil = only the owner.
type PeopleFunc func(ctx context.Context, org string) ([]Person, error)

// MaxAgents mirrors the per-organization cap of the office (api/roles.go).
const MaxAgents = 20

// Service is the organization model.
type Service struct {
	Store      Store
	Agents     AgentSource
	Writer     AgentWriter
	Caps       CapSetter
	People     PeopleFunc
	Templates  TemplateCreator
	Audit      func(ctx context.Context, a domain.AuditLog)
	DefaultOrg string
	Now        func() time.Time
	// OnChange (optional) is called after the model changed in a way that can release held work (a
	// schedule or a rule was edited): the orchestrator asks its gates again at once (application.WakeHeld).
	OnChange func()

	mu    sync.Mutex
	cache map[string]jobCache
	snaps map[string]snapshot // delegation lookups (enforce.go)
}

type jobCache struct {
	at   time.Time
	jobs map[string]Job
}

const jobCacheTTL = 15 * time.Second

func (s *Service) org(ctx context.Context) string { return application.OrgFrom(ctx, s.DefaultOrg) }

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) actor(ctx context.Context) string { return application.ActorFrom(ctx, "user") }

func (s *Service) audit(ctx context.Context, action, entity, id string, d map[string]any) {
	if s.Audit != nil {
		s.Audit(ctx, domain.AuditLog{Actor: s.actor(ctx), Action: action, Entity: entity, EntityID: id, Details: d})
	}
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalid, fmt.Sprintf(format, a...))
}

func (s *Service) writer() (AgentWriter, error) {
	if s.Writer == nil {
		return nil, invalid("this store cannot change agents")
	}
	return s.Writer, nil
}

func (s *Service) invalidate(org string) {
	s.mu.Lock()
	delete(s.cache, org)
	delete(s.snaps, org)
	s.mu.Unlock()
	if s.OnChange != nil {
		s.OnChange()
	}
}

// ---- people ----

// People returns the members as the chart shows them; the owner alias always resolves to someone.
func (s *Service) people(ctx context.Context) []Person {
	var out []Person
	if s.People != nil {
		if ps, err := s.People(ctx, s.org(ctx)); err == nil {
			out = ps
		}
	}
	if len(out) == 0 {
		out = []Person{{UserID: OwnerID, Name: "Owner", Role: "owner"}}
	}
	return out
}

func (s *Service) personExists(ctx context.Context, id string) bool {
	if id == OwnerID {
		return true
	}
	for _, p := range s.people(ctx) {
		if p.UserID == id {
			return true
		}
	}
	return false
}

// checkRef validates a reference to an existing person or agent.
func (s *Service) checkRef(ctx context.Context, r Ref, what string, agents map[string]domain.Agent) error {
	if r.IsZero() {
		return nil
	}
	if err := validateRef(r, what); err != nil {
		return invalid("%v", err)
	}
	switch r.Kind {
	case KindAgent:
		if _, ok := agents[r.ID]; !ok {
			return invalid("%s: unknown agent %q", what, r.ID)
		}
	case KindPerson:
		if !s.personExists(ctx, r.ID) {
			return invalid("%s: unknown person %q", what, r.ID)
		}
	}
	return nil
}

func (s *Service) agentMap(ctx context.Context) (map[string]domain.Agent, []domain.Agent, error) {
	list, err := s.Agents.ListAgents(ctx, s.org(ctx))
	if err != nil {
		return nil, nil, err
	}
	m := make(map[string]domain.Agent, len(list))
	for _, a := range list {
		m[a.ID] = a
	}
	return m, list, nil
}

// ---- departments ----

// DepartmentInput is the body to create or replace a department.
type DepartmentInput struct {
	Name       string `json:"name"`
	ParentID   string `json:"parent_id"`
	Head       Ref    `json:"head"`
	CostCenter string `json:"cost_center"`
}

func (s *Service) Departments(ctx context.Context) ([]Department, error) {
	d, err := s.Store.ListDepartments(ctx, s.org(ctx))
	if d == nil {
		d = []Department{}
	}
	return d, err
}

func normalizeDept(in DepartmentInput) DepartmentInput {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	in.ParentID = strings.TrimSpace(in.ParentID)
	in.CostCenter = strings.TrimSpace(in.CostCenter)
	if in.Head.Kind == "" || in.Head.ID == "" {
		in.Head = Ref{}
	}
	return in
}

// CreateDepartment adds a department.
func (s *Service) CreateDepartment(ctx context.Context, in DepartmentInput) (Department, error) {
	return s.putDepartment(ctx, "dept_"+strings.ReplaceAll(uuid.NewString(), "-", "")[:12], normalizeDept(in), true)
}

// UpdateDepartment replaces a department; a rename updates the tag of its agents.
func (s *Service) UpdateDepartment(ctx context.Context, id string, in DepartmentInput) (Department, error) {
	return s.putDepartment(ctx, id, normalizeDept(in), false)
}

func (s *Service) putDepartment(ctx context.Context, id string, in DepartmentInput, create bool) (Department, error) {
	org := s.org(ctx)
	all, err := s.Store.ListDepartments(ctx, org)
	if err != nil {
		return Department{}, err
	}
	var cur *Department
	rest := make([]Department, 0, len(all)+1)
	for i := range all {
		if all[i].ID == id {
			cur = &all[i]
			continue
		}
		rest = append(rest, all[i])
	}
	if create && cur != nil {
		return Department{}, fmt.Errorf("%w: department %s already exists", domain.ErrConflict, id)
	}
	if !create && cur == nil {
		return Department{}, fmt.Errorf("%w: department %s", domain.ErrNotFound, id)
	}
	now := s.now()
	d := Department{ID: id, Name: in.Name, ParentID: in.ParentID, Head: in.Head, CostCenter: in.CostCenter, CreatedAt: now, UpdatedAt: now}
	if cur != nil {
		d.CreatedAt = cur.CreatedAt
	}
	if err := ValidateDepartments(append(rest, d)); err != nil {
		return Department{}, invalid("%v", err)
	}
	agents, _, err := s.agentMap(ctx)
	if err != nil {
		return Department{}, err
	}
	if err := s.checkRef(ctx, d.Head, "head", agents); err != nil {
		return Department{}, err
	}
	if err := s.Store.PutDepartment(ctx, org, d); err != nil {
		return Department{}, err
	}
	s.invalidate(org)
	if cur != nil && cur.Name != d.Name {
		if err := s.retag(ctx, id, &d.Name); err != nil {
			return Department{}, err
		}
	}
	action := "department.created"
	if !create {
		action = "department.updated"
	}
	s.audit(ctx, action, "department", id, map[string]any{"name": d.Name, "parent_id": d.ParentID, "cost_center": d.CostCenter})
	return d, nil
}

// retag writes name (nil clears) to the department tag of every agent whose job is in department id.
func (s *Service) retag(ctx context.Context, id string, name *string) error {
	if s.Writer == nil {
		return nil
	}
	jobs, err := s.Store.ListJobs(ctx, s.org(ctx))
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.DepartmentID != id {
			continue
		}
		if err := s.Writer.SetAgentDepartment(ctx, s.org(ctx), j.AgentID, name); err != nil && !isNotFound(err) {
			return err
		}
	}
	return nil
}

func isNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }

// DeleteDepartment removes a department without sub-departments; its members become unassigned.
func (s *Service) DeleteDepartment(ctx context.Context, id string) error {
	org := s.org(ctx)
	all, err := s.Store.ListDepartments(ctx, org)
	if err != nil {
		return err
	}
	found := false
	for _, d := range all {
		if d.ID == id {
			found = true
		}
		if d.ParentID == id {
			return fmt.Errorf("%w: the department has sub-departments; move or delete them first", domain.ErrConflict)
		}
	}
	if !found {
		return fmt.Errorf("%w: department %s", domain.ErrNotFound, id)
	}
	if err := s.retag(ctx, id, nil); err != nil {
		return err
	}
	jobs, err := s.Store.ListJobs(ctx, org)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.DepartmentID == id {
			j.DepartmentID, j.UpdatedAt = "", s.now()
			if err := s.Store.PutJob(ctx, org, j); err != nil {
				return err
			}
		}
	}
	places, err := s.Store.ListPlacements(ctx, org)
	if err != nil {
		return err
	}
	for _, p := range places {
		if p.DepartmentID == id {
			p.DepartmentID, p.UpdatedAt = "", s.now()
			if err := s.Store.PutPlacement(ctx, org, p); err != nil {
				return err
			}
		}
	}
	s.invalidate(org)
	if err := s.Store.DeleteDepartment(ctx, org, id); err != nil {
		return err
	}
	s.audit(ctx, "department.deleted", "department", id, nil)
	return nil
}

// ---- job descriptions ----

// JobInput is the body of a job description update (the whole description is replaced).
type JobInput struct {
	DepartmentID     string   `json:"department_id"`
	ReportsTo        Ref      `json:"reports_to"`
	Escalation       Ref      `json:"escalation"`
	Responsibilities []string `json:"responsibilities"`
	AllowedTools     []string `json:"allowed_tools"`
	Schedule         Schedule `json:"schedule"`
	BudgetUSD        *float64 `json:"budget_usd"`
}

// Limits of a job description.
const (
	MaxResponsibilities = 20
	MaxResponsibility   = 300
	MaxBudgetUSD        = 1_000_000
)

func cleanLines(in []string, max, maxRunes int) ([]string, error) {
	out := []string{}
	for _, l := range in {
		l = strings.Join(strings.Fields(l), " ")
		if l == "" {
			continue
		}
		if utf8.RuneCountInString(l) > maxRunes {
			return nil, invalid("each responsibility must have at most %d characters", maxRunes)
		}
		out = append(out, l)
	}
	if len(out) > max {
		return nil, invalid("at most %d responsibilities", max)
	}
	return out, nil
}

// RoleTools returns the tools an agent's role template allows (the ceiling of its job description);
// without a template, the tools the agent has now.
func RoleTools(a domain.Agent) []string {
	if t, ok := roles.Get(a.Role); ok && len(t.Tools) > 0 {
		return slices.Clone(t.Tools)
	}
	return slices.Clone(a.Tools)
}

// Job returns the job description of an agent; without a saved one, a default derived from the agent.
func (s *Service) Job(ctx context.Context, agentID string) (Job, error) {
	agents, _, err := s.agentMap(ctx)
	if err != nil {
		return Job{}, err
	}
	a, ok := agents[agentID]
	if !ok {
		return Job{}, fmt.Errorf("%w: agent %s", domain.ErrNotFound, agentID)
	}
	jobs, err := s.Store.ListJobs(ctx, s.org(ctx))
	if err != nil {
		return Job{}, err
	}
	for _, j := range jobs {
		if j.AgentID == agentID {
			return j, nil
		}
	}
	return defaultJob(a), nil
}

func defaultJob(a domain.Agent) Job {
	return Job{AgentID: a.ID, Responsibilities: slices.Clone(a.Responsibilities), AllowedTools: slices.Clone(a.Tools),
		Schedule: Schedule{Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "18:00", Timezone: "UTC"}}
}

// Jobs returns the saved job descriptions.
func (s *Service) Jobs(ctx context.Context) ([]Job, error) {
	j, err := s.Store.ListJobs(ctx, s.org(ctx))
	if j == nil {
		j = []Job{}
	}
	return j, err
}

// PutJob validates and saves the job description of an agent, and applies what the rest of the
// system already enforces: the department tag, the narrowed tools, the responsibilities the planner
// reads and the monthly budget cap.
func (s *Service) PutJob(ctx context.Context, agentID string, in JobInput) (Job, error) {
	org := s.org(ctx)
	w, err := s.writer()
	if err != nil {
		return Job{}, err
	}
	agents, _, err := s.agentMap(ctx)
	if err != nil {
		return Job{}, err
	}
	a, ok := agents[agentID]
	if !ok {
		return Job{}, fmt.Errorf("%w: agent %s", domain.ErrNotFound, agentID)
	}
	deps, err := s.Store.ListDepartments(ctx, org)
	if err != nil {
		return Job{}, err
	}
	var dept *Department
	if in.DepartmentID != "" {
		for i := range deps {
			if deps[i].ID == in.DepartmentID {
				dept = &deps[i]
			}
		}
		if dept == nil {
			return Job{}, invalid("unknown department %q", in.DepartmentID)
		}
	}
	resp, err := cleanLines(in.Responsibilities, MaxResponsibilities, MaxResponsibility)
	if err != nil {
		return Job{}, err
	}
	ceiling := RoleTools(a)
	tools := []string{}
	for _, t := range in.AllowedTools {
		if !roles.KnownTools[t] {
			return Job{}, invalid("tool %q is not registered", t)
		}
		if !slices.Contains(ceiling, t) {
			return Job{}, invalid("tool %q is not one of the tools of the role (a job description can only narrow them)", t)
		}
		if !slices.Contains(tools, t) {
			tools = append(tools, t)
		}
	}
	sched := in.Schedule.Normalize()
	if err := sched.Validate(); err != nil {
		return Job{}, invalid("schedule: %v", err)
	}
	if in.BudgetUSD != nil && (*in.BudgetUSD < 0 || *in.BudgetUSD > MaxBudgetUSD) {
		return Job{}, invalid("budget must be between 0 and %d", MaxBudgetUSD)
	}
	self := Ref{Kind: KindAgent, ID: agentID}
	for _, r := range []struct {
		ref  Ref
		what string
	}{{in.ReportsTo, "reports_to"}, {in.Escalation, "escalation"}} {
		if r.ref == self {
			return Job{}, invalid("%s cannot be the agent itself", r.what)
		}
		if err := s.checkRef(ctx, r.ref, r.what, agents); err != nil {
			return Job{}, err
		}
	}
	if c := s.reportingCycleWith(ctx, agentID, in.ReportsTo); c != nil {
		return Job{}, invalid("the reporting lines form a cycle: %s", strings.Join(c, " -> "))
	}
	j := Job{AgentID: agentID, DepartmentID: in.DepartmentID, ReportsTo: in.ReportsTo, Escalation: in.Escalation, Responsibilities: resp,
		AllowedTools: tools, Schedule: sched, BudgetUSD: in.BudgetUSD, UpdatedBy: s.actor(ctx), UpdatedAt: s.now()}
	if err := s.Store.PutJob(ctx, org, j); err != nil {
		return Job{}, err
	}
	s.invalidate(org)
	var tag *string
	if dept != nil {
		n := dept.Name
		tag = &n
	}
	if err := w.SetAgentDepartment(ctx, org, agentID, tag); err != nil {
		return Job{}, err
	}
	if !slices.Equal(tools, a.Tools) {
		if err := w.SetAgentTools(ctx, org, agentID, tools); err != nil {
			return Job{}, err
		}
	}
	if len(resp) > 0 && !slices.Equal(resp, a.Responsibilities) {
		if err := w.SetAgentResponsibilities(ctx, org, agentID, resp); err != nil {
			return Job{}, err
		}
	}
	if s.Caps != nil {
		capUSD := 0.0 // <= 0 removes the cap: the organization default applies
		if j.BudgetUSD != nil {
			capUSD = *j.BudgetUSD
		}
		if err := s.Caps.SetBudgetCap(ctx, org, domain.ScopeAgent, agentID, capUSD); err != nil {
			return Job{}, err
		}
	}
	s.audit(ctx, "agent.job_updated", "agent", agentID, map[string]any{"department_id": j.DepartmentID, "tools": tools,
		"schedule_enabled": sched.Enabled, "budget_usd": j.BudgetUSD, "reports_to": in.ReportsTo.Key(), "escalation": in.Escalation.Key()})
	return j, nil
}

// reportingCycleWith checks every reporting line (agents and people) with the new boss of agentID.
func (s *Service) reportingCycleWith(ctx context.Context, agentID string, boss Ref) []string {
	org := s.org(ctx)
	edges := map[string]string{}
	if jobs, err := s.Store.ListJobs(ctx, org); err == nil {
		for _, j := range jobs {
			if !j.ReportsTo.IsZero() {
				edges[Ref{Kind: KindAgent, ID: j.AgentID}.Key()] = j.ReportsTo.Key()
			}
		}
	}
	if places, err := s.Store.ListPlacements(ctx, org); err == nil {
		for _, p := range places {
			if !p.ReportsTo.IsZero() {
				edges[Ref{Kind: KindPerson, ID: p.UserID}.Key()] = p.ReportsTo.Key()
			}
		}
	}
	return s.edgesCycle(edges, Ref{Kind: KindAgent, ID: agentID}, boss)
}

func (s *Service) edgesCycle(edges map[string]string, who, boss Ref) []string {
	if boss.IsZero() {
		delete(edges, who.Key())
	} else {
		edges[who.Key()] = boss.Key()
	}
	return ReportingCycle(edges)
}

// ---- people placement ----

// PlacementInput is the body that places a person in the chart.
type PlacementInput struct {
	DepartmentID string `json:"department_id"`
	Title        string `json:"title"`
	ReportsTo    Ref    `json:"reports_to"`
}

// PutPlacement places a person (a member) in a department with a title and a boss.
func (s *Service) PutPlacement(ctx context.Context, userID string, in PlacementInput) (Placement, error) {
	org := s.org(ctx)
	if userID != OwnerID && !s.personExists(ctx, userID) {
		return Placement{}, fmt.Errorf("%w: person %s", domain.ErrNotFound, userID)
	}
	title := strings.Join(strings.Fields(in.Title), " ")
	if utf8.RuneCountInString(title) > 80 {
		return Placement{}, invalid("title is too long (max 80)")
	}
	if in.DepartmentID != "" {
		deps, err := s.Store.ListDepartments(ctx, org)
		if err != nil {
			return Placement{}, err
		}
		if !slices.ContainsFunc(deps, func(d Department) bool { return d.ID == in.DepartmentID }) {
			return Placement{}, invalid("unknown department %q", in.DepartmentID)
		}
	}
	agents, _, err := s.agentMap(ctx)
	if err != nil {
		return Placement{}, err
	}
	self := Ref{Kind: KindPerson, ID: userID}
	if in.ReportsTo == self {
		return Placement{}, invalid("a person cannot report to themselves")
	}
	if err := s.checkRef(ctx, in.ReportsTo, "reports_to", agents); err != nil {
		return Placement{}, err
	}
	edges := map[string]string{}
	if jobs, err := s.Store.ListJobs(ctx, org); err == nil {
		for _, j := range jobs {
			if !j.ReportsTo.IsZero() {
				edges[Ref{Kind: KindAgent, ID: j.AgentID}.Key()] = j.ReportsTo.Key()
			}
		}
	}
	if places, err := s.Store.ListPlacements(ctx, org); err == nil {
		for _, p := range places {
			if !p.ReportsTo.IsZero() {
				edges[Ref{Kind: KindPerson, ID: p.UserID}.Key()] = p.ReportsTo.Key()
			}
		}
	}
	if c := s.edgesCycle(edges, self, in.ReportsTo); c != nil {
		return Placement{}, invalid("the reporting lines form a cycle: %s", strings.Join(c, " -> "))
	}
	p := Placement{UserID: userID, DepartmentID: in.DepartmentID, Title: title, ReportsTo: in.ReportsTo, UpdatedAt: s.now()}
	if err := s.Store.PutPlacement(ctx, org, p); err != nil {
		return Placement{}, err
	}
	s.invalidate(org)
	s.audit(ctx, "person.placed", "person", userID, map[string]any{"department_id": p.DepartmentID, "title": title, "reports_to": in.ReportsTo.Key()})
	return p, nil
}

// ---- availability and the task gate ----

// Availability tells whether an agent may act now and, if not, when its next working window opens.
func (s *Service) Availability(ctx context.Context, agentID string, at time.Time) (Availability, error) {
	j, err := s.Job(ctx, agentID)
	if err != nil {
		return Availability{}, err
	}
	return j.Schedule.AvailabilityAt(at), nil
}

func (s *Service) jobFor(ctx context.Context, org, agentID string) (Job, bool) {
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]jobCache{}
	}
	c, ok := s.cache[org]
	s.mu.Unlock()
	if !ok || s.now().Sub(c.at) > jobCacheTTL {
		jobs, err := s.Store.ListJobs(ctx, org)
		if err != nil {
			return Job{}, false // cannot read: do not hold work back
		}
		c = jobCache{at: s.now(), jobs: make(map[string]Job, len(jobs))}
		for _, j := range jobs {
			c.jobs[j.AgentID] = j
		}
		s.mu.Lock()
		s.cache[org] = c
		s.mu.Unlock()
	}
	j, ok := c.jobs[agentID]
	return j, ok
}

// TaskGate wraps the projects gate so that work for an agent outside its working hours is held in
// the queue (GateHold: the task waits and is asked again) instead of acting. Everything else is the
// inner gate's decision, and an agent without a schedule is never held.
type TaskGate struct {
	Inner application.TaskGate
	Svc   *Service
}

var (
	_ application.TaskGate     = TaskGate{}
	_ application.ReviewPolicy = TaskGate{}
	_ application.RequestOwner = TaskGate{}
)

func (g TaskGate) GateTask(ctx context.Context, org string, t domain.Task) application.GateDecision {
	d := application.GateDecision{Action: application.GateRun}
	if g.Inner != nil {
		d = g.Inner.GateTask(ctx, org, t)
	}
	if d.Action != application.GateRun || g.Svc == nil {
		return d
	}
	j, ok := g.Svc.jobFor(ctx, org, t.AgentID)
	if !ok || !j.Schedule.Enabled {
		return d
	}
	if av := j.Schedule.AvailabilityAt(g.Svc.now()); !av.Open {
		reason := "outside working hours"
		if av.NextOpen != nil {
			reason += " (next window " + av.NextOpen.Format(time.RFC3339) + ")"
		}
		d := application.GateDecision{Action: application.GateHold, Reason: reason}
		if av.NextOpen != nil {
			d.Until = *av.NextOpen
		}
		return d
	}
	return d
}

func (g TaskGate) GateResolved(ctx context.Context, org string, t domain.Task, approvalID string, approved bool) {
	if g.Inner != nil {
		g.Inner.GateResolved(ctx, org, t, approvalID, approved)
	}
}

func (g TaskGate) ReviewSpec(ctx context.Context, org string, t domain.Task) application.ReviewSpec {
	if rp, ok := g.Inner.(application.ReviewPolicy); ok {
		return rp.ReviewSpec(ctx, org, t)
	}
	return application.ReviewSpec{}
}

func (g TaskGate) OwnsRequest(ctx context.Context, org, requestID string) bool {
	if ro, ok := g.Inner.(application.RequestOwner); ok {
		return ro.OwnsRequest(ctx, org, requestID)
	}
	return false
}

func (g TaskGate) ResumeRequest(ctx context.Context, org, requestID string) bool {
	if ro, ok := g.Inner.(application.RequestOwner); ok {
		return ro.ResumeRequest(ctx, org, requestID)
	}
	return false
}
