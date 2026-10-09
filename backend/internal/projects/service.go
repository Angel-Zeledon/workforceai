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
	// Limits are the size limits and planner bounds (zero values: defaults).
	Limits Limits
}

// Service is the projects use case layer.
type Service struct {
	cfg  Config
	root context.Context

	mu    sync.Mutex
	live  map[string]*liveProject // org|project id
	byReq map[string]*liveProject // org|request id
	locks map[string]*sync.Mutex  // org|project id
	jobs  map[string]*planJob     // org|project id: planners still running
	// launchMu serializes the active-projects check with the launch itself.
	launchMu sync.Mutex

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
	cfg.Limits = cfg.Limits.withDefaults()
	s := &Service{cfg: cfg, root: ctx, live: map[string]*liveProject{}, byReq: map[string]*liveProject{}, locks: map[string]*sync.Mutex{}, jobs: map[string]*planJob{}}
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
	d := s.build(ctx, snap)
	d.Planner = snap.rec.Planner
	if p := snap.rec.Planner; p != nil && p.Status == PlannerRunning {
		if j := s.jobOf(snap.rec.OrgID, snap.rec.ID); j != nil {
			d.Planning = &PlanningProgress{Done: int(j.doneN.Load()), Total: int(j.tot.Load())}
		} else { // the server restarted while the planner ran: say so, do not wait forever
			cp := *p
			cp.Status, cp.Code = PlannerFailed, "planner_interrupted"
			d.Planner = &cp
		}
	}
	return d, nil
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
	pid := "prj-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	var (
		tpl    Template
		params map[string]string
		info   *PlannerInfo
		job    *planJob
		err    error
	)
	if in.TemplateID == "" && goal != "" && !financialCloseRe.MatchString(goal) {
		// A free goal: the planner (hierarchical when the runtime supports it) builds the plan.
		// CreateDraft waits for it up to SyncWait; a slower planner finishes in the background
		// and the draft shows its progress (Detail.Planning) until then.
		job = s.startPlanner(ctx, pid, goal, locale)
		select {
		case <-job.done:
			tpl, info = job.tpl, &job.info
			s.dropJob(org, pid)
		case <-time.After(s.cfg.Limits.SyncWait):
			tpl, info = placeholderTemplate(goal), &PlannerInfo{Mode: PlannerHierarchical, Status: PlannerRunning}
		case <-ctx.Done():
			return Record{}, ctx.Err()
		}
	} else if tpl, params, goal, err = s.pickTemplate(ctx, in, goal, locale); err != nil {
		return Record{}, err
	}
	inst, err := tpl.instantiate(pid, goal, params, locale)
	if err == nil {
		err = s.cfg.Limits.checkSize(inst.Nodes)
	}
	if err != nil {
		return Record{}, err
	}
	tid := tpl.ID
	rec := Record{ID: pid, OrgID: org, Name: inst.Name, NameKey: inst.NameKey, Goal: inst.Goal, Status: StatusDraft, Control: ControlActive, TemplateID: &tid,
		Params: params, Locale: locale, Budget: defaultBudgetPolicy(), MaxParallel: 4, Objectives: inst.Objectives, Nodes: inst.Nodes, StructureVersion: 1,
		Decisions: map[string]string{}, CreatedBy: application.ActorFrom(ctx, ""), CreatedAt: s.now(), Planner: info}
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
	s.emit(ctx, "project.created", pid, map[string]any{"project": d.Project, "nodes": d.Nodes, "objectives": d.Objectives, "planning": d.Planning, "estimate": rec.Estimate, "planner": d.Planner})
	if job != nil && info.Status == PlannerRunning {
		go s.finishPlanning(context.WithoutCancel(ctx), rec, job, in.BudgetUSD, locale)
	} else if info != nil {
		s.plannerNotice(ctx, pid, info)
	}
	return rec, nil
}

// plannerNotice makes a planner failure or degradation visible: an event for the
// open UI (the draft also carries it) and an audit entry. An ok plan is silent.
func (s *Service) plannerNotice(ctx context.Context, pid string, info *PlannerInfo) {
	if info == nil || info.Status == PlannerOK || info.Status == PlannerRunning {
		return
	}
	s.audit(ctx, "project.planner_"+info.Status, pid, map[string]any{"mode": info.Mode, "code": info.Code, "fell_back_from": info.FellBackFrom,
		"failed_phases": info.FailedPhases, "message": info.Message})
	s.emit(ctx, "project.planner_notice", pid, map[string]any{"project_id": pid, "planner": info})
}

// finishPlanning waits for a background planner and fills the draft with its plan.
func (s *Service) finishPlanning(ctx context.Context, rec Record, job *planJob, userBudget float64, locale string) {
	<-job.done
	org := s.org(ctx)
	defer s.dropJob(org, rec.ID)
	tpl, info := job.tpl, job.info
	inst, err := tpl.instantiate(rec.ID, rec.Goal, nil, locale)
	if err == nil {
		err = s.cfg.Limits.checkSize(inst.Nodes)
	}
	if err != nil { // the plan does not fit the limits: keep the generic one and say why
		info = PlannerInfo{Mode: PlannerGeneric, Status: PlannerFailed, Code: "planner_invalid", Message: clip(err.Error(), 200), Calls: info.Calls, CostUSD: info.CostUSD}
		inst, _ = genericTemplate().instantiate(rec.ID, rec.Goal, nil, locale)
	}
	var out Record
	_, err = s.update(ctx, rec.ID, func(r *Record) error {
		if r.Status != StatusDraft || r.Planner == nil || r.Planner.Status != PlannerRunning {
			return fmt.Errorf("%w: the draft changed while it was being planned", domain.ErrConflict)
		}
		tid := tpl.ID
		r.TemplateID, r.Objectives, r.Nodes, r.Planner = &tid, inst.Objectives, inst.Nodes, &info
		r.Estimate = estimatePlan(r.Nodes, r.Objectives)
		if userBudget <= 0 {
			r.BudgetUSD = defaultBudget(r.Estimate)
		}
		r.StructureVersion++
		out = *r
		return nil
	})
	if err != nil {
		s.cfg.Log.Warn("planner result discarded", "project", rec.ID, "err", err)
		return
	}
	d, _ := s.detailOf(ctx, out)
	s.audit(ctx, "project.planned", rec.ID, map[string]any{"mode": info.Mode, "status": info.Status, "nodes": len(leaves(out.Nodes)), "calls": info.Calls, "cost_usd": info.CostUSD})
	s.emit(ctx, "project.planned", rec.ID, map[string]any{"project": d.Project, "nodes": d.Nodes, "objectives": d.Objectives, "estimate": out.Estimate,
		"structure_version": out.StructureVersion, "planner": d.Planner})
	s.plannerNotice(ctx, rec.ID, &info)
}

func defaultBudget(e *Estimate) float64 { return math.Ceil(e.Total.P90USD*1.1*1000) / 1000 }

func (s *Service) pickTemplate(ctx context.Context, in NewProject, goal, locale string) (Template, map[string]string, string, error) {
	switch id := in.TemplateID; {
	case id == "" && financialCloseRe.MatchString(goal):
		t, _ := builtinByID(idFinancialClose)
		return t, in.Params, goal, nil
	case id == "":
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
