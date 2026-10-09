package projects

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
)

// NodeEvent tells an optional Sink (the workspace of artifacts) that a leaf
// node changed state or progress, with the task output once it finished.
type NodeEvent struct {
	ProjectID, ProjectName string
	NodeID, ObjectiveID    string
	Kind, Title            string
	AgentID                string
	State                  string
	Progress               int
	TaskID, RequestID      string
	Output                 *domain.StructuredOutput
}

// Sink receives node changes of launched projects.
type Sink interface {
	NodeChanged(ctx context.Context, ev NodeEvent)
}

// Config wires the project service to the rest of the backend.
type Config struct {
	Store     Store
	Orch      *application.Orchestrator
	Core      application.Store
	Approvals *application.Approvals
	Rec       *application.Recorder
	// Runtime is the optional planner of free-text projects (nil: the generic template).
	Runtime application.Runtime
	// Guard is the optional execution guard (kill switch, agent pause): it only
	// colors the node states ("paused"); enforcement stays in the orchestrator.
	Guard   application.ExecutionGuard
	Catalog *catalog.Catalog
	Sink    Sink
	OrgID   string
	// LocaleFor resolves the project language (org setting); default "es".
	LocaleFor func(ctx context.Context) string
	Poll      time.Duration // monitor period while the project changes (default 250 ms)
	// PollIdleMax is the longest monitor period while nothing changes (default 2 s).
	PollIdleMax time.Duration
	// BudgetWarnPct is the early-warning threshold as a fraction of the project
	// budget (default 0.8) for projects with the default budget policy.
	BudgetWarnPct float64
	MaxAttempts   int // shown as max_attempts of a node (default 3)
	Log           *slog.Logger
	Now           func() time.Time
}

// Service is the projects use case layer.
type Service struct {
	cfg  Config
	root context.Context

	mu    sync.Mutex
	live  map[string]*liveProject // org|project id
	byReq map[string]*liveProject // org|request id
	locks map[string]*sync.Mutex  // org|project id

	// launching counts the projects being submitted and registered: the gate of
	// a task that starts before its registration finished waits for it.
	launching atomic.Int32
	// recovered: Recover ran, so a launched project without a live entry is
	// adopted (its request was settled by the orchestrator) instead of failed.
	recovered atomic.Bool
}

// New builds the service and installs it as the orchestrator's task gate.
func New(ctx context.Context, cfg Config) *Service {
	if cfg.Poll <= 0 {
		cfg.Poll = 250 * time.Millisecond
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.Catalog == nil {
		cfg.Catalog = catalog.Default()
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	s := &Service{cfg: cfg, root: ctx, live: map[string]*liveProject{}, byReq: map[string]*liveProject{}, locks: map[string]*sync.Mutex{}}
	if cfg.Orch != nil {
		cfg.Orch.SetTaskGate(s)
	}
	return s
}

func (s *Service) org(ctx context.Context) string { return application.OrgFrom(ctx, s.cfg.OrgID) }

func (s *Service) locale(ctx context.Context, explicit string) string {
	if explicit != "" {
		return catalog.NormalizeLocale(explicit)
	}
	if s.cfg.LocaleFor != nil {
		return catalog.NormalizeLocale(s.cfg.LocaleFor(ctx))
	}
	return "es"
}

func (s *Service) liveOf(org, id string) *liveProject {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live[org+"|"+id]
}

func (s *Service) lockFor(org, id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.locks[org+"|"+id]
	if !ok {
		m = &sync.Mutex{}
		s.locks[org+"|"+id] = m
	}
	return m
}

// update runs fn on the stored record under the project lock and saves it.
func (s *Service) update(ctx context.Context, id string, fn func(r *Record) error) (Record, error) {
	org := s.org(ctx)
	m := s.lockFor(org, id)
	m.Lock()
	defer m.Unlock()
	rec, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return rec, err
	}
	if err := fn(&rec); err != nil {
		return rec, err
	}
	return rec, s.cfg.Store.Put(ctx, rec)
}

func (s *Service) audit(ctx context.Context, action, id string, details map[string]any) {
	if s.cfg.Rec == nil {
		return
	}
	s.cfg.Rec.Audit(ctx, domain.AuditLog{Actor: application.ActorFrom(ctx, "user"), Action: action, Entity: "project", EntityID: id, Details: details})
}

func (s *Service) emit(ctx context.Context, typ, id string, payload any) {
	if s.cfg.Rec == nil {
		return
	}
	s.cfg.Rec.Emit(ctx, application.Action{Type: typ, Entity: "project", EntityID: id, Payload: payload, SkipAudit: true})
}

// get loads a record. A launched, unfinished project without a live entry is
// adopted when the restart recovery ran (application/durable.go); without it
// (no recovery in this process) it is failed as interrupted.
func (s *Service) get(ctx context.Context, id string) (Record, error) {
	org := s.org(ctx)
	rec, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return rec, err
	}
	if rec.RequestID != "" && !terminalStatus(rec.Status) && s.liveOf(org, id) == nil && s.recovered.Load() {
		s.adopt(ctx, rec)
		return rec, nil
	}
	if rec.RequestID != "" && !terminalStatus(rec.Status) && s.liveOf(org, id) == nil {
		now := s.now()
		rec, err = s.update(ctx, id, func(r *Record) error {
			if terminalStatus(r.Status) {
				return nil
			}
			r.Status, r.Control, r.FinishedAt = StatusFailed, ControlCancelled, &now
			r.Error = "interrupted: the server restarted while the project was running"
			return nil
		})
	}
	return rec, err
}

// ---- reads ----

func (s *Service) detailOf(ctx context.Context, rec Record) (Detail, error) {
	snap, err := s.loadSnapshot(ctx, rec)
	if err != nil {
		return Detail{}, err
	}
	return s.build(ctx, snap), nil
}

// List returns the summaries of the organization's projects, newest first.
func (s *Service) List(ctx context.Context) ([]Summary, error) {
	recs, err := s.cfg.Store.List(ctx, s.org(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(recs))
	for _, r := range recs {
		rec, err := s.get(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		d, err := s.detailOf(ctx, rec)
		if err != nil {
			return nil, err
		}
		out = append(out, d.Project)
	}
	return out, nil
}

// Get returns the snapshot of a project: header, objectives, nodes and approvals.
func (s *Service) Get(ctx context.Context, id string) (Detail, error) {
	rec, err := s.get(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	return s.detailOf(ctx, rec)
}

// Info is the name and status of a project (used by the artifacts workspace).
func (s *Service) Info(ctx context.Context, id string) (name, status string, err error) {
	d, err := s.Get(ctx, id)
	return d.Project.Name, d.Project.Status, err
}

// Health computes the health view of a project.
func (s *Service) Health(ctx context.Context, id string) (Health, error) {
	d, err := s.Get(ctx, id)
	if err != nil {
		return Health{}, err
	}
	return computeHealth(d, s.now()), nil
}

// Approvals lists the approvals of a project (gates, agent tool approvals and budget).
func (s *Service) Approvals(ctx context.Context, id string) ([]Approval, error) {
	d, err := s.Get(ctx, id)
	return d.Approvals, err
}

// ---- drafts ----

var financialCloseRe = regexp.MustCompile(`(?i)cierre\s+(financiero|contable)|financial\s+close|(month|quarter|year)[ -]?end\s+close`)

const maxGoalLen = 2000

// CreateDraft builds a draft project from a template (id or key), the catalog
// workflows ("wf:<key>"), a custom template or, for a free goal, the runtime
// planner (falling back to the generic template).
func (s *Service) CreateDraft(ctx context.Context, in NewProject) (Record, error) {
	goal := strings.TrimSpace(in.Goal)
	if goal == "" && in.TemplateID == "" {
		return Record{}, fmt.Errorf("%w: goal is required", domain.ErrInvalid)
	}
	if len([]rune(goal)) > maxGoalLen {
		return Record{}, fmt.Errorf("%w: goal is too long", domain.ErrInvalid)
	}
	if in.BudgetUSD < 0 || math.IsNaN(in.BudgetUSD) || math.IsInf(in.BudgetUSD, 0) || in.BudgetUSD > 1e6 {
		return Record{}, fmt.Errorf("%w: budget_usd must be between 0 and 1000000", domain.ErrInvalid)
	}
	org := s.org(ctx)
	locale := s.locale(ctx, in.Locale)
	tpl, params, goal, err := s.pickTemplate(ctx, in, goal, locale)
	if err != nil {
		return Record{}, err
	}
	pid := "prj-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	inst, err := tpl.instantiate(pid, goal, params, locale)
	if err != nil {
		return Record{}, err
	}
	tid := tpl.ID
	rec := Record{ID: pid, OrgID: org, Name: inst.Name, NameKey: inst.NameKey, Goal: inst.Goal, Status: StatusDraft, Control: ControlActive, TemplateID: &tid,
		Params: params, Locale: locale, Budget: defaultBudgetPolicy(), MaxParallel: 4, Objectives: inst.Objectives, Nodes: inst.Nodes, StructureVersion: 1,
		Decisions: map[string]string{}, CreatedBy: application.ActorFrom(ctx, ""), CreatedAt: s.now()}
	rec.Estimate = estimatePlan(rec.Nodes, rec.Objectives)
	rec.BudgetUSD = in.BudgetUSD
	if rec.BudgetUSD <= 0 {
		rec.BudgetUSD = defaultBudget(rec.Estimate)
	}
	if err := s.cfg.Store.Put(ctx, rec); err != nil {
		return rec, err
	}
	s.audit(ctx, "project.created", pid, map[string]any{"template": tpl.ID, "nodes": len(leaves(rec.Nodes))})
	d, _ := s.detailOf(ctx, rec)
	s.emit(ctx, "project.created", pid, map[string]any{"project": d.Project, "nodes": d.Nodes, "objectives": d.Objectives, "planning": nil, "estimate": rec.Estimate})
	return rec, nil
}

func defaultBudget(e *Estimate) float64 { return math.Ceil(e.Total.P90USD*1.1*1000) / 1000 }

func (s *Service) pickTemplate(ctx context.Context, in NewProject, goal, locale string) (Template, map[string]string, string, error) {
	switch id := in.TemplateID; {
	case id == "" && financialCloseRe.MatchString(goal):
		t, _ := builtinByID(idFinancialClose)
		return t, in.Params, goal, nil
	case id == "":
		if t, ok := s.plannedTemplate(ctx, goal, locale); ok {
			return t, nil, goal, nil
		}
		t, _ := builtinByID(idGeneric)
		return t, nil, goal, nil
	case strings.HasPrefix(id, catalogPrefix):
		key := strings.TrimPrefix(id, catalogPrefix)
		plan, err := s.cfg.Catalog.Instantiate(key, in.Params, locale)
		if err != nil {
			return Template{}, nil, goal, err
		}
		name := plan.Text
		if v, ok := s.cfg.Catalog.Workflow(key, locale); ok {
			name = v.Name
		}
		if goal == "" {
			goal = plan.Text
		}
		return planTemplate(id, key, name, plan.Tasks), nil, goal, nil
	}
	if t, ok := builtinByID(in.TemplateID); ok {
		return t, in.Params, goal, nil
	}
	customs, err := s.cfg.Store.ListTemplates(ctx, s.org(ctx))
	if err != nil {
		return Template{}, nil, goal, err
	}
	for _, t := range customs {
		if t.ID == in.TemplateID || t.Key == in.TemplateID {
			return t, in.Params, goal, nil
		}
	}
	return Template{}, nil, goal, fmt.Errorf("%w: unknown template %q", domain.ErrNotFound, in.TemplateID)
}

// planTemplate wraps resolved tasks (catalog or planner) as a one-workflow template.
func planTemplate(id, key, name string, tasks []catalog.Task) Template {
	nodes := make([]TemplateNode, 0, len(tasks))
	for _, t := range tasks {
		nodes = append(nodes, TemplateNode{Key: t.Key, Title: t.Title, Description: t.Description, Agent: t.AgentID, Complexity: "M", Deps: t.DependsOn})
	}
	return Template{ID: id, Key: key, Version: 1, Name: name, Params: []TemplateParam{},
		Objectives: []TemplateObjective{{Key: "o1", Title: name, Workflows: []TemplateWorkflow{{Key: "w1", Title: name, Nodes: nodes}}}}}
}

// plannedTemplate asks the runtime planner for a plan (best effort).
func (s *Service) plannedTemplate(ctx context.Context, goal, locale string) (Template, bool) {
	if s.cfg.Runtime == nil || goal == "" {
		return Template{}, false
	}
	agents, err := s.cfg.Core.ListAgents(ctx, s.org(ctx))
	if err != nil || len(agents) == 0 {
		return Template{}, false
	}
	pa := make([]application.PlanAgent, 0, len(agents))
	known := map[string]bool{}
	for _, a := range agents {
		pa = append(pa, application.NewPlanAgent(a, locale))
		known[a.ID] = true
	}
	pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := s.cfg.Runtime.Plan(pctx, application.PlanRequest{RequestText: goal, Agents: pa, Locale: locale})
	if err != nil || len(resp.Tasks) == 0 {
		return Template{}, false
	}
	keys := map[string]bool{}
	var tasks []catalog.Task
	for i, t := range resp.Tasks {
		if t.Key == "" {
			t.Key = fmt.Sprintf("t%d", i+1)
		}
		if keys[t.Key] || strings.ContainsAny(t.Key, ":~") {
			return Template{}, false
		}
		keys[t.Key] = true
		if !known[t.AgentID] {
			t.AgentID = "assistant"
		}
		tasks = append(tasks, catalog.Task{Key: t.Key, Title: t.Title, Description: t.Description, AgentID: t.AgentID, DependsOn: t.DependsOn})
	}
	for i := range tasks {
		tasks[i].DependsOn = slices.DeleteFunc(slices.Clone(tasks[i].DependsOn), func(d string) bool { return !keys[d] })
	}
	return planTemplate("tpl-planned", "planned", truncRunes(goal, 60), tasks), true
}

// PatchPlan edits a draft. Only "update" operations (title, agent) exist today.
func (s *Service) PatchPlan(ctx context.Context, id string, ops []PlanOp) (int, []Issue, error) {
	var agentIDs []string
	if ags, err := s.cfg.Core.ListAgents(ctx, s.org(ctx)); err == nil {
		for _, a := range ags {
			agentIDs = append(agentIDs, a.ID)
		}
	}
	var issues []Issue
	rec, err := s.update(ctx, id, func(r *Record) error {
		if r.Status != StatusDraft {
			return fmt.Errorf("%w: only a draft can be edited", domain.ErrConflict)
		}
		for _, op := range ops {
			if op.Op != "update" {
				return fmt.Errorf("%w: unsupported plan operation %q", domain.ErrInvalid, op.Op)
			}
			i := slices.IndexFunc(r.Nodes, func(n NodeDef) bool { return n.ID == op.ID })
			if i < 0 {
				return fmt.Errorf("%w: unknown node %q", domain.ErrNotFound, op.ID)
			}
			n := &r.Nodes[i]
			if t := op.Fields.Title; t != nil {
				title := strings.TrimSpace(*t)
				if title == "" || len([]rune(title)) > 300 {
					return fmt.Errorf("%w: title must have 1-300 characters", domain.ErrInvalid)
				}
				n.Title, n.TitleKey, n.TitleParams = title, "", nil
			}
			if a := op.Fields.AgentID; a != nil {
				if n.isGroup() || n.human() {
					return fmt.Errorf("%w: node %q has no agent", domain.ErrInvalid, op.ID)
				}
				if len(agentIDs) > 0 && !slices.Contains(agentIDs, *a) {
					return fmt.Errorf("%w: unknown agent %q", domain.ErrInvalid, *a)
				}
				n.AgentID = ptrStr(*a)
				if k := len(n.DelegationChain); k > 0 {
					n.DelegationChain = append(slices.Clone(n.DelegationChain[:k-1]), *a)
				} else {
					n.DelegationChain = []string{*a}
				}
			}
		}
		r.StructureVersion++
		r.Estimate = estimatePlan(r.Nodes, r.Objectives)
		issues = validatePlan(r.Nodes, agentIDs)
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	s.audit(ctx, "project.plan_edited", id, map[string]any{"ops": len(ops)})
	d, _ := s.detailOf(ctx, rec)
	s.emit(ctx, "project.delta", id, map[string]any{"project": d.Project, "nodes": d.Nodes, "estimate": rec.Estimate, "structure_version": rec.StructureVersion})
	return rec.StructureVersion, nonNil(issues), nil
}

// Estimate recomputes and stores the estimate of a draft (or returns the stored one).
func (s *Service) Estimate(ctx context.Context, id string) (*Estimate, error) {
	rec, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec.Status != StatusDraft && rec.Estimate != nil {
		return rec.Estimate, nil
	}
	rec, err = s.update(ctx, id, func(r *Record) error { r.Estimate = estimatePlan(r.Nodes, r.Objectives); return nil })
	return rec.Estimate, err
}

// Validate checks a draft: cycles, agents, titles.
func (s *Service) Validate(ctx context.Context, id string) ([]Issue, error) {
	rec, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	ids, err := s.agentIDs(ctx)
	if err != nil {
		return nil, err
	}
	return validatePlan(rec.Nodes, ids), nil
}

func (s *Service) agentIDs(ctx context.Context) ([]string, error) {
	ags, err := s.cfg.Core.ListAgents(ctx, s.org(ctx))
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(ags))
	for _, a := range ags {
		ids = append(ids, a.ID)
	}
	return ids, nil
}

// ---- templates ----

// Templates lists the built-in project templates, the catalog workflows and the
// custom templates saved by the organization.
func (s *Service) Templates(ctx context.Context) ([]Template, error) {
	locale := s.locale(ctx, "")
	out := []Template{}
	for _, t := range builtinTemplates() {
		out = append(out, t.resolved(locale))
	}
	for _, v := range s.cfg.Catalog.Workflows(locale) {
		out = append(out, catalogTemplate(v))
	}
	customs, err := s.cfg.Store.ListTemplates(ctx, s.org(ctx))
	if err != nil {
		return nil, err
	}
	for _, t := range customs {
		out = append(out, t.resolved(locale))
	}
	return out, nil
}

// SaveAsTemplate stores the structure of a project as a reusable template.
func (s *Service) SaveAsTemplate(ctx context.Context, id string) (Template, error) {
	rec, err := s.get(ctx, id)
	if err != nil {
		return Template{}, err
	}
	customs, err := s.cfg.Store.ListTemplates(ctx, s.org(ctx))
	if err != nil {
		return Template{}, err
	}
	tpl := templateFromRecord(rec, "tpl-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:12], fmt.Sprintf("custom_%d", len(customs)+1))
	if err := s.cfg.Store.PutTemplate(ctx, s.org(ctx), tpl); err != nil {
		return tpl, err
	}
	s.audit(ctx, "project.saved_as_template", id, map[string]any{"template": tpl.ID})
	return tpl, nil
}
