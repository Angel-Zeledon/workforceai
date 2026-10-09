package projects_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/roles"
)

// ---- fakes ----

// fakeRuntime records which tasks run, how many at once, and lets a test hook
// into RunTask.
type fakeRuntime struct {
	mu       sync.Mutex
	running  int
	peak     int
	started  []string       // task titles in start order
	finished map[string]int // title -> order of finish
	nFin     int
	onRun    func(in application.RunTaskRequest)
	cost     float64
	planResp *application.PlanResponse
	failFn   func(title string) error                                             // injected runtime failure (nil: none)
	review   func(n int, in application.ReviewRequest) application.ReviewResponse // Q1 reviewer (nil: always pass)
	reviews  []application.ReviewRequest
	suggest  func(title string) []string // suggested_tasks of a task output (nil: none)
}

func newRT() *fakeRuntime { return &fakeRuntime{finished: map[string]int{}} }

func (f *fakeRuntime) Plan(context.Context, application.PlanRequest) (application.PlanResponse, error) {
	if f.planResp != nil {
		return *f.planResp, nil
	}
	return application.PlanResponse{}, errors.New("no planner")
}

func (f *fakeRuntime) RunTask(_ context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
	f.mu.Lock()
	f.running++
	f.peak = max(f.peak, f.running)
	f.started = append(f.started, in.Task.Title)
	f.mu.Unlock()
	if f.onRun != nil {
		f.onRun(in)
	}
	if fn := f.failFn; fn != nil {
		if err := fn(in.Task.Title); err != nil {
			f.mu.Lock()
			f.running--
			f.mu.Unlock()
			return application.RunTaskResponse{}, err
		}
	}
	f.mu.Lock()
	f.running--
	f.nFin++
	f.finished[in.Task.Title] = f.nFin
	f.mu.Unlock()
	var sug []string
	if f.suggest != nil {
		sug = f.suggest(in.Task.Title)
	}
	return application.RunTaskResponse{Output: domain.StructuredOutput{Summary: "hecho: " + in.Task.Title, Confidence: 0.9, SuggestedTasks: sug},
		Usage: application.Usage{Model: "deepseek-chat", CostUSD: f.cost}}, nil
}

func (f *fakeRuntime) Consult(context.Context, application.ConsultRequest) (application.ConsultResponse, error) {
	return application.ConsultResponse{Answer: "ok"}, nil
}
func (f *fakeRuntime) Synthesize(context.Context, application.SynthesizeRequest) (application.SynthesizeResponse, error) {
	return application.SynthesizeResponse{Title: "Informe", Summary: "ok", Sections: []domain.Section{}}, nil
}
func (f *fakeRuntime) Health(context.Context) (string, error) { return "simulation", nil }

func (f *fakeRuntime) startedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

func (f *fakeRuntime) hasStarted(title string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.started {
		if t == title {
			return true
		}
	}
	return false
}

type nopPub struct{}

func (nopPub) Publish(context.Context, domain.Event) error { return nil }

// stubGuard is an execution guard that blocks the agents in `blocked`.
type stubGuard struct {
	mu      sync.Mutex
	blocked map[string]bool
}

func (g *stubGuard) set(agent string, v bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blocked[agent] = v
}
func (g *stubGuard) Admit(_ context.Context, _, agent string) application.GuardVerdict {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.blocked[agent] {
		return application.GuardVerdict{Code: "agent_paused"}
	}
	return application.GuardVerdict{Allowed: true}
}
func (g *stubGuard) SideEffectsBlocked(context.Context, string, string) bool  { return false }
func (g *stubGuard) ToolBlocked(context.Context, string, string, string) bool { return false }
func (g *stubGuard) IsSideEffect(string, string) bool                         { return true }

type env struct {
	t     *testing.T
	svc   *projects.Service
	orch  *application.Orchestrator
	appr  *application.Approvals
	store *memory.Store
	rt    *fakeRuntime
	ctx   context.Context
}

func newEnv(t *testing.T, rt *fakeRuntime, guard application.ExecutionGuard, mutate func(*application.Config)) *env {
	t.Helper()
	cfg := application.DefaultConfig()
	cfg.IdleDelay, cfg.RetryBase = 0, time.Millisecond
	if mutate != nil {
		mutate(&cfg)
	}
	store := memory.New()
	if err := store.Seed(context.Background(), domain.SeedOrg(cfg.BudgetUSD), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: store, Pub: nopPub{}, Log: log}
	appr := application.NewApprovals(cfg, store, rec)
	q := &application.Queries{Store: store, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	orch := application.NewOrchestrator(ctx, cfg, store, rt, memory.NewLocker(), rec, appr, q, log)
	if guard != nil {
		orch.SetConnections(guard, nil, nil)
		orch.SetGuardPoll(10 * time.Millisecond)
	}
	svc := projects.New(ctx, projects.Config{Store: projects.NewMemStore(), Orch: orch, Core: store, Approvals: appr, Rec: rec, Runtime: rt,
		Guard: guard, Catalog: catalog.Default(), OrgID: cfg.OrgID, Poll: 10 * time.Millisecond, Log: log})
	return &env{t: t, svc: svc, orch: orch, appr: appr, store: store, rt: rt, ctx: context.Background()}
}

func (e *env) waitFor(what string, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.t.Fatalf("timeout waiting for %s", what)
}

func (e *env) detail(id string) projects.Detail {
	e.t.Helper()
	d, err := e.svc.Get(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *env) draft(templateID string, budget float64) string {
	e.t.Helper()
	rec, err := e.svc.CreateDraft(e.ctx, projects.NewProject{Goal: "cierre financiero", TemplateID: templateID, BudgetUSD: budget})
	if err != nil {
		e.t.Fatal(err)
	}
	return rec.ID
}

func (e *env) launch(id string, budget float64) {
	e.t.Helper()
	if _, err := e.svc.Launch(e.ctx, id, projects.LaunchBody{ApprovedBudgetUSD: budget, AcknowledgeUnderbudget: true}); err != nil {
		e.t.Fatal(err)
	}
}

// approveAll decides pending approvals of the project until stop returns true.
func (e *env) approveAll(id string, stop func(projects.Detail) bool) {
	e.t.Helper()
	e.waitFor("project to settle", func() bool {
		d := e.detail(id)
		for _, a := range d.Approvals {
			if a.Status == "pending" {
				if _, err := e.appr.Decide(e.ctx, a.ID, "approve", ""); err != nil {
					e.t.Fatalf("approve %s: %v", a.Action, err)
				}
			}
		}
		return stop(d)
	})
}

// approveGates approves the pending project approvals once.
func (e *env) approveGates(id string) {
	for _, a := range e.detail(id).Approvals {
		if a.Status == "pending" {
			_, _ = e.appr.Decide(e.ctx, a.ID, "approve", "")
		}
	}
}

func nodeByTitle(d projects.Detail, title string) projects.Node {
	for _, n := range d.Nodes {
		if n.Title == title {
			return n
		}
	}
	return projects.Node{}
}

const (
	tFC       = "tpl-financial-close"
	budgetBig = 5.0
)

// ---- tests ----

// The accountant builds the balance sheet and, IN PARALLEL, the income
// statement; the equity step waits for the net income (cross-workflow dependency).
func TestFinancialCloseRunsParallelWorkRespectingDependencies(t *testing.T) {
	rt := newRT()
	// The three tasks that only depend on the trial balance must be in flight together.
	var barrier sync.WaitGroup
	barrier.Add(3)
	var released atomic.Bool
	rt.onRun = func(in application.RunTaskRequest) {
		switch in.Task.Title {
		case "Clasificar activos y pasivos", "Consolidar los ingresos", "Consolidar costos y gastos":
			barrier.Done()
			done := make(chan struct{})
			go func() { barrier.Wait(); close(done) }()
			select {
			case <-done:
				released.Store(true)
			case <-time.After(5 * time.Second): // would mean they did not overlap
			}
		}
	}
	e := newEnv(t, rt, nil, nil)
	id := e.draft(tFC, 0)

	d := e.detail(id)
	if d.Project.Status != projects.StatusDraft || d.Project.TasksTotal != 14 || len(d.Objectives) != 3 {
		t.Fatalf("draft: status=%s tasks=%d objectives=%d", d.Project.Status, d.Project.TasksTotal, len(d.Objectives))
	}
	sub := nodeByTitle(d, "Confirmar el inventario físico")
	if sub.Kind != projects.KindSubtask || sub.DelegationDepth != 2 || sub.AgentID == nil || *sub.AgentID != "operations" {
		t.Fatalf("delegated subtask wrong: %+v", sub)
	}
	e.launch(id, budgetBig)
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })

	if !released.Load() {
		t.Fatal("a1, b1 and b2 never ran at the same time: no parallelism")
	}
	d = e.detail(id)
	for _, n := range d.Nodes {
		if n.Kind != projects.KindGroup && n.State != projects.StateDone {
			t.Fatalf("node %q ended %s", n.Title, n.State)
		}
	}
	// Dependencies: equity (a2) after net income (b3); the report after the owner gate.
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.finished["Calcular la utilidad neta"] >= rt.finished["Calcular el patrimonio (usa la utilidad neta)"] {
		t.Fatal("equity was calculated before the net income finished")
	}
	if rt.peak < 3 {
		t.Fatalf("peak concurrency %d", rt.peak)
	}
	// Real tasks with dependencies exist in the orchestrator.
	tasks, _ := e.store.ListTasksByRequest(e.ctx, domain.DemoOrgID, d.Project.RequestID)
	if len(tasks) != 14 {
		t.Fatalf("orchestrator tasks = %d", len(tasks))
	}
	withDeps := 0
	for _, tk := range tasks {
		if len(tk.DependsOn) > 0 {
			withDeps++
		}
	}
	if withDeps < 10 {
		t.Fatalf("only %d tasks carry dependencies", withDeps)
	}
	if d.Project.SpentUSD < 0 || d.Project.Light == "" {
		t.Fatalf("summary: %+v", d.Project)
	}
}

func TestPauseResumeCancel(t *testing.T) {
	rt := newRT()
	hold := make(chan struct{})
	rt.onRun = func(in application.RunTaskRequest) {
		if in.Task.Title == "Investigar el contexto y los datos" {
			<-hold // keep the first wave in flight while the project is paused
		}
	}
	e := newEnv(t, rt, nil, nil)
	id := e.draft("tpl-generic", 0)
	e.launch(id, budgetBig)
	e.waitFor("first wave", func() bool { return rt.hasStarted("Investigar el contexto y los datos") })

	sum, err := e.svc.Control(e.ctx, id, "pause")
	if err != nil || sum.Control != projects.ControlPaused {
		t.Fatalf("pause: %v %+v", err, sum)
	}
	close(hold)
	e.waitFor("paused state", func() bool { return e.detail(id).Project.Status == projects.StatusPaused })
	before := rt.startedCount()
	time.Sleep(300 * time.Millisecond)
	if got := rt.startedCount(); got > before+1 { // at most the other task of the first wave
		t.Fatalf("a paused project started new work: %d -> %d", before, got)
	}
	if _, err := e.svc.Control(e.ctx, id, "pause"); err != nil {
		t.Fatalf("pause is idempotent: %v", err)
	}
	if _, err := e.svc.Control(e.ctx, id, "resume"); err != nil {
		t.Fatal(err)
	}
	e.waitFor("progress after resume", func() bool { return rt.hasStarted("Elaborar la propuesta principal") })

	if _, err := e.svc.Control(e.ctx, id, "cancel"); err != nil {
		t.Fatal(err)
	}
	e.waitFor("cancelled", func() bool { return e.detail(id).Project.Status == projects.StatusCancelled })
	n := rt.startedCount()
	time.Sleep(300 * time.Millisecond)
	if rt.startedCount() != n {
		t.Fatal("a cancelled project kept starting tasks")
	}
	if rt.hasStarted("Consolidar el informe final") {
		t.Fatal("the final report ran in a cancelled project")
	}
	d := e.detail(id)
	if _, err := e.svc.Control(e.ctx, id, "resume"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("resume of a cancelled project: %v", err)
	}
	if d.Project.Control != projects.ControlCancelled {
		t.Fatalf("control = %s", d.Project.Control)
	}
}

// A project that reaches its cap pauses visibly and asks for more money
// (extend_budget) instead of silently overspending.
func TestBudgetPauseAsksForMoreBudget(t *testing.T) {
	rt := newRT()
	rt.cost = 0.001
	e := newEnv(t, rt, nil, nil)
	id := e.draft("tpl-generic", 0)
	if _, err := e.svc.SetBudget(e.ctx, id, 0.01); err != nil {
		t.Fatal(err)
	}
	e.launch(id, 0.01)
	e.waitFor("budget approval", func() bool {
		for _, a := range e.detail(id).Approvals {
			if a.Action == projects.ActionExtendBudget && a.Status == "pending" {
				return true
			}
		}
		return false
	})
	if st := e.detail(id).Project.Status; st != projects.StatusWaitingHuman {
		t.Fatalf("status while paused by budget = %s", st)
	}
	extended := 0
	e.approveAll(id, func(d projects.Detail) bool {
		extended = 0
		for _, a := range d.Approvals {
			if a.Action == projects.ActionExtendBudget {
				extended++
			}
		}
		return d.Project.Status == projects.StatusDone
	})
	if extended == 0 {
		t.Fatal("no extend_budget approval was ever raised")
	}
	if got := e.detail(id).Project.BudgetUSD; got <= 0.01 {
		t.Fatalf("the approved extension did not raise the budget: %v", got)
	}
}

func TestBatchApprovalsAndHighRiskConfirmation(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	id := e.draft(tFC, 0)
	e.launch(id, budgetBig)

	pending := func(action string) int {
		n := 0
		for _, a := range e.detail(id).Approvals {
			if a.Action == action && a.Status == "pending" {
				n++
			}
		}
		return n
	}
	// Both statements wait for a person at the same time (they are independent branches).
	e.waitFor("two publish_statement approvals", func() bool { return pending("publish_statement") == 2 })

	batch := projects.BatchDecision{Decision: "approve", ExpectedCount: 1}
	batch.Filter.ProjectID, batch.Filter.Action = id, "publish_statement"
	if _, err := e.svc.DecideBatch(e.ctx, batch); !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "count_mismatch") {
		t.Fatalf("wrong expected_count must conflict: %v", err)
	}
	// An agent can never decide, not even through the batch endpoint.
	agentCtx := application.WithActor(e.ctx, "agent:accounting")
	batch.ExpectedCount = 2
	if _, err := e.svc.DecideBatch(agentCtx, batch); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("agents must not approve: %v", err)
	}
	if pending("publish_statement") != 2 {
		t.Fatal("a refused batch changed approvals")
	}
	res, err := e.svc.DecideBatch(e.ctx, batch)
	if err != nil || !res.OK || res.Decided != 2 {
		t.Fatalf("batch: %v %+v", err, res)
	}

	// The owner gate is high risk: it needs an explicit confirmation.
	e.waitFor("owner gate", func() bool { return pending("approve_close") == 1 })
	gate := projects.BatchDecision{Decision: "approve", ExpectedCount: 1}
	gate.Filter.ProjectID, gate.Filter.Action = id, "approve_close"
	if _, err := e.svc.DecideBatch(e.ctx, gate); !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "high_risk_needs_confirmation") {
		t.Fatalf("high risk needs confirmation: %v", err)
	}
	gate.IncludeHigh = true
	if _, err := e.svc.DecideBatch(e.ctx, gate); err != nil {
		t.Fatal(err)
	}
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })
}

func TestRejectedGateBlocksWhatDependsOnIt(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	id := e.draft("tpl-generic", 0)
	e.launch(id, budgetBig)
	var gate string
	e.waitFor("plan gate", func() bool {
		for _, a := range e.detail(id).Approvals {
			if a.Action == "approve_plan" && a.Status == "pending" {
				gate = a.ID
			}
		}
		return gate != ""
	})
	if _, err := e.appr.Decide(e.ctx, gate, "reject", "no"); err != nil {
		t.Fatal(err)
	}
	e.waitFor("failed project", func() bool { return e.detail(id).Project.Status == projects.StatusFailed })
	d := e.detail(id)
	if n := nodeByTitle(d, "Consolidar el informe final"); n.State != projects.StateBlocked {
		t.Fatalf("report after a rejected gate = %s", n.State)
	}
	if e.rt.hasStarted("Consolidar el informe final") {
		t.Fatal("a task behind a rejected gate ran")
	}
}

// An agent paused by the kill switch / controls keeps its project tasks waiting.
func TestAgentPauseHoldsProjectNodes(t *testing.T) {
	g := &stubGuard{blocked: map[string]bool{"analyst": true}}
	rt := newRT()
	e := newEnv(t, rt, g, nil)
	id := e.draft("tpl-generic", 0)
	e.launch(id, budgetBig)
	e.waitFor("operations task done", func() bool { return rt.hasStarted("Evaluar la viabilidad operativa") })
	time.Sleep(150 * time.Millisecond)
	if rt.hasStarted("Investigar el contexto y los datos") {
		t.Fatal("a paused agent ran a project task")
	}
	if n := nodeByTitle(e.detail(id), "Investigar el contexto y los datos"); n.State != projects.StatePaused {
		t.Fatalf("node of a paused agent = %s", n.State)
	}
	g.set("analyst", false)
	e.waitFor("analyst task after release", func() bool { return rt.hasStarted("Investigar el contexto y los datos") })
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })
}

func TestLaunchRules(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	id := e.draft(tFC, 0)
	est, err := e.svc.Estimate(e.ctx, id)
	if err != nil || est.Total.P50USD <= 0 || est.Total.P90USD < est.Total.P50USD || est.Basis != "priors" {
		t.Fatalf("estimate: %v %+v", err, est)
	}
	if _, err := e.svc.Launch(e.ctx, id, projects.LaunchBody{ApprovedBudgetUSD: est.Total.P50USD / 2}); !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "underbudget") {
		t.Fatalf("underbudget without acknowledgement: %v", err)
	}
	if _, err := e.svc.Launch(e.ctx, id, projects.LaunchBody{ApprovedBudgetUSD: 0}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("zero budget: %v", err)
	}
	// Edit the draft: unknown agent is refused, a valid one is applied.
	n := nodeByTitle(e.detail(id), "Revisar el cumplimiento fiscal")
	bad := "ghost"
	op := projects.PlanOp{Op: "update", ID: n.ID}
	op.Fields.AgentID = &bad
	if _, _, err := e.svc.PatchPlan(e.ctx, id, []projects.PlanOp{op}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown agent: %v", err)
	}
	title, agent := "Revisar impuestos", "analyst"
	op.Fields.AgentID, op.Fields.Title = &agent, &title
	if _, issues, err := e.svc.PatchPlan(e.ctx, id, []projects.PlanOp{op}); err != nil || len(issues) != 0 {
		t.Fatalf("patch: %v %v", err, issues)
	}
	if got := nodeByTitle(e.detail(id), "Revisar impuestos"); got.AgentID == nil || *got.AgentID != "analyst" || got.TitleKey != "" {
		t.Fatalf("patched node: %+v", got)
	}
	e.launch(id, budgetBig)
	if _, err := e.svc.Launch(e.ctx, id, projects.LaunchBody{ApprovedBudgetUSD: budgetBig}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a launched project cannot be launched again: %v", err)
	}
	if _, _, err := e.svc.PatchPlan(e.ctx, id, []projects.PlanOp{op}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a launched project cannot be edited: %v", err)
	}
	// The budget can be changed while running; the request cap follows.
	if _, err := e.svc.SetBudget(e.ctx, id, 9); err != nil {
		t.Fatal(err)
	}
	if caps, _ := e.store.ListBudgetCaps(e.ctx, domain.DemoOrgID); len(caps) == 0 {
		t.Fatal("the request cap was not stored")
	}
	if h, err := e.svc.Health(e.ctx, id); err != nil || h.ProjectID != id || len(h.CriticalPath.NodeIDs) == 0 {
		t.Fatalf("health: %v %+v", err, h)
	}
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })
}

func TestTemplatesCatalogAndSaveAsTemplate(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	tpls, err := e.svc.Templates(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tp := range tpls {
		have[tp.ID] = true
	}
	for _, id := range []string{"tpl-financial-close", "tpl-branch-opening", "tpl-generic", "wf:month_close"} {
		if !have[id] {
			t.Fatalf("template %s missing (%v)", id, have)
		}
	}
	rec, err := e.svc.CreateDraft(e.ctx, projects.NewProject{Goal: "", TemplateID: "wf:month_close", Params: map[string]string{"month": "marzo"}})
	if err != nil {
		t.Fatal(err)
	}
	d := e.detail(rec.ID)
	if d.Project.TasksTotal != 5 || !strings.Contains(nodeByTitle(d, "Balance general de marzo").Title, "marzo") {
		t.Fatalf("catalog draft: %+v", d.Project)
	}
	// A free goal about the financial close picks the close template.
	rec2, err := e.svc.CreateDraft(e.ctx, projects.NewProject{Goal: "Haz el cierre financiero de septiembre"})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.detail(rec2.ID).Project.TasksTotal; got != 14 {
		t.Fatalf("free goal did not map to the financial close: %d tasks", got)
	}
	// Save as template and reuse it.
	tpl, err := e.svc.SaveAsTemplate(e.ctx, rec2.ID)
	if err != nil || tpl.Builtin {
		t.Fatalf("save as template: %v", err)
	}
	rec3, err := e.svc.CreateDraft(e.ctx, projects.NewProject{Goal: "otro cierre", TemplateID: tpl.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.detail(rec3.ID).Project.TasksTotal; got != 14 {
		t.Fatalf("custom template drifted: %d tasks", got)
	}
	// The planner path: a free goal planned by the runtime.
	e.rt.planResp = &application.PlanResponse{Tasks: []application.PlannedTask{
		{Key: "a", Title: "Investigar", AgentID: "analyst"}, {Key: "b", Title: "Redactar", AgentID: "ghost", DependsOn: []string{"a", "zzz"}}}}
	rec4, err := e.svc.CreateDraft(e.ctx, projects.NewProject{Goal: "lanzar un producto"})
	if err != nil {
		t.Fatal(err)
	}
	d4 := e.detail(rec4.ID)
	b := nodeByTitle(d4, "Redactar")
	if d4.Project.TasksTotal != 2 || b.AgentID == nil || *b.AgentID != "assistant" || len(b.DependsOn) != 1 {
		t.Fatalf("planned draft: %+v", b)
	}
}

func TestOrganizationIsolation(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	id := e.draft("tpl-generic", 0)
	other := application.WithOrg(context.Background(), "00000000-0000-0000-0000-0000000000b2")
	if _, err := e.svc.Get(other, id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("another organization read the project: %v", err)
	}
	if list, err := e.svc.List(other); err != nil || len(list) != 0 {
		t.Fatalf("another organization lists projects: %v %v", err, list)
	}
	for name, fn := range map[string]func() error{
		"launch":   func() error { _, err := e.svc.Launch(other, id, projects.LaunchBody{ApprovedBudgetUSD: 1}); return err },
		"control":  func() error { _, err := e.svc.Control(other, id, "cancel"); return err },
		"budget":   func() error { _, err := e.svc.SetBudget(other, id, 5); return err },
		"health":   func() error { _, err := e.svc.Health(other, id); return err },
		"template": func() error { _, err := e.svc.SaveAsTemplate(other, id); return err },
		"batch": func() error {
			b := projects.BatchDecision{Decision: "approve"}
			b.Filter.ProjectID, b.Filter.Action = id, "x"
			_, err := e.svc.DecideBatch(other, b)
			return err
		},
	} {
		if err := fn(); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s from another organization: %v", name, err)
		}
	}
	if list, _ := e.svc.List(e.ctx); len(list) != 1 {
		t.Fatalf("owner lists %d projects", len(list))
	}
	// Custom templates are per organization too.
	if _, err := e.svc.SaveAsTemplate(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	if tpls, _ := e.svc.Templates(other); func() bool {
		for _, tp := range tpls {
			if !tp.Builtin {
				return true
			}
		}
		return false
	}() {
		t.Fatal("another organization sees custom templates")
	}
}

func TestDraftRejectsBadInput(t *testing.T) {
	e := newEnv(t, newRT(), nil, nil)
	for name, in := range map[string]projects.NewProject{
		"empty":    {},
		"budget":   {Goal: "x", BudgetUSD: -1},
		"template": {Goal: "x", TemplateID: "nope"},
		"long":     {Goal: strings.Repeat("x", 3000)},
	} {
		if _, err := e.svc.CreateDraft(e.ctx, in); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
	if _, err := e.svc.CreateDraft(e.ctx, projects.NewProject{TemplateID: "wf:month_close"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("missing required parameter: %v", err)
	}
}
