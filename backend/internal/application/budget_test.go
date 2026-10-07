package application_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// ---- helpers ----

// estRuntime adds /v1/estimate to the fake runtime with a flat range per task.
type estRuntime struct {
	*fakeRuntime
	taskMax, synthMax float64
}

func (e *estRuntime) Estimate(_ context.Context, in application.EstimateRequest) (application.EstimateResponse, error) {
	out := application.EstimateResponse{Mode: "simulation", Model: "m", Basis: domain.BasisSimulation, Currency: "USD",
		Synthesis: domain.CostRange{MinUSD: e.synthMax / 2, MaxUSD: e.synthMax}}
	var lo, hi float64
	for _, t := range in.Tasks {
		out.Tasks = append(out.Tasks, application.EstimateTaskOut{ID: t.ID, Title: t.Title, AgentID: t.AgentID, MinUSD: e.taskMax / 2, MaxUSD: e.taskMax})
		lo += e.taskMax / 2
		hi += e.taskMax
	}
	out.Total = domain.CostRange{MinUSD: lo + e.synthMax/2, MaxUSD: hi + e.synthMax}
	return out, nil
}

func costRuntime(plan func(application.PlanRequest) (application.PlanResponse, error), cost, taskMax float64) *estRuntime {
	rt := &fakeRuntime{plan: plan, runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		r := okResult("hecho: " + in.Task.Title)
		r.Usage = application.Usage{Model: "m", InputTokens: 100, OutputTokens: 100, CostUSD: cost}
		return r, nil
	}}
	return &estRuntime{fakeRuntime: rt, taskMax: taskMax, synthMax: 0.01}
}

func chainPlan(agents ...string) func(application.PlanRequest) (application.PlanResponse, error) {
	return func(application.PlanRequest) (application.PlanResponse, error) {
		var tasks []application.PlannedTask
		for i, a := range agents {
			pt := application.PlannedTask{Key: string(rune('a' + i)), Title: "T" + string(rune('A'+i)), AgentID: a}
			if i > 0 {
				pt.DependsOn = []string{string(rune('a' + i - 1))}
			}
			tasks = append(tasks, pt)
		}
		return application.PlanResponse{Tasks: tasks}, nil
	}
}

func parallelPlan(agents ...string) func(application.PlanRequest) (application.PlanResponse, error) {
	return func(application.PlanRequest) (application.PlanResponse, error) {
		var tasks []application.PlannedTask
		for i, a := range agents {
			tasks = append(tasks, application.PlannedTask{Key: string(rune('a' + i)), Title: "T" + string(rune('A'+i)), AgentID: a})
		}
		return application.PlanResponse{Tasks: tasks}, nil
	}
}

func (c *capture) first(typ string) (map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.events {
		if e.Type == typ {
			m, _ := e.Payload.(map[string]any)
			return m, true
		}
	}
	return nil, false
}

// proceed answers the cost confirmation that a small request cap triggers
// (the estimate exceeds the cap), so the test can exercise the hard cap itself.
func (h *harness) proceed(reqID string) {
	h.t.Helper()
	h.waitFor("awaiting confirmation", func() bool { return h.request(reqID).Status == domain.RequestAwaitingConfirmation })
	if err := h.orch.Budget().Confirm(context.Background(), reqID, true, 0); err != nil {
		h.t.Fatal(err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// ---- Budget.Reserve ----

func TestReserveCountsInFlightReservationsAndReleases(t *testing.T) {
	h := newHarness(t, &fakeRuntime{plan: proposalPlan()}, nil)
	ctx := context.Background()
	if err := h.store.SetBudgetCap(ctx, domain.DemoOrgID, domain.ScopeAgent, "sales", 0.10); err != nil {
		t.Fatal(err)
	}
	b := h.orch.Budget()
	r1, ex, err := b.Reserve(ctx, "req-x", "sales", 0.06)
	if err != nil || ex != nil {
		t.Fatalf("first reservation must fit: ex=%v err=%v", ex, err)
	}
	// Parallel work: the second call would exceed the cap together with the first reservation.
	_, ex, err = b.Reserve(ctx, "req-x", "sales", 0.06)
	if err != nil || ex == nil {
		t.Fatalf("second reservation must be blocked: ex=%v err=%v", ex, err)
	}
	if ex.Scope != domain.ScopeAgent || !ex.Resumable() || !near(ex.ReservedUSD, 0.06) || !near(ex.NeededUSD, 0.12) || !near(ex.CapUSD, 0.10) {
		t.Fatalf("unexpected exceeded: %+v", ex)
	}
	// Another agent is unaffected.
	if r, ex, _ := b.Reserve(ctx, "req-x", "legal", 0.06); ex != nil {
		t.Fatalf("other agents have no cap: %v", ex)
	} else {
		r.Release()
	}
	r1.Release()
	r1.Release() // idempotent
	r2, ex, _ := b.Reserve(ctx, "req-x", "sales", 0.06)
	if ex != nil {
		t.Fatalf("released budget must be available again: %v", ex)
	}
	r2.Release()
	// Exact fit is allowed (float noise tolerated).
	if r, ex, _ := b.Reserve(ctx, "req-x", "sales", 0.10); ex != nil {
		t.Fatalf("exact fit must pass: %v", ex)
	} else {
		r.Release()
	}
}

func TestReserveRequestCapAndOrgBudgetScopes(t *testing.T) {
	h := newHarness(t, &fakeRuntime{plan: proposalPlan()}, func(c *application.Config) { c.BudgetUSD = 1 })
	ctx := context.Background()
	req := domain.Request{ID: "r1", Text: "x", Status: domain.RequestRunning, CreatedAt: time.Now()}
	if err := h.store.CreateRequest(ctx, domain.DemoOrgID, req); err != nil {
		t.Fatal(err)
	}
	_ = h.store.AddCost(ctx, domain.DemoOrgID, "r1", "", 0.30)
	if err := h.orch.Budget().SetCap(ctx, domain.ScopeRequest, "r1", 0.35); err != nil {
		t.Fatal(err)
	}
	_, ex, _ := h.orch.Budget().Reserve(ctx, "r1", "sales", 0.10)
	if ex == nil || ex.Scope != domain.ScopeRequest || !near(ex.SpentUSD, 0.30) {
		t.Fatalf("request cap must block: %+v", ex)
	}
	// Without a request cap the org budget (1 USD) is the next limit and is not resumable.
	_ = h.orch.Budget().SetCap(ctx, domain.ScopeRequest, "r1", 0)
	_, ex, _ = h.orch.Budget().Reserve(ctx, "r1", "sales", 0.80)
	if ex == nil || ex.Scope != domain.ScopeOrg || ex.Resumable() {
		t.Fatalf("org budget must block and not be resumable: %+v", ex)
	}
	// Invalid caps are rejected.
	if err := h.orch.Budget().SetCap(ctx, domain.ScopeRequest, "r1", -1); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("negative cap: %v", err)
	}
	if err := h.orch.Budget().SetCap(ctx, domain.ScopeOrg, "x", 5); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("org cap is not settable here: %v", err)
	}
	if err := h.orch.Budget().SetCap(ctx, domain.ScopeAgent, "ghost", 5); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown agent: %v", err)
	}
}

func TestRecordBillsTheGreaterOfRuntimeAndBackendCost(t *testing.T) {
	h := newHarness(t, &fakeRuntime{plan: proposalPlan()}, nil)
	ctx := context.Background()
	_ = h.store.CreateRequest(ctx, domain.DemoOrgID, domain.Request{ID: "r1", Text: "x", Status: domain.RequestRunning, CreatedAt: time.Now()})
	// Runtime under-reports (0.01) a call of 1M input tokens at the default rate (3 USD/M).
	cost, err := h.orch.Budget().Record(ctx, "r1", domain.UsageEntry{AgentID: "sales", Kind: domain.UsageRunTask, Model: "x", InputTokens: 1_000_000}, 0.01)
	if err != nil || !near(cost, 3.0) {
		t.Fatalf("cost=%v err=%v, want the backend's 3.0", cost, err)
	}
	// Runtime over-reports: its (higher) number wins.
	cost, _ = h.orch.Budget().Record(ctx, "r1", domain.UsageEntry{AgentID: "sales", Kind: domain.UsageRunTask, Model: "x", InputTokens: 10}, 5)
	if !near(cost, 5) {
		t.Fatalf("cost=%v, want 5", cost)
	}
	if r, _ := h.store.GetRequest(ctx, domain.DemoOrgID, "r1"); !near(r.CostUSD, 8.0) {
		t.Fatalf("request cost = %v, want 8", r.CostUSD)
	}
}

// ---- pause / resume end to end ----

func TestRequestCapPausesWithExplanationAndResumesWhenRaised(t *testing.T) {
	rt := costRuntime(chainPlan("analyst", "sales"), 0.04, 0.05)
	h := newHarness(t, rt, nil)
	ctx := application.WithBudgetCap(context.Background(), 0.08)
	reqID, err := h.orch.Submit(ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	h.proceed(reqID)
	h.waitFor("request paused", func() bool { return h.request(reqID).Status == domain.RequestPaused })

	ts := h.tasks(reqID)
	if ts["TA"].Status != domain.TaskDone || ts["TB"].Status != domain.TaskPending {
		t.Fatalf("A done and B waiting, got %s / %s", ts["TA"].Status, ts["TB"].Status)
	}
	p, ok := h.pub.first(domain.EvBudgetExceeded)
	if !ok {
		t.Fatal("a pause must publish budget.exceeded (never a silent stop)")
	}
	if p["scope"] != domain.ScopeRequest || p["resumable"] != true || p["request_id"] != reqID || p["message"] == "" {
		t.Fatalf("payload not explanatory: %+v", p)
	}
	if !near(p["cap_usd"].(float64), 0.08) || !near(p["spent_usd"].(float64), 0.04) || !near(p["needed_usd"].(float64), 0.09) {
		t.Fatalf("numbers: %+v", p)
	}
	if h.pub.count(domain.EvRequestStatus) == 0 {
		t.Error("entering paused must publish request.status_changed")
	}
	if !h.hasAudit("budget.exceeded") {
		t.Error("missing audit")
	}
	// The hard cap held: nothing beyond what was spent.
	if c := h.request(reqID).CostUSD; c > 0.08 {
		t.Fatalf("cost %v exceeds the cap", c)
	}

	// "Raise the cap and continue".
	if err := h.orch.Budget().SetCap(context.Background(), domain.ScopeRequest, reqID, 1); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	req := h.request(reqID)
	if req.Status != domain.RequestDone || req.ReportID == nil {
		t.Fatalf("after raising the cap the request must finish: %+v", req)
	}
	if h.pub.count(domain.EvBudgetResumed) != 1 {
		t.Errorf("budget.resumed count = %d", h.pub.count(domain.EvBudgetResumed))
	}
	if !near(req.CostUSD, 0.08) {
		t.Errorf("cost = %v, want 0.08", req.CostUSD)
	}
}

func TestAgentCapPausesOnlyThatAgent(t *testing.T) {
	rt := costRuntime(parallelPlan("analyst", "sales"), 0.04, 0.05)
	h := newHarness(t, rt, nil)
	if err := h.orch.Budget().SetCap(context.Background(), domain.ScopeAgent, "analyst", 0.03); err != nil {
		t.Fatal(err)
	}
	reqID, _ := h.orch.Submit(context.Background(), "x")
	h.waitFor("sales done and analyst blocked", func() bool {
		ts := h.tasks(reqID)
		return len(ts) == 2 && ts["TB"].Status == domain.TaskDone && h.agent("analyst").State == domain.StateBlocked
	})
	if ts := h.tasks(reqID); ts["TA"].Status != domain.TaskPending {
		t.Fatalf("the capped agent's task must wait, got %s", ts["TA"].Status)
	}
	p, ok := h.pub.first(domain.EvBudgetExceeded)
	if !ok || p["scope"] != domain.ScopeAgent || p["agent_id"] != "analyst" || p["resumable"] != true {
		t.Fatalf("payload: %+v", p)
	}
	st, err := h.orch.Budget().Status(context.Background())
	if err != nil || len(st.Pauses) != 1 || st.Pauses[0].Scope != domain.ScopeAgent {
		t.Fatalf("status pauses: %+v err=%v", st.Pauses, err)
	}
	if err := h.orch.Budget().SetCap(context.Background(), domain.ScopeAgent, "analyst", 1); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	if h.request(reqID).Status != domain.RequestDone {
		t.Fatalf("request = %s", h.request(reqID).Status)
	}
	if h.pub.count(domain.EvBudgetResumed) != 1 {
		t.Errorf("resumed count = %d", h.pub.count(domain.EvBudgetResumed))
	}
}

func TestParallelTasksNeverOverspendTheRequestCap(t *testing.T) {
	// Two parallel tasks, each reserves 0.05 and costs 0.04; the cap is 0.06.
	rt := costRuntime(parallelPlan("analyst", "sales"), 0.04, 0.05)
	h := newHarness(t, rt, nil)
	reqID, _ := h.orch.Submit(application.WithBudgetCap(context.Background(), 0.06), "x")
	h.proceed(reqID)
	h.waitFor("one task done and the request paused", func() bool {
		return h.request(reqID).Status == domain.RequestPaused
	})
	time.Sleep(50 * time.Millisecond)
	if c := h.request(reqID).CostUSD; c > 0.06+1e-9 {
		t.Fatalf("spent %v over the 0.06 cap", c)
	}
	done := 0
	for _, tk := range h.tasks(reqID) {
		if tk.Status == domain.TaskDone {
			done++
		}
	}
	if done != 1 {
		t.Fatalf("exactly one task fits under the cap, got %d", done)
	}
}

func TestPauseTimesOutWithVisibleFailure(t *testing.T) {
	rt := costRuntime(chainPlan("analyst"), 0.04, 0.05)
	h := newHarness(t, rt, func(c *application.Config) { c.PauseTimeout = 50 * time.Millisecond })
	reqID, _ := h.orch.Submit(application.WithBudgetCap(context.Background(), 0.01), "x")
	h.proceed(reqID)
	h.orch.Wait()
	if st := h.request(reqID).Status; st != domain.RequestFailed {
		t.Fatalf("status = %s, want failed after the pause timeout", st)
	}
	if h.pub.count(domain.EvError) == 0 {
		t.Error("the timeout must surface as an error event")
	}
	if _, ok := h.pub.first(domain.EvBudgetExceeded); !ok {
		t.Error("budget.exceeded must be published before waiting")
	}
}

func TestDefaultRequestCapComesFromConfig(t *testing.T) {
	rt := costRuntime(chainPlan("analyst"), 0.04, 0.05)
	h := newHarness(t, rt, func(c *application.Config) { c.RequestBudgetCapUSD = 0.02; c.PauseTimeout = time.Second })
	reqID, _ := h.orch.Submit(context.Background(), "x")
	h.proceed(reqID)
	h.waitFor("paused by the default cap", func() bool { return h.request(reqID).Status == domain.RequestPaused })
	caps, _ := h.store.ListBudgetCaps(context.Background(), domain.DemoOrgID)
	if len(caps) != 1 || caps[0].Scope != domain.ScopeRequest || !near(caps[0].CapUSD, 0.02) {
		t.Fatalf("caps = %+v", caps)
	}
}

// ---- estimate and confirmation ----

func TestEstimateIsPublishedAsRangeWithoutGateWhenBelowThreshold(t *testing.T) {
	rt := costRuntime(chainPlan("analyst", "sales"), 0.01, 0.05)
	h := newHarness(t, rt, nil)
	reqID, _ := h.orch.Submit(context.Background(), "x")
	h.orch.Wait()
	p, ok := h.pub.first(domain.EvCostEstimated)
	if !ok {
		t.Fatal("missing cost.estimated")
	}
	est := p["estimate"].(domain.CostEstimate)
	if est.RequestID != reqID || len(est.Tasks) != 2 || est.RequiresConfirmation {
		t.Fatalf("estimate = %+v", est)
	}
	if !(est.Total.MinUSD > 0 && est.Total.MinUSD < est.Total.MaxUSD) || est.Basis != domain.BasisSimulation {
		t.Fatalf("must be a range: %+v", est.Total)
	}
	if got, ok := h.orch.Budget().Estimate(context.Background(), reqID); !ok || got.Total != est.Total {
		t.Fatal("estimate must be retrievable")
	}
	if h.request(reqID).Status != domain.RequestDone {
		t.Fatalf("status = %s", h.request(reqID).Status)
	}
}

func TestEstimateAboveThresholdWaitsForConfirmation(t *testing.T) {
	rt := costRuntime(chainPlan("analyst", "sales"), 0.01, 0.80) // max total 1.61 > 1.0
	h := newHarness(t, rt, nil)
	reqID, _ := h.orch.Submit(context.Background(), "x")
	h.waitFor("awaiting confirmation", func() bool { return h.request(reqID).Status == domain.RequestAwaitingConfirmation })
	p, _ := h.pub.first(domain.EvCostEstimated)
	est := p["estimate"].(domain.CostEstimate)
	if !est.RequiresConfirmation || est.ConfirmReason != domain.ConfirmReasonThreshold || est.ThresholdUSD != 1.0 {
		t.Fatalf("estimate = %+v", est)
	}
	for _, tk := range h.tasks(reqID) {
		if tk.Status != domain.TaskPending {
			t.Fatalf("no task may start before the confirmation, %s is %s", tk.Title, tk.Status)
		}
	}
	if err := h.orch.Budget().Confirm(context.Background(), reqID, true, 0); err != nil {
		t.Fatal(err)
	}
	if err := h.orch.Budget().Confirm(context.Background(), reqID, true, 0); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a second answer must conflict, got %v", err)
	}
	h.orch.Wait()
	if h.request(reqID).Status != domain.RequestDone {
		t.Fatalf("status = %s", h.request(reqID).Status)
	}
}

func TestConfirmWithCapAppliesTheCap(t *testing.T) {
	rt := costRuntime(chainPlan("analyst"), 0.01, 1.20)
	h := newHarness(t, rt, nil)
	reqID, _ := h.orch.Submit(context.Background(), "x")
	h.waitFor("awaiting confirmation", func() bool { return h.request(reqID).Status == domain.RequestAwaitingConfirmation })
	if err := h.orch.Budget().Confirm(context.Background(), reqID, true, 2.5); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	caps, _ := h.store.ListBudgetCaps(context.Background(), domain.DemoOrgID)
	if len(caps) != 1 || !near(caps[0].CapUSD, 2.5) {
		t.Fatalf("caps = %+v", caps)
	}
}

func TestEstimateAboveRequestCapAsksForConfirmationAndCancelBlocksTasks(t *testing.T) {
	rt := costRuntime(chainPlan("analyst", "sales"), 0.01, 0.05) // max total 0.11 > cap 0.10
	h := newHarness(t, rt, nil)
	reqID, _ := h.orch.Submit(application.WithBudgetCap(context.Background(), 0.10), "x")
	h.waitFor("awaiting confirmation", func() bool { return h.request(reqID).Status == domain.RequestAwaitingConfirmation })
	p, _ := h.pub.first(domain.EvCostEstimated)
	if est := p["estimate"].(domain.CostEstimate); est.ConfirmReason != domain.ConfirmReasonCap {
		t.Fatalf("reason = %q", est.ConfirmReason)
	}
	if err := h.orch.Budget().Confirm(context.Background(), reqID, false, 0); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	if h.request(reqID).Status != domain.RequestFailed {
		t.Fatalf("status = %s", h.request(reqID).Status)
	}
	for _, tk := range h.tasks(reqID) {
		if tk.Status != domain.TaskBlocked {
			t.Fatalf("task %s = %s, want blocked", tk.Title, tk.Status)
		}
	}
	if c := h.request(reqID).CostUSD; c != 0 {
		t.Fatalf("a cancelled request must cost nothing, got %v", c)
	}
}

func TestFallbackEstimateNeverGatesTheRequest(t *testing.T) {
	// fakeRuntime has no /v1/estimate: the backend uses coarse constants and must not block.
	h := newHarness(t, &fakeRuntime{plan: proposalPlan()}, func(c *application.Config) { c.ConfirmThresholdUSD = 0.0001 })
	reqID, _ := h.orch.Submit(context.Background(), "x")
	h.orch.Wait()
	p, ok := h.pub.first(domain.EvCostEstimated)
	if !ok {
		t.Fatal("missing cost.estimated")
	}
	est := p["estimate"].(domain.CostEstimate)
	if est.Basis != domain.BasisFallback || est.RequiresConfirmation || est.Total.MinUSD >= est.Total.MaxUSD {
		t.Fatalf("estimate = %+v", est)
	}
	if h.request(reqID).Status == domain.RequestAwaitingConfirmation {
		t.Fatal("fallback must not gate")
	}
}

func TestConfirmUnknownOrNotWaitingRequest(t *testing.T) {
	h := newHarness(t, &fakeRuntime{plan: proposalPlan()}, nil)
	if err := h.orch.Budget().Confirm(context.Background(), "nope", true, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

// ---- breakdown ----

func TestCostBreakdownAggregatesLedger(t *testing.T) {
	h := newHarness(t, &fakeRuntime{plan: proposalPlan()}, nil)
	ctx := context.Background()
	org := domain.DemoOrgID
	_ = h.store.CreateRequest(ctx, org, domain.Request{ID: "r1", Text: "x", Status: domain.RequestRunning, CreatedAt: time.Now()})
	_ = h.store.CreateTask(ctx, org, domain.Task{ID: "t1", RequestID: "r1", Title: "Preparar", AgentID: "sales", Status: domain.TaskDone, CreatedAt: time.Now()})
	add := func(u domain.UsageEntry) {
		u.RequestID = "r1"
		_ = h.store.AddCost(ctx, org, "r1", u.TaskID, u.CostUSD)
		if err := h.store.AddUsage(ctx, org, u); err != nil {
			t.Fatal(err)
		}
	}
	add(domain.UsageEntry{ID: "u1", TaskID: "t1", AgentID: "sales", Kind: domain.UsageRunTask, Model: "m", InputTokens: 10, OutputTokens: 5, CostUSD: 0.30, Tools: []string{"email.send", "crm.update"}})
	add(domain.UsageEntry{ID: "u2", TaskID: "t1", AgentID: "legal", Kind: domain.UsageConsult, Model: "m", CostUSD: 0.10})
	add(domain.UsageEntry{ID: "u3", AgentID: "assistant", Kind: domain.UsageSynthesize, Model: "m", CostUSD: 0.20})
	// Cost recorded before the ledger existed has no breakdown and must be reported as such.
	_ = h.store.AddCost(ctx, org, "r1", "", 0.50)

	b, err := h.q.CostBreakdown(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !near(b.TotalUSD, 0.60) || b.Calls != 3 || !near(b.UntrackedUSD, 0.50) {
		t.Fatalf("totals: %+v", b)
	}
	if len(b.ByAgent) != 3 || b.ByAgent[0].AgentID != "sales" || !near(b.ByAgent[0].CostUSD, 0.30) || b.ByAgent[0].InputTokens != 10 {
		t.Fatalf("by agent: %+v", b.ByAgent)
	}
	if len(b.ByTask) != 1 || b.ByTask[0].Title != "Preparar" || !near(b.ByTask[0].CostUSD, 0.40) || b.ByTask[0].Calls != 2 {
		t.Fatalf("by task: %+v", b.ByTask)
	}
	if len(b.ByTool) != 2 || !near(b.ByTool[0].CostUSD, 0.15) || !near(b.LLMOnlyUSD, 0.30) {
		t.Fatalf("by tool: %+v llm-only=%v", b.ByTool, b.LLMOnlyUSD)
	}
	if len(b.ByOperation) != 3 || b.ByOperation[0].Kind != domain.UsageRunTask {
		t.Fatalf("by operation: %+v", b.ByOperation)
	}
	one, _ := h.q.CostBreakdown(ctx, "other")
	if one.Calls != 0 || one.TotalUSD != 0 {
		t.Fatalf("filter by request: %+v", one)
	}
}

func TestResetClearsLedgerAndRequestCapsButKeepsAgentCaps(t *testing.T) {
	h := newHarness(t, &fakeRuntime{plan: proposalPlan()}, nil)
	ctx := context.Background()
	org := domain.DemoOrgID
	_ = h.store.SetBudgetCap(ctx, org, domain.ScopeAgent, "sales", 5)
	_ = h.store.SetBudgetCap(ctx, org, domain.ScopeRequest, "r1", 1)
	_ = h.store.AddUsage(ctx, org, domain.UsageEntry{ID: "u", RequestID: "r1", AgentID: "sales", Kind: domain.UsageRunTask, CostUSD: 1})
	if err := h.orch.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if u, _ := h.store.ListUsage(ctx, org); len(u) != 0 {
		t.Fatalf("usage after reset: %d", len(u))
	}
	caps, _ := h.store.ListBudgetCaps(ctx, org)
	if len(caps) != 1 || caps[0].Scope != domain.ScopeAgent {
		t.Fatalf("caps after reset: %+v", caps)
	}
}
