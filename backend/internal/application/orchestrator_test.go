package application_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/roles"
)

// ---- fakes ----

type fakeRuntime struct {
	plan    func(application.PlanRequest) (application.PlanResponse, error)
	runTask func(application.RunTaskRequest) (application.RunTaskResponse, error)
}

func (f *fakeRuntime) Plan(_ context.Context, in application.PlanRequest) (application.PlanResponse, error) {
	return f.plan(in)
}

func (f *fakeRuntime) RunTask(_ context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
	if f.runTask != nil {
		return f.runTask(in)
	}
	return okResult("hecho: " + in.Task.Title), nil
}

func (f *fakeRuntime) Consult(_ context.Context, in application.ConsultRequest) (application.ConsultResponse, error) {
	return application.ConsultResponse{Answer: "respuesta de " + in.ToAgentID}, nil
}

func (f *fakeRuntime) Synthesize(_ context.Context, in application.SynthesizeRequest) (application.SynthesizeResponse, error) {
	return application.SynthesizeResponse{Title: "Informe", Summary: "ok", Sections: []domain.Section{{Heading: "Resumen", Body: "x"}}}, nil
}

func (f *fakeRuntime) Health(context.Context) (string, error) { return "simulation", nil }

func okResult(summary string) application.RunTaskResponse {
	return application.RunTaskResponse{Output: domain.StructuredOutput{Summary: summary, Confidence: 0.9}}
}

type capture struct {
	mu     sync.Mutex
	events []domain.Event
}

func (c *capture) Publish(_ context.Context, e domain.Event) error {
	c.mu.Lock()
	c.events = append(c.events, e)
	c.mu.Unlock()
	return nil
}

func (c *capture) count(typ string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

type harness struct {
	t     *testing.T
	store *memory.Store
	pub   *capture
	orch  *application.Orchestrator
	appr  *application.Approvals
	q     *application.Queries
}

func newHarness(t *testing.T, rt application.Runtime, mutate func(*application.Config)) *harness {
	t.Helper()
	cfg := application.DefaultConfig()
	cfg.IdleDelay, cfg.RetryBase, cfg.MaxRetries = 0, time.Millisecond, 3
	if mutate != nil {
		mutate(&cfg)
	}
	store := memory.New()
	if err := store.Seed(context.Background(), domain.SeedOrg(cfg.BudgetUSD), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	pub := &capture{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: store, Pub: pub, Log: log}
	appr := application.NewApprovals(cfg, store, rec)
	q := &application.Queries{Store: store, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	orch := application.NewOrchestrator(ctx, cfg, store, rt, memory.NewLocker(), rec, appr, q, log)
	return &harness{t, store, pub, orch, appr, q}
}

func (h *harness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("timeout waiting for %s", what)
}

func (h *harness) request(id string) domain.Request {
	r, err := h.store.GetRequest(context.Background(), domain.DemoOrgID, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

func (h *harness) tasks(reqID string) map[string]domain.Task {
	ts, _ := h.store.ListTasksByRequest(context.Background(), domain.DemoOrgID, reqID)
	out := map[string]domain.Task{}
	for _, t := range ts {
		out[t.Title] = t
	}
	return out
}

func (h *harness) agent(id string) domain.Agent {
	a, err := h.store.GetAgent(context.Background(), domain.DemoOrgID, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return a
}

// awaitApproval waits until an approval is pending and its agent has settled
// in awaiting_approval (the approval row is written before the agent state).
func (h *harness) awaitApproval() domain.Approval {
	h.t.Helper()
	var ap domain.Approval
	h.waitFor("pending approval", func() bool {
		var ok bool
		ap, ok = h.pendingApproval()
		return ok && h.agent(ap.AgentID).State == domain.StateAwaitingApproval
	})
	return ap
}

func (h *harness) pendingApproval() (domain.Approval, bool) {
	ps, _ := h.store.ListApprovals(context.Background(), domain.DemoOrgID, "pending")
	if len(ps) == 0 {
		return domain.Approval{}, false
	}
	return ps[0], true
}

func (h *harness) hasAudit(action string) bool {
	for _, a := range h.store.Audit() {
		if a.Action == action {
			return true
		}
	}
	return false
}

func proposalPlan(extra ...application.PlannedTask) func(application.PlanRequest) (application.PlanResponse, error) {
	return func(application.PlanRequest) (application.PlanResponse, error) {
		tasks := []application.PlannedTask{
			{Key: "a", Title: "Preparar propuesta", AgentID: "sales"},
			{Key: "b", Title: "Revisar margen", AgentID: "accounting", DependsOn: []string{"a"}},
		}
		return application.PlanResponse{Objectives: []string{"enviar propuesta"}, Tasks: append(tasks, extra...)}, nil
	}
}

func sendProposalRuntime(plan func(application.PlanRequest) (application.PlanResponse, error)) *fakeRuntime {
	return &fakeRuntime{plan: plan, runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		res := okResult("hecho: " + in.Task.Title)
		if in.Task.Title == "Preparar propuesta" {
			res.Consults = []application.ConsultRequestItem{{ToAgentID: "legal", Question: "¿cláusulas?"}}
			res.ToolRequests = []application.ToolRequest{{Tool: "email", Action: "send_proposal", Risk: "medium", Args: map[string]any{"to": "cliente"}}}
		}
		return res, nil
	}}
}

// ---- tests ----

func TestApprovalApprovedContinuesAndProducesReport(t *testing.T) {
	h := newHarness(t, sendProposalRuntime(proposalPlan()), nil)
	reqID, err := h.orch.Submit(context.Background(), "enviar propuesta de $50,000")
	if err != nil {
		t.Fatal(err)
	}
	var ap domain.Approval
	ap = h.awaitApproval()

	if ap.Action != "send_proposal" || ap.AgentID != "sales" {
		t.Fatalf("unexpected approval %+v", ap)
	}
	ts := h.tasks(reqID)
	if ts["Preparar propuesta"].Status != domain.TaskAwaitingApproval {
		t.Fatalf("task status = %s", ts["Preparar propuesta"].Status)
	}
	if ts["Revisar margen"].Status != domain.TaskPending {
		t.Fatalf("dependant must wait, got %s", ts["Revisar margen"].Status)
	}
	if h.agent("sales").State != domain.StateAwaitingApproval {
		t.Fatalf("agent state = %s", h.agent("sales").State)
	}
	if got := h.request(reqID).Status; got != domain.RequestAwaitingApproval {
		t.Fatalf("request status = %s", got)
	}

	if _, err := h.appr.Decide(context.Background(), ap.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.appr.Decide(context.Background(), ap.ID, "approve", ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second decision should conflict, got %v", err)
	}
	h.orch.Wait()

	req := h.request(reqID)
	if req.Status != domain.RequestDone || req.ReportID == nil {
		t.Fatalf("request = %+v", req)
	}
	for title, task := range h.tasks(reqID) {
		if task.Status != domain.TaskDone {
			t.Fatalf("task %q = %s", title, task.Status)
		}
	}
	for _, typ := range []string{domain.EvPlanCreated, domain.EvApprovalRequest, domain.EvApprovalResolved, domain.EvReportCreated, domain.EvRequestCompleted} {
		if h.pub.count(typ) != 1 {
			t.Errorf("event %s count = %d, want 1", typ, h.pub.count(typ))
		}
	}
	if h.pub.count(domain.EvTaskCreated) != 2 || h.pub.count(domain.EvTaskCompleted) != 2 {
		t.Errorf("task events created=%d completed=%d", h.pub.count(domain.EvTaskCreated), h.pub.count(domain.EvTaskCompleted))
	}
	// consult produced consult + answer messages between agents
	convs, _ := h.store.ListConversations(context.Background(), domain.DemoOrgID)
	msgs, _ := h.store.ListMessages(context.Background(), domain.DemoOrgID, convs[0].ID)
	kinds := map[string]int{}
	for _, m := range msgs {
		kinds[m.Kind]++
	}
	if kinds["consult"] != 1 || kinds["answer"] != 1 || kinds["delegation"] != 2 {
		t.Errorf("message kinds = %v", kinds)
	}
	for _, a := range []string{"approval.approved", "tool.executed", "request.received"} {
		if !h.hasAudit(a) {
			t.Errorf("missing audit action %s", a)
		}
	}
}

func TestApprovalRejectedBlocksTaskAndDependants(t *testing.T) {
	independent := application.PlannedTask{Key: "c", Title: "Analizar rentabilidad", AgentID: "analyst"}
	h := newHarness(t, sendProposalRuntime(proposalPlan(independent)), nil)
	reqID, _ := h.orch.Submit(context.Background(), "enviar propuesta")
	var ap domain.Approval
	ap = h.awaitApproval()

	if _, err := h.appr.Decide(context.Background(), ap.ID, "reject", "no"); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()

	ts := h.tasks(reqID)
	if ts["Preparar propuesta"].Status != domain.TaskBlocked || ts["Revisar margen"].Status != domain.TaskBlocked {
		t.Fatalf("statuses: %s / %s", ts["Preparar propuesta"].Status, ts["Revisar margen"].Status)
	}
	if ts["Analizar rentabilidad"].Status != domain.TaskDone {
		t.Fatalf("independent task = %s", ts["Analizar rentabilidad"].Status)
	}
	if h.pub.count(domain.EvTaskBlocked) != 2 {
		t.Errorf("task.blocked events = %d", h.pub.count(domain.EvTaskBlocked))
	}
	rep, _ := h.store.ListReports(context.Background(), domain.DemoOrgID)
	if len(rep) != 1 || rep[0].Sections[len(rep[0].Sections)-1].Heading != "Tareas no completadas" {
		t.Fatalf("partial report expected, got %+v", rep)
	}
	if h.hasAudit("tool.executed") {
		t.Error("rejected tool must not be executed")
	}
}

func TestInvalidDecisionAndUnknownApproval(t *testing.T) {
	h := newHarness(t, &fakeRuntime{}, nil)
	if _, err := h.appr.Decide(context.Background(), "nope", "approve", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if _, err := h.appr.Decide(context.Background(), "nope", "maybe", ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("want invalid, got %v", err)
	}
}

func TestHighRiskNeedsApprovalEvenIfActionNotListed(t *testing.T) {
	rt := &fakeRuntime{
		plan: func(application.PlanRequest) (application.PlanResponse, error) {
			return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "Contratar", AgentID: "hr"}}}, nil
		},
		runTask: func(application.RunTaskRequest) (application.RunTaskResponse, error) {
			r := okResult("x")
			r.ToolRequests = []application.ToolRequest{{Tool: "ats", Action: "post_job", Risk: "high"}}
			return r, nil
		},
	}
	h := newHarness(t, rt, nil)
	h.orch.Submit(context.Background(), "contratar")
	var ap domain.Approval
	ap = h.awaitApproval()
	if ap.Risk != "high" {
		t.Fatalf("risk = %s", ap.Risk)
	}
	h.appr.Decide(context.Background(), ap.ID, "approve", "")
	h.orch.Wait()
}

func TestRuntimeDownDegradesToVisibleError(t *testing.T) {
	rt := &fakeRuntime{plan: func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{}, errors.New("connection refused")
	}}
	h := newHarness(t, rt, nil)
	reqID, err := h.orch.Submit(context.Background(), "hola")
	if err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	if got := h.request(reqID).Status; got != domain.RequestFailed {
		t.Fatalf("request status = %s", got)
	}
	if st := h.agent("assistant").State; st != domain.StateError {
		t.Fatalf("assistant state = %s", st)
	}
	if h.pub.count(domain.EvError) == 0 {
		t.Fatal("expected an error event")
	}
}

func TestTaskFailureAfterRetriesMarksAgentError(t *testing.T) {
	var calls atomic.Int32
	rt := &fakeRuntime{
		plan: func(application.PlanRequest) (application.PlanResponse, error) {
			return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "Analizar", AgentID: "analyst"}}}, nil
		},
		runTask: func(application.RunTaskRequest) (application.RunTaskResponse, error) {
			calls.Add(1)
			return application.RunTaskResponse{}, errors.New("boom")
		},
	}
	h := newHarness(t, rt, nil)
	reqID, _ := h.orch.Submit(context.Background(), "analiza")
	h.orch.Wait()
	if calls.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", calls.Load())
	}
	if h.tasks(reqID)["Analizar"].Status != domain.TaskFailed || h.agent("analyst").State != domain.StateError {
		t.Fatal("task must be failed and agent in error")
	}
	if h.request(reqID).Status != domain.RequestFailed {
		t.Fatalf("request = %s", h.request(reqID).Status)
	}
}

func TestRetrySucceedsAfterTransientError(t *testing.T) {
	var calls atomic.Int32
	rt := &fakeRuntime{
		plan: func(application.PlanRequest) (application.PlanResponse, error) {
			return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "Analizar", AgentID: "analyst"}}}, nil
		},
		runTask: func(application.RunTaskRequest) (application.RunTaskResponse, error) {
			if calls.Add(1) < 3 {
				return application.RunTaskResponse{}, errors.New("temporal")
			}
			return okResult("ok"), nil
		},
	}
	h := newHarness(t, rt, nil)
	reqID, _ := h.orch.Submit(context.Background(), "analiza")
	h.orch.Wait()
	if h.request(reqID).Status != domain.RequestDone {
		t.Fatalf("request = %s", h.request(reqID).Status)
	}
}

func TestBudgetStopsFurtherRuntimeCalls(t *testing.T) {
	rt := &fakeRuntime{
		plan: func(application.PlanRequest) (application.PlanResponse, error) {
			return application.PlanResponse{Tasks: []application.PlannedTask{
				{Key: "a", Title: "Caro", AgentID: "analyst"},
				{Key: "b", Title: "Siguiente", AgentID: "sales", DependsOn: []string{"a"}},
			}}, nil
		},
		runTask: func(application.RunTaskRequest) (application.RunTaskResponse, error) {
			r := okResult("ok")
			r.Usage = application.Usage{CostUSD: 10, InputTokens: 1}
			return r, nil
		},
	}
	h := newHarness(t, rt, func(c *application.Config) { c.BudgetUSD = 5 })
	reqID, _ := h.orch.Submit(context.Background(), "x")
	h.orch.Wait()
	ts := h.tasks(reqID)
	if ts["Caro"].Status != domain.TaskDone || ts["Siguiente"].Status != domain.TaskFailed {
		t.Fatalf("statuses: %s / %s", ts["Caro"].Status, ts["Siguiente"].Status)
	}
	if !h.hasAudit("budget.exceeded") {
		t.Error("missing budget.exceeded audit")
	}
}

func TestDelegationDepthLimit(t *testing.T) {
	rt := &fakeRuntime{plan: func(application.PlanRequest) (application.PlanResponse, error) {
		var tasks []application.PlannedTask
		keys := []string{"a", "b", "c", "d", "e", "f"}
		for i, k := range keys {
			pt := application.PlannedTask{Key: k, Title: k, AgentID: "analyst"}
			if i > 0 {
				pt.DependsOn = []string{keys[i-1]}
			}
			tasks = append(tasks, pt)
		}
		return application.PlanResponse{Tasks: tasks}, nil
	}}
	h := newHarness(t, rt, func(c *application.Config) { c.MaxPlanDepth = 5 }) // the configured limit stays enforced
	reqID, _ := h.orch.Submit(context.Background(), "cadena larga")
	h.orch.Wait()
	if h.request(reqID).Status != domain.RequestFailed || len(h.tasks(reqID)) != 0 {
		t.Fatal("a plan deeper than the limit must be rejected before creating tasks")
	}
	if !h.hasAudit("delegation.depth_exceeded") {
		t.Error("missing depth audit")
	}
}

// A free-form request may be a long sequential chain: the default limit is well above 5,
// and cycles / unknown dependencies are still rejected.
func TestLongSequentialChainRuns(t *testing.T) {
	chain := func(n int, cycle bool) func(application.PlanRequest) (application.PlanResponse, error) {
		return func(application.PlanRequest) (application.PlanResponse, error) {
			var tasks []application.PlannedTask
			for i := 0; i < n; i++ {
				pt := application.PlannedTask{Key: fmt.Sprintf("k%d", i), Title: fmt.Sprintf("paso %d", i), AgentID: "analyst"}
				if i > 0 {
					pt.DependsOn = []string{fmt.Sprintf("k%d", i-1)}
				}
				tasks = append(tasks, pt)
			}
			if cycle {
				tasks[0].DependsOn = []string{fmt.Sprintf("k%d", n-1)}
			}
			return application.PlanResponse{Tasks: tasks}, nil
		}
	}
	h := newHarness(t, &fakeRuntime{plan: chain(12, false)}, nil)
	reqID, _ := h.orch.Submit(context.Background(), "cadena de 12")
	h.orch.Wait()
	if st := h.request(reqID).Status; st != domain.RequestDone || len(h.tasks(reqID)) != 12 {
		t.Fatalf("a 12-step chain must run: status %s, tasks %d", st, len(h.tasks(reqID)))
	}
	h = newHarness(t, &fakeRuntime{plan: chain(12, true)}, nil)
	reqID, _ = h.orch.Submit(context.Background(), "ciclo")
	h.orch.Wait()
	if h.request(reqID).Status != domain.RequestFailed || len(h.tasks(reqID)) != 0 {
		t.Fatal("a cycle must be rejected")
	}
}

func TestResetClearsExecutionData(t *testing.T) {
	h := newHarness(t, &fakeRuntime{plan: proposalPlan()}, nil)
	h.orch.Submit(context.Background(), "x")
	h.orch.Wait()
	if err := h.orch.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	reqs, _ := h.store.ListRequests(context.Background(), domain.DemoOrgID)
	agents, _ := h.store.ListAgents(context.Background(), domain.DemoOrgID)
	if len(reqs) != 0 || len(agents) != 7 {
		t.Fatalf("requests=%d agents=%d", len(reqs), len(agents))
	}
	m, _ := h.q.Metrics(context.Background())
	if m.RequestsTotal != 0 || m.TasksDone != 0 {
		t.Fatalf("metrics = %+v", m)
	}
}
