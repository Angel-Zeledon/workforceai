package projects_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/roles"
)

// ---- a hierarchical runtime that mirrors the deterministic Python simulation ----

type hierRT struct {
	*fakeRuntime
	phasesErr  error
	failPhase  string        // phase key whose expansion fails
	gate       chan struct{} // when set, PlanPhases waits for it (a slow planner)
	mu         sync.Mutex
	calls      int
	inflight   int
	peak       int
	phaseSizes []string
}

var simRoadmap = []application.PlanPhase{
	{Key: "p1", Title: "Discovery", Size: "M", DependsOn: []string{}},
	{Key: "p2", Title: "Design", Size: "L", DependsOn: []string{"p1"}},
	{Key: "p3", Title: "Build", Size: "XL", DependsOn: []string{"p2"}},
	{Key: "p4", Title: "Support", Size: "L", DependsOn: []string{"p2"}},
	{Key: "p5", Title: "Closure", Size: "M", DependsOn: []string{"p3", "p4"}},
}

func (h *hierRT) PlanPhases(ctx context.Context, in application.PlanPhasesRequest) (application.PlanPhasesResponse, error) {
	h.mu.Lock()
	h.calls++
	h.mu.Unlock()
	if h.gate != nil {
		select {
		case <-h.gate:
		case <-ctx.Done():
			return application.PlanPhasesResponse{}, ctx.Err()
		}
	}
	if h.phasesErr != nil {
		return application.PlanPhasesResponse{}, h.phasesErr
	}
	return application.PlanPhasesResponse{Phases: simRoadmap, Usage: &application.Usage{Model: "deepseek-chat", InputTokens: 1000, OutputTokens: 500, CostUSD: 0.001}}, nil
}

func (h *hierRT) PlanPhase(_ context.Context, in application.PlanPhaseRequest) (application.PlanPhaseResponse, error) {
	h.mu.Lock()
	h.calls++
	h.inflight++
	h.peak = max(h.peak, h.inflight)
	h.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	h.mu.Lock()
	h.inflight--
	h.mu.Unlock()
	if in.Phase.Key == h.failPhase {
		return application.PlanPhaseResponse{}, errors.New("boom")
	}
	cx := []string{"S", "M", "L", "XL"}
	roles := []string{"sales", "analyst", "legal", "operations"}
	var tasks []application.PhaseTask
	for i := 0; i < in.TargetTasks; i++ {
		t := application.PhaseTask{Key: fmt.Sprintf("t%d", i+1), Title: fmt.Sprintf("%s step %d", in.Phase.Title, i+1), AgentID: roles[i%len(roles)],
			Complexity: cx[i%len(cx)], DependsOn: []string{}}
		if i >= 4 {
			t.DependsOn = []string{fmt.Sprintf("t%d", i-3)}
		}
		tasks = append(tasks, t)
	}
	return application.PlanPhaseResponse{Tasks: tasks, Usage: &application.Usage{Model: "deepseek-chat", InputTokens: 2000, OutputTokens: 1500, CostUSD: 0.002}}, nil
}

func newPlannerEnv(t *testing.T, rt application.Runtime, lim projects.Limits) (*projects.Service, *memory.Store, *application.Orchestrator) {
	t.Helper()
	cfg := application.DefaultConfig()
	cfg.IdleDelay, cfg.RetryBase = 0, time.Millisecond
	store := memory.New()
	if err := store.Seed(context.Background(), domainSeedOrg(cfg.BudgetUSD), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: store, Pub: nopPub{}, Log: log}
	appr := application.NewApprovals(cfg, store, rec)
	q := &application.Queries{Store: store, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	orch := application.NewOrchestrator(ctx, cfg, store, rt, memory.NewLocker(), rec, appr, q, log)
	svc := projects.New(ctx, projects.Config{Store: projects.NewMemStore(), Orch: orch, Core: store, Approvals: appr, Rec: rec, Runtime: rt,
		Catalog: catalog.Default(), OrgID: cfg.OrgID, Poll: 10 * time.Millisecond, Log: log, Limits: lim})
	return svc, store, orch
}

func leafCount(d projects.Detail) (n int) {
	for _, nd := range d.Nodes {
		if nd.Kind != projects.KindGroup {
			n++
		}
	}
	return
}

func TestHierarchicalPlanBuildsAValidLargeDAG(t *testing.T) {
	rt := &hierRT{fakeRuntime: newRT()}
	svc, store, _ := newPlannerEnv(t, rt, projects.Limits{})
	ctx := context.Background()
	rec, err := svc.CreateDraft(ctx, projects.NewProject{Goal: "Launch the new product line in three countries"})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := svc.Get(ctx, rec.ID)
	if p := d.Planner; p == nil || p.Mode != projects.PlannerHierarchical || p.Status != projects.PlannerOK || p.Phases != 5 {
		t.Fatalf("planner info: %+v", d.Planner)
	}
	if len(d.Objectives) < 3 {
		t.Fatalf("want >= 3 phases as objectives, got %d", len(d.Objectives))
	}
	if n := leafCount(d); n < 60 {
		t.Fatalf("want >= 60 tasks, got %d", n)
	}
	issues, err := svc.Validate(ctx, rec.ID)
	if err != nil || len(issues) != 0 {
		t.Fatalf("a hierarchical plan must be a valid DAG: %v %v", issues, err)
	}
	obj := map[string]string{}
	complexities := map[string]bool{}
	for _, n := range d.Nodes {
		obj[n.ID] = n.ObjectiveID
		if n.Kind != projects.KindGroup {
			complexities[n.Complexity] = true
		}
	}
	cross := 0
	for _, n := range d.Nodes {
		for _, dep := range n.DependsOn {
			if obj[dep] != n.ObjectiveID {
				cross++
			}
		}
	}
	if cross == 0 {
		t.Fatal("phases must depend on each other through cross-phase dependencies")
	}
	if len(complexities) < 3 {
		t.Fatalf("per-task complexity must reach the nodes: %v", complexities)
	}
	if rt.peak > 3 || rt.peak < 2 {
		t.Fatalf("phase expansions must run in parallel within the concurrency limit (3), peak %d", rt.peak)
	}
	// every planner call is in the cost ledger (1 phases call + 5 phase calls)
	usage, _ := store.ListUsage(ctx, application.DefaultConfig().OrgID)
	plan := 0
	for _, u := range usage {
		if string(u.Kind) == "plan" {
			plan++
		}
	}
	if plan != 6 {
		t.Fatalf("planner calls must be recorded as spend, got %d ledger entries", plan)
	}
	// the estimate prices the planner's complexity: an XL task costs more than an S one
	var s, xl float64
	for _, n := range d.Nodes {
		switch {
		case n.Kind != projects.KindGroup && n.Complexity == "S" && len(n.DependsOn) == 0:
			s = n.EstCostUSD
		case n.Kind != projects.KindGroup && n.Complexity == "XL" && len(n.DependsOn) == 0:
			xl = n.EstCostUSD
		}
	}
	if s == 0 || xl <= s {
		t.Fatalf("estimate must follow complexity: S=%v XL=%v", s, xl)
	}
}

func TestPlannerFailureIsVisible(t *testing.T) {
	// flat runtime without a plan: generic template + a visible failed notice
	rt := newRT()
	svc, _, _ := newPlannerEnv(t, rt, projects.Limits{})
	rec, err := svc.CreateDraft(context.Background(), projects.NewProject{Goal: "algo grande"})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := svc.Get(context.Background(), rec.ID)
	if d.Planner == nil || d.Planner.Status != projects.PlannerFailed || d.Planner.Mode != projects.PlannerGeneric || d.Planner.Code != "planner_unavailable" {
		t.Fatalf("a failed planner must be reported: %+v", d.Planner)
	}
	if leafCount(d) == 0 {
		t.Fatal("the generic template is still produced")
	}
}

func TestHierarchicalFallsBackToFlatVisibly(t *testing.T) {
	rt := &hierRT{fakeRuntime: newRT(), phasesErr: errors.New("model down")}
	rt.planResp = &application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "A", AgentID: "sales"}, {Key: "b", Title: "B", AgentID: "analyst", DependsOn: []string{"a"}}}}
	svc, _, _ := newPlannerEnv(t, rt, projects.Limits{})
	rec, err := svc.CreateDraft(context.Background(), projects.NewProject{Goal: "objetivo libre"})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := svc.Get(context.Background(), rec.ID)
	p := d.Planner
	if p == nil || p.Mode != projects.PlannerFlat || p.Status != projects.PlannerDegraded || p.FellBackFrom != projects.PlannerHierarchical || p.Code != "planner_unavailable" {
		t.Fatalf("fallback must be visible: %+v", p)
	}
	if leafCount(d) != 2 {
		t.Fatalf("flat plan: %d tasks", leafCount(d))
	}
}

func TestFailedPhaseDegradesInsteadOfLosingTheProject(t *testing.T) {
	rt := &hierRT{fakeRuntime: newRT(), failPhase: "p4"}
	svc, _, _ := newPlannerEnv(t, rt, projects.Limits{})
	rec, err := svc.CreateDraft(context.Background(), projects.NewProject{Goal: "proyecto grande"})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := svc.Get(context.Background(), rec.ID)
	p := d.Planner
	if p == nil || p.Status != projects.PlannerDegraded || p.Code != "phases_failed" || len(p.FailedPhases) != 1 || p.FailedPhases[0] != "p4" {
		t.Fatalf("failed phase must be reported: %+v", p)
	}
	if issues, _ := svc.Validate(context.Background(), rec.ID); len(issues) != 0 {
		t.Fatalf("still a valid DAG: %v", issues)
	}
}

func TestLimitMaxNodes(t *testing.T) {
	ctx := context.Background()
	// planner output above the limit is refused with the stable code the UI translates
	svc, _, _ := newPlannerEnv(t, &hierRT{fakeRuntime: newRT()}, projects.Limits{MaxNodesPerProject: 30})
	if _, err := svc.CreateDraft(ctx, projects.NewProject{Goal: "demasiado grande"}); err == nil || !strings.Contains(err.Error(), "limit_exceeded: max_nodes_per_project") {
		t.Fatalf("an oversized planner plan must be refused with the limit code, got %v", err)
	}
	// so is a template
	svc2, _, _ := newPlannerEnv(t, newRT(), projects.Limits{MaxNodesPerProject: 3})
	_, err := svc2.CreateDraft(ctx, projects.NewProject{Goal: "cierre financiero"})
	if err == nil || !strings.Contains(err.Error(), "limit_exceeded: max_nodes_per_project") {
		t.Fatalf("want max_nodes_per_project, got %v", err)
	}
}

func TestLimitMaxChildrenPerGroupChunksPlannerWorkflows(t *testing.T) {
	rt := &hierRT{fakeRuntime: newRT()}
	svc, _, _ := newPlannerEnv(t, rt, projects.Limits{MaxChildrenPerGroup: 10})
	rec, err := svc.CreateDraft(context.Background(), projects.NewProject{Goal: "proyecto grande"})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := svc.Get(context.Background(), rec.ID)
	children := map[string]int{}
	for _, n := range d.Nodes {
		if n.ParentID != nil {
			children[*n.ParentID]++
		}
	}
	groups := 0
	for _, n := range d.Nodes {
		if n.Kind == projects.KindGroup {
			groups++
		}
	}
	for g, c := range children {
		if c > 10 {
			t.Fatalf("group %s has %d children (> 10)", g, c)
		}
	}
	if groups <= 5 || leafCount(d) < 60 {
		t.Fatalf("phases must be split in workflows: %d groups, %d tasks", groups, leafCount(d))
	}
}

func TestLimitMaxChildrenRefusesAnOversizedWorkflow(t *testing.T) {
	rt := newRT()
	var tasks []application.PlannedTask
	for i := 0; i < 12; i++ {
		tasks = append(tasks, application.PlannedTask{Key: fmt.Sprintf("t%d", i), Title: fmt.Sprintf("T%d", i), AgentID: "sales"})
	}
	rt.planResp = &application.PlanResponse{Tasks: tasks}
	svc, _, _ := newPlannerEnv(t, rt, projects.Limits{MaxChildrenPerGroup: 5})
	_, err := svc.CreateDraft(context.Background(), projects.NewProject{Goal: "plan plano"})
	if err == nil || !strings.Contains(err.Error(), "limit_exceeded: max_children_per_group") {
		t.Fatalf("an oversized workflow must be refused with the limit code, got %v", err)
	}
}

func TestLimitMaxActiveProjects(t *testing.T) {
	rt := newRT()
	svc, _, _ := newPlannerEnv(t, rt, projects.Limits{MaxActiveProjects: 1})
	ctx := context.Background()
	a, err := svc.CreateDraft(ctx, projects.NewProject{Goal: "cierre financiero", TemplateID: "tpl-financial-close"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.CreateDraft(ctx, projects.NewProject{Goal: "cierre financiero", TemplateID: "tpl-financial-close"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Launch(ctx, a.ID, projects.LaunchBody{ApprovedBudgetUSD: 10, AcknowledgeUnderbudget: true}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Launch(ctx, b.ID, projects.LaunchBody{ApprovedBudgetUSD: 10, AcknowledgeUnderbudget: true})
	if err == nil || !strings.Contains(err.Error(), "limit_exceeded: max_active_projects") {
		t.Fatalf("the second launch must hit the active-projects limit, got %v", err)
	}
}

func TestSlowPlannerFinishesInTheBackground(t *testing.T) {
	gate := make(chan struct{})
	rt := &hierRT{fakeRuntime: newRT(), gate: gate}
	svc, _, _ := newPlannerEnv(t, rt, projects.Limits{SyncWait: 30 * time.Millisecond})
	ctx := context.Background()
	rec, err := svc.CreateDraft(ctx, projects.NewProject{Goal: "proyecto lento"})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := svc.Get(ctx, rec.ID)
	if d.Planner == nil || d.Planner.Status != projects.PlannerRunning || d.Planning == nil {
		t.Fatalf("a slow planner leaves a draft in planning state: %+v %+v", d.Planner, d.Planning)
	}
	if _, err := svc.Launch(ctx, rec.ID, projects.LaunchBody{ApprovedBudgetUSD: 10, AcknowledgeUnderbudget: true}); err == nil || !strings.Contains(err.Error(), "planning_in_progress") {
		t.Fatalf("a draft that is still being planned cannot launch: %v", err)
	}
	close(gate)
	deadline := time.Now().Add(10 * time.Second)
	for {
		d, _ = svc.Get(ctx, rec.ID)
		if d.Planner != nil && d.Planner.Status == projects.PlannerOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("planner never finished: %+v", d.Planner)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if leafCount(d) < 60 || d.Planning != nil {
		t.Fatalf("planned draft: %d tasks, planning %+v", leafCount(d), d.Planning)
	}
}

func TestOldPlanPayloadStillDecodes(t *testing.T) {
	// a runtime that predates W4 sends no usage and no complexity
	var r application.PlanResponse
	if err := jsonUnmarshal(`{"objectives":["o"],"tasks":[{"key":"a","title":"A","description":"d","agent_id":"sales","depends_on":[]}],"clarifying_questions":[]}`, &r); err != nil {
		t.Fatal(err)
	}
	if r.Usage != nil || len(r.Tasks) != 1 {
		t.Fatalf("%+v", r)
	}
}
