package application_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

func independentPlan(n int) application.PlanResponse {
	p := application.PlanResponse{Objectives: []string{"x"}}
	for i := 0; i < n; i++ {
		p.Tasks = append(p.Tasks, application.PlannedTask{Key: fmt.Sprintf("k%d", i), Title: fmt.Sprintf("T%02d", i), AgentID: "analyst"})
	}
	return p
}

// W1(a): the per-request parallelism override (a project's max_parallel) is
// applied: 16 tasks are inside the runtime at once although MAX_PARALLEL is 4.
func TestRunParallelOverrideRunsMoreThanTheDefault(t *testing.T) {
	var mu sync.Mutex
	arrived := 0
	all := make(chan struct{})
	rt := &fakeRuntime{runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		mu.Lock()
		if arrived++; arrived == 16 {
			close(all)
		}
		mu.Unlock()
		select {
		case <-all:
			return okResult("ok"), nil
		case <-time.After(5 * time.Second):
			return application.RunTaskResponse{}, fmt.Errorf("only %d tasks ran together", arrived)
		}
	}}
	h := newHarness(t, rt, func(cfg *application.Config) { cfg.MaxParallelPerOrg = 32; cfg.MaxRetries = 1 })
	id, err := h.orch.SubmitPlan(application.WithRunParallel(context.Background(), 16), "wide", independentPlan(16))
	if err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	if st := h.request(id).Status; st != domain.RequestDone {
		t.Fatalf("status = %s", st)
	}
}

// Without the override the configured MAX_PARALLEL still caps one request.
func TestRunParallelDefaultStillCaps(t *testing.T) {
	c := &concurrency{}
	rt := &fakeRuntime{runTask: func(application.RunTaskRequest) (application.RunTaskResponse, error) {
		c.enter()
		return okResult("ok"), nil
	}}
	h := newHarness(t, rt, func(cfg *application.Config) { cfg.MaxParallel = 3; cfg.MaxParallelPerOrg = 32 })
	h.orch.SubmitPlan(context.Background(), "wide", independentPlan(9))
	h.orch.Wait()
	if c.max() > 3 {
		t.Fatalf("peak = %d, want <= 3", c.max())
	}
}

// holdGate holds the tasks titled "G*" until released.
type holdGate struct{ released atomic.Bool }

func (g *holdGate) GateTask(_ context.Context, _ string, t domain.Task) application.GateDecision {
	if len(t.Title) > 0 && t.Title[0] == 'G' && !g.released.Load() {
		return application.GateDecision{Action: application.GateHold, Reason: "test"}
	}
	return application.GateDecision{}
}
func (g *holdGate) GateResolved(context.Context, string, domain.Task, string, bool) {}

// W1(b): tasks waiting on a gate do not hold a scheduler slot: with
// MAX_PARALLEL=2 and 4 held tasks listed first, the independent ones still run.
func TestHeldTasksDoNotBlockIndependentBranches(t *testing.T) {
	rt := &fakeRuntime{runTask: func(application.RunTaskRequest) (application.RunTaskResponse, error) { return okResult("ok"), nil }}
	h := newHarness(t, rt, func(cfg *application.Config) { cfg.MaxParallel = 2 })
	g := &holdGate{}
	h.orch.SetTaskGate(g)
	p := application.PlanResponse{Objectives: []string{"x"}}
	for i := 0; i < 4; i++ {
		p.Tasks = append(p.Tasks, application.PlannedTask{Key: fmt.Sprintf("g%d", i), Title: fmt.Sprintf("G%d", i), AgentID: "analyst"})
	}
	for i := 0; i < 4; i++ {
		p.Tasks = append(p.Tasks, application.PlannedTask{Key: fmt.Sprintf("i%d", i), Title: fmt.Sprintf("I%d", i), AgentID: "analyst"})
	}
	id, err := h.orch.SubmitPlan(context.Background(), "gates", p)
	if err != nil {
		t.Fatal(err)
	}
	h.waitFor("independent tasks to finish while gates are held", func() bool {
		ts := h.tasks(id)
		for i := 0; i < 4; i++ {
			if ts[fmt.Sprintf("I%d", i)].Status != domain.TaskDone {
				return false
			}
		}
		return true
	})
	g.released.Store(true)
	h.orch.Wait()
	if st := h.request(id).Status; st != domain.RequestDone {
		t.Fatalf("status = %s", st)
	}
	for i := 0; i < 4; i++ {
		if s := h.tasks(id)[fmt.Sprintf("G%d", i)].Status; s != domain.TaskDone {
			t.Fatalf("G%d = %s", i, s)
		}
	}
}

// W1 gap: the per-project max_parallel is persisted with the run meta, so a
// request resumed by restart recovery keeps it instead of using MAX_PARALLEL.
func TestRecoverRestoresRunParallelOverride(t *testing.T) {
	store, pub := newStore(t), &capture{}
	cfg := func(c *application.Config) { c.MaxParallel = 2; c.MaxParallelPerOrg = 32; c.MaxRetries = 1 }

	hold := make(chan struct{})
	var inside atomic.Int32
	p1 := startProcess(t, store, pub, &fakeRuntime{runTask: func(application.RunTaskRequest) (application.RunTaskResponse, error) {
		inside.Add(1)
		<-hold
		return application.RunTaskResponse{}, fmt.Errorf("process killed") // never completes: the task stays for Recover
	}}, cfg)
	reqID, err := p1.orch.SubmitPlan(application.WithRunParallel(context.Background(), 8), "wide", independentPlan(8))
	if err != nil {
		t.Fatal(err)
	}
	p1.waitFor("8 tasks inside the runtime", func() bool { return inside.Load() == 8 })
	go func() { time.Sleep(50 * time.Millisecond); close(hold) }()
	p1.stop() // the "crash": the request stays running in the store

	var mu sync.Mutex
	arrived := 0
	all := make(chan struct{})
	p2 := startProcess(t, store, pub, &fakeRuntime{runTask: func(application.RunTaskRequest) (application.RunTaskResponse, error) {
		mu.Lock()
		if arrived++; arrived == 8 {
			close(all)
		}
		mu.Unlock()
		select {
		case <-all:
			return okResult("ok"), nil
		case <-time.After(5 * time.Second):
			return application.RunTaskResponse{}, fmt.Errorf("only %d tasks ran together after recovery", arrived)
		}
	}}, cfg)
	if n, err := p2.orch.Recover(context.Background(), nil); err != nil || n != 1 {
		t.Fatalf("recover = %d, %v", n, err)
	}
	p2.orch.Wait()
	mu.Lock()
	n := arrived
	mu.Unlock()
	if n != 8 {
		t.Fatalf("%d tasks ran after recovery, want 8", n)
	}
	if st := p2.request(reqID).Status; st != domain.RequestDone {
		t.Fatalf("status = %s, want done (8 together needs the restored max_parallel=8, MAX_PARALLEL is 2)", st)
	}
}

// reserveOrPause yields the scheduler slot while a project task waits on a cap
// (with reminders) and takes it back on each wake-up. A capped task must neither
// leak a slot (the others would run below MAX_PARALLEL) nor take two (above it),
// across many reminder ticks, and must resume correctly when the cap is raised.
func TestBudgetPausedProjectTaskNeitherLeaksNorDoubleAcquiresSlots(t *testing.T) {
	const sales = 14
	c := &concurrency{}
	var capped atomic.Int32
	rt := &fakeRuntime{
		plan: func(application.PlanRequest) (application.PlanResponse, error) {
			p := application.PlanResponse{Objectives: []string{"x"}, Tasks: []application.PlannedTask{{Key: "cap", Title: "Capped", AgentID: "analyst"}}}
			for i := 0; i < sales; i++ {
				p.Tasks = append(p.Tasks, application.PlannedTask{Key: fmt.Sprintf("s%d", i), Title: fmt.Sprintf("S%02d", i), AgentID: "sales"})
			}
			return p, nil
		},
		runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
			r := okResult("ok")
			r.Usage = application.Usage{Model: "m", InputTokens: 10, OutputTokens: 10, CostUSD: 0.01}
			if in.Task.Title == "Capped" {
				capped.Add(1)
				r.Usage.CostUSD = 0.04
				return r, nil
			}
			c.enter() // 15 ms inside the runtime
			return r, nil
		},
	}
	h := newHarness(t, &estRuntime{fakeRuntime: rt, taskMax: 0.05, synthMax: 0.01}, func(cfg *application.Config) {
		cfg.MaxParallel, cfg.MaxParallelPerOrg, cfg.ProjectReminderEvery, cfg.ConfirmThresholdUSD = 2, 32, 5*time.Millisecond, 0
	})
	h.orch.SetTaskGate(projectOwner{})
	ctx := context.Background()
	if err := h.orch.Budget().SetCap(ctx, domain.ScopeAgent, "analyst", 0.03); err != nil {
		t.Fatal(err)
	}
	reqID, err := h.orch.Submit(ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	h.waitFor("all the other tasks done while one is cap-paused", func() bool {
		done := 0
		for title, tk := range h.tasks(reqID) {
			if title != "Capped" && tk.Status == domain.TaskDone {
				done++
			}
		}
		return done == sales
	})
	if got := h.pub.count(domain.EvBudgetExceeded); got < 5 {
		t.Fatalf("only %d budget.exceeded events: the pause must have seen several reminder ticks", got)
	}
	if p := c.max(); p != 2 {
		t.Fatalf("peak concurrency = %d, want exactly MAX_PARALLEL=2 (<2 leaks a slot, >2 double-acquires)", p)
	}
	if st := h.tasks(reqID)["Capped"].Status; st != domain.TaskPending || capped.Load() != 0 {
		t.Fatalf("the capped task must still be waiting, status=%s runs=%d", st, capped.Load())
	}
	if err := h.orch.Budget().SetCap(ctx, domain.ScopeAgent, "analyst", 1); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	if st := h.request(reqID).Status; st != domain.RequestDone {
		t.Fatalf("request = %s, want done after the cap was raised", st)
	}
	if capped.Load() != 1 || h.tasks(reqID)["Capped"].Status != domain.TaskDone {
		t.Fatalf("the capped task must run exactly once, runs=%d", capped.Load())
	}
}
