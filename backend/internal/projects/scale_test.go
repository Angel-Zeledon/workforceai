package projects_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
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

// End-to-end scale test in simulation: a goal is planned hierarchically (5
// phases, ~78 tasks), launched with max_parallel 16 and one transient failure
// and one human-gated task, and must finish with a bounded number of synthesis
// calls and exactly one budget early warning. It chains W1 (parallelism, slots
// released while waiting), W2 (auto-retry), W3 (hierarchical synthesis), W4
// (planner) and W5 (budget warning).

type evLog struct {
	mu sync.Mutex
	n  map[string]int
}

func (e *evLog) Publish(_ context.Context, ev domain.Event) error {
	e.mu.Lock()
	if e.n == nil {
		e.n = map[string]int{}
	}
	e.n[ev.Type]++
	e.mu.Unlock()
	return nil
}
func (e *evLog) count(t string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.n[t]
}

// scaleRT is the hierarchical planner runtime plus a synthesis counter.
type scaleRT struct {
	*hierRT
	synth atomic.Int32
	mu    sync.Mutex
	price map[string]float64 // task title -> cost (set before launch)
}

func (s *scaleRT) RunTask(ctx context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
	res, err := s.hierRT.RunTask(ctx, in)
	s.mu.Lock()
	res.Usage.CostUSD = s.price[in.Task.Title]
	s.mu.Unlock()
	return res, err
}

func (s *scaleRT) Synthesize(ctx context.Context, in application.SynthesizeRequest) (application.SynthesizeResponse, error) {
	s.synth.Add(1)
	return s.hierRT.Synthesize(ctx, in)
}

func TestScaleHierarchicalProjectEndToEnd(t *testing.T) {
	const budget = 10.0
	rt := &scaleRT{hierRT: &hierRT{fakeRuntime: newRT()}}
	rt.onRun = func(application.RunTaskRequest) { time.Sleep(25 * time.Millisecond) }
	var flaky atomic.Int32
	const flakyTitle, gateTitle = "Design step 3", "Support step 2"
	rt.failFn = func(title string) error {
		if title == flakyTitle && flaky.Add(1) == 1 {
			return fmt.Errorf("transient runtime failure") // W2: retried automatically
		}
		return nil
	}

	cfg := application.DefaultConfig()
	cfg.IdleDelay, cfg.RetryBase, cfg.TaskRetryBackoff = 0, time.Millisecond, time.Millisecond
	cfg.MaxParallelPerOrg = 32
	cfg.SynthTokenBudget, cfg.SynthMaxGroups = 150, 6 // small budget: the ~78 outputs must be synthesized in groups
	store := memory.New()
	if err := store.Seed(context.Background(), domainSeedOrg(cfg.BudgetUSD), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pub := &evLog{}
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: store, Pub: pub, Log: log}
	appr := application.NewApprovals(cfg, store, rec)
	q := &application.Queries{Store: store, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	orch := application.NewOrchestrator(ctx, cfg, store, rt, memory.NewLocker(), rec, appr, q, log)
	pstore := projects.NewMemStore()
	svc := projects.New(ctx, projects.Config{Store: pstore, Orch: orch, Core: store, Approvals: appr, Rec: rec, Runtime: rt,
		Catalog: catalog.Default(), OrgID: cfg.OrgID, Poll: 10 * time.Millisecond, Log: log})

	started := time.Now()
	r, err := svc.CreateDraft(ctx, projects.NewProject{Goal: "Launch the new product line in three countries", BudgetUSD: budget})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := svc.Get(ctx, r.ID)
	total := leafCount(d)
	if d.Planner == nil || d.Planner.Mode != projects.PlannerHierarchical || total < 60 {
		t.Fatalf("want a hierarchical plan with >= 60 tasks, got %+v / %d tasks", d.Planner, total)
	}

	// Gate one task on a human approval (what a project gate / approval node does).
	rec0, err := pstore.Get(ctx, cfg.OrgID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	gateID := ""
	for i, n := range rec0.Nodes {
		if n.Title == gateTitle {
			rec0.Nodes[i].ApprovalAction, rec0.Nodes[i].ApprovalRisk, gateID = "send_external", "low", n.ID
		}
	}
	if gateID == "" {
		t.Fatalf("no node titled %q", gateTitle)
	}
	if err := pstore.Put(ctx, rec0); err != nil {
		t.Fatal(err)
	}
	// Everything that does not depend (transitively) on the gated task.
	blocked := map[string]bool{gateID: true}
	for changed := true; changed; {
		changed = false
		for _, n := range d.Nodes {
			if blocked[n.ID] || n.Kind == projects.KindGroup {
				continue
			}
			for _, dep := range n.DependsOn {
				if blocked[dep] {
					blocked[n.ID], changed = true, true
					break
				}
			}
		}
	}
	independent := total - len(blocked)
	if independent < 30 {
		t.Fatalf("test setup: only %d independent tasks", independent)
	}
	// The tasks that run before the gate cost ~86% of the budget in total: the
	// 80% warning is crossed while the gate waits, the cap never is.
	rt.mu.Lock()
	rt.price = map[string]float64{}
	for _, n := range d.Nodes {
		if n.Kind != projects.KindGroup && !blocked[n.ID] {
			rt.price[n.Title] = 0.86 * budget / float64(independent)
		}
	}
	rt.mu.Unlock()

	if _, err := svc.Launch(ctx, r.ID, projects.LaunchBody{ApprovedBudgetUSD: budget, AcknowledgeUnderbudget: true, MaxParallel: 16}); err != nil {
		t.Fatal(err)
	}
	waitUntil := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if cond() {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		dd, _ := svc.Get(ctx, r.ID)
		t.Fatalf("timeout waiting for %s (done %d/%d, status %s)", what, dd.Project.TasksDone, total, dd.Project.Status)
	}

	// W1: the gated task waits for a human, the independent branches all finish.
	waitUntil("independent branches to finish while the gate waits", func() bool {
		dd, _ := svc.Get(ctx, r.ID)
		done := 0
		for _, n := range dd.Nodes {
			if n.State == "done" && !blocked[n.ID] && n.Kind != projects.KindGroup {
				done++
			}
		}
		return done == independent
	})
	dd, _ := svc.Get(ctx, r.ID)
	if st := dd.Project.Status; st == "done" || st == "failed" {
		t.Fatalf("the project must wait for the gate, status = %s", st)
	}
	waitUntil("the budget warning while the gate waits", func() bool { return pub.count("project.budget_warning") >= 1 })
	var pending []domain.Approval
	waitUntil("the gate approval", func() bool {
		all, _ := store.ListApprovals(ctx, cfg.OrgID, "")
		pending = pending[:0]
		for _, a := range all {
			if a.Status == domain.ApprovalPending {
				pending = append(pending, a)
			}
		}
		return len(pending) == 1
	})
	if _, err := appr.Decide(ctx, pending[0].ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	waitUntil("the project to finish", func() bool {
		dd, _ := svc.Get(ctx, r.ID)
		return dd.Project.Status == "done"
	})
	elapsed := time.Since(started)

	dd, _ = svc.Get(ctx, r.ID)
	if dd.Project.TasksDone != dd.Project.TasksTotal || dd.Project.Failed != 0 {
		t.Fatalf("all tasks must be done: %d/%d failed %d", dd.Project.TasksDone, dd.Project.TasksTotal, dd.Project.Failed)
	}
	for _, n := range dd.Nodes {
		if n.Kind != projects.KindGroup && n.State != "done" {
			t.Fatalf("node %q is %s", n.Title, n.State)
		}
	}
	// W2: the transient failure was retried automatically (2 runtime calls, 0 failures).
	if got := flaky.Load(); got != 2 {
		t.Fatalf("%q ran %d times, want 2 (one transient failure + one retry)", flakyTitle, got)
	}
	// W1: real parallelism (the org cap, 32, is above the project's 16).
	if rt.fakeRuntime.peak <= 4 || rt.fakeRuntime.peak > 16 {
		t.Fatalf("peak concurrent runtime calls = %d, want 5..16 (above the default max_parallel 4; the DAG width bounds it)", rt.fakeRuntime.peak)
	}
	// W3: hierarchical synthesis: more than one call, bounded by groups + final pass.
	if n := rt.synth.Load(); n < 2 || n > 7 {
		t.Fatalf("synthesis calls = %d, want 2..7 (SynthMaxGroups+1)", n)
	}
	// W5: one early warning (the monitor may still be finishing its last tick).
	time.Sleep(50 * time.Millisecond)
	if n := pub.count("project.budget_warning"); n != 1 {
		t.Fatalf("project.budget_warning emitted %d times, want exactly 1", n)
	}
	if dd.Project.SpentUSD <= 0.8*budget || dd.Project.SpentUSD >= budget {
		t.Fatalf("spent %.2f of %.2f: the test must cross 80%% without reaching the cap", dd.Project.SpentUSD, budget)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("took %s", elapsed)
	}
	t.Logf("%d tasks, peak %d, synthesis calls %d, %s", total, rt.fakeRuntime.peak, rt.synth.Load(), elapsed.Round(time.Millisecond))
}
