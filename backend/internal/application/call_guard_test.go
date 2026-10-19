package application_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// liveEst is an estimator that reports a live (token heuristic) estimate and a fake runtime that records the
// max_cost_usd of every call, so the pre-call guard can be tested without any model.
type liveEst struct {
	*fakeRuntime
	taskMax float64
	mu      sync.Mutex
	allowed []float64
	calls   int
}

func (e *liveEst) Estimate(_ context.Context, in application.EstimateRequest) (application.EstimateResponse, error) {
	out := application.EstimateResponse{Mode: "live", Model: "deepseek-chat", Basis: domain.BasisTokenHeuristic, Currency: "USD",
		Synthesis: domain.CostRange{MinUSD: 0.0001, MaxUSD: 0.001}}
	for _, t := range in.Tasks {
		out.Tasks = append(out.Tasks, application.EstimateTaskOut{ID: t.ID, Title: t.Title, AgentID: t.AgentID, MinUSD: e.taskMax / 2, MaxUSD: e.taskMax})
		out.Total.MaxUSD += e.taskMax
	}
	out.Total.MaxUSD += out.Synthesis.MaxUSD
	return out, nil
}

func (e *liveEst) RunTask(ctx context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
	e.mu.Lock()
	e.allowed = append(e.allowed, in.MaxCostUSD)
	e.calls++
	e.mu.Unlock()
	return e.fakeRuntime.RunTask(ctx, in)
}

func liveRuntime(taskMax float64, usageCost float64) *liveEst {
	rt := &fakeRuntime{plan: parallelPlan("analyst"), runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		r := okResult("hecho: " + in.Task.Title)
		r.Usage = application.Usage{Model: "deepseek-chat", InputTokens: 1000, OutputTokens: 500, CostUSD: usageCost}
		return r, nil
	}}
	return &liveEst{fakeRuntime: rt, taskMax: taskMax}
}

func TestLiveCallCarriesTheReservedAllowance(t *testing.T) {
	rt := liveRuntime(0.02, 0.001)
	h := newHarness(t, rt, nil)
	reqID, err := h.orch.Submit(context.Background(), "calcula el margen")
	if err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	if st := h.request(reqID).Status; st != domain.RequestDone {
		t.Fatalf("status %s", st)
	}
	if len(rt.allowed) != 1 || rt.allowed[0] <= 0 {
		t.Fatalf("the live call must carry max_cost_usd (the reservation plus slack): %v", rt.allowed)
	}
}

func TestSimulationCallsCarryNoAllowance(t *testing.T) {
	rt := costRuntime(parallelPlan("analyst"), 0.001, 0.02)
	var seen []float64
	var mu sync.Mutex
	rt.fakeRuntime.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		mu.Lock()
		seen = append(seen, in.MaxCostUSD)
		mu.Unlock()
		r := okResult("hecho")
		r.Usage = application.Usage{Model: "m", InputTokens: 100, OutputTokens: 100, CostUSD: 0.001}
		return r, nil
	}
	h := newHarness(t, rt, nil)
	if _, err := h.orch.Submit(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	if len(seen) != 1 || seen[0] != 0 {
		t.Fatalf("simulated runs keep the old behaviour: %v", seen)
	}
}

func TestHugeLivePayloadPausesBeforeTheCallInsteadOfOvershooting(t *testing.T) {
	// The per-task estimate says 0.002, so the old check passed; the real payload (a big task description)
	// costs more than the request cap, so the call must NOT be made.
	rt := liveRuntime(0.002, 0.001)
	rt.fakeRuntime.plan = func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "T", AgentID: "analyst",
			Description: strings.Repeat("dato ", 60000)}}}, nil // ~300 KB ~ 85k tokens
	}
	h := newHarness(t, rt, nil)
	ctx := application.WithBudgetCap(context.Background(), 0.01)
	reqID, err := h.orch.Submit(ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	h.waitFor("request paused", func() bool { return h.request(reqID).Status == domain.RequestPaused })
	rt.mu.Lock()
	calls := rt.calls
	rt.mu.Unlock()
	if calls != 0 {
		t.Fatalf("the model was called %d times although the call would cross the cap", calls)
	}
	p, ok := h.pub.first(domain.EvBudgetExceeded)
	if !ok || p["scope"] != domain.ScopeRequest || p["resumable"] != true {
		t.Fatalf("budget.exceeded must explain the pause: %+v", p)
	}
	if !(p["needed_usd"].(float64) > p["cap_usd"].(float64)) {
		t.Fatalf("numbers: %+v", p)
	}
	// Raising the cap lets it continue.
	if err := h.orch.Budget().SetCap(context.Background(), domain.ScopeRequest, reqID, 5); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	if st := h.request(reqID).Status; st != domain.RequestDone {
		t.Fatalf("after raising the cap the request must finish, got %s", st)
	}
}

func TestBreakdownReportsPerCallTokens(t *testing.T) {
	h := newHarness(t, &fakeRuntime{plan: proposalPlan()}, nil)
	ctx := context.Background()
	org := domain.DemoOrgID
	_ = h.store.CreateRequest(ctx, org, domain.Request{ID: "r1", Text: "x", Status: domain.RequestRunning, CreatedAt: time.Now()})
	_ = h.store.CreateTask(ctx, org, domain.Task{ID: "t1", RequestID: "r1", Title: "Preparar", AgentID: "sales", Status: domain.TaskDone, CreatedAt: time.Now()})
	now := time.Now()
	for i, u := range []domain.UsageEntry{
		{ID: "u1", TaskID: "t1", AgentID: "sales", Kind: domain.UsageRunTask, Model: "m", InputTokens: 1200, OutputTokens: 300, CostUSD: 0.30},
		{ID: "u2", TaskID: "t1", AgentID: "legal", Kind: domain.UsageConsult, Model: "m", InputTokens: 400, OutputTokens: 80, CostUSD: 0.10},
	} {
		u.RequestID, u.TS = "r1", now.Add(time.Duration(i)*time.Second)
		_ = h.store.AddCost(ctx, org, "r1", u.TaskID, u.CostUSD)
		if err := h.store.AddUsage(ctx, org, u); err != nil {
			t.Fatal(err)
		}
	}
	b, err := h.q.CostBreakdown(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if b.InputTokens != 1600 || b.OutputTokens != 380 {
		t.Fatalf("token totals: %+v", b)
	}
	if len(b.ByCall) != 2 || b.ByCall[0].Kind != domain.UsageRunTask || b.ByCall[0].InputTokens != 1200 || b.ByCall[1].OutputTokens != 80 {
		t.Fatalf("per call: %+v", b.ByCall)
	}
	if len(b.ByTask) != 1 || b.ByTask[0].InputTokens != 1600 || b.ByTask[0].OutputTokens != 380 {
		t.Fatalf("by task: %+v", b.ByTask)
	}
	if len(b.ByOperation) != 2 || len(b.ByModel) != 1 || b.ByModel[0].InputTokens != 1600 {
		t.Fatalf("by operation/model: %+v %+v", b.ByOperation, b.ByModel)
	}
	if all, _ := h.q.CostBreakdown(ctx, ""); len(all.ByCall) != 0 {
		t.Fatal("the organization-wide breakdown does not list every call")
	}
}
