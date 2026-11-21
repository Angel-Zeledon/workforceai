package application_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
)

// ---- stalled-work watchdog (F6) ----

// hangRuntime hangs the calls chosen by block until their context is cancelled; the others succeed. It records which
// agent made each call.
type hangRuntime struct {
	fakeRuntime
	calls  atomic.Int32
	block  func(n int32, in application.RunTaskRequest) bool
	mu     sync.Mutex
	agents []string
}

func (r *hangRuntime) RunTask(ctx context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
	n := r.calls.Add(1)
	r.mu.Lock()
	r.agents = append(r.agents, in.Agent.ID)
	r.mu.Unlock()
	if r.block != nil && r.block(n, in) {
		<-ctx.Done()
		return application.RunTaskResponse{}, ctx.Err()
	}
	return okResult("hecho: " + in.Task.Title), nil
}

func (r *hangRuntime) callAgents() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.agents...)
}

func firstCallHangs(plan func(application.PlanRequest) (application.PlanResponse, error)) *hangRuntime {
	rt := &hangRuntime{block: func(n int32, _ application.RunTaskRequest) bool { return n == 1 }}
	rt.plan = plan
	return rt
}

// watchdogOf builds the watchdog of a harness whose clock runs ahead of real time by ahead.
func watchdogOf(h *harness, ahead time.Duration) (*application.Watchdog, *application.InitiativeService) {
	cfg := application.DefaultConfig()
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: h.store, Pub: h.pub, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	clock := func() time.Time { return time.Now().Add(ahead) }
	is := &application.InitiativeService{Store: h.store, Submit: h.orch, Rec: rec, Cfg: cfg, Now: clock}
	w := &application.Watchdog{Orch: h.orch, Initiative: is, Rec: rec, Cfg: cfg, Now: clock}
	w.Install()
	return w, is
}

func (h *harness) waitRunning(reqID, title string) domain.Task {
	h.t.Helper()
	var t domain.Task
	h.waitFor(title+" running", func() bool { t = h.tasks(reqID)[title]; return t.Status == domain.TaskRunning })
	return t
}

func TestWatchdogDetectsARunningTaskWithoutProgressOnlyPastTheThreshold(t *testing.T) {
	rt := firstCallHangs(singleTaskPlan())
	h := newHarness(t, rt, nil)
	reqID, _ := h.orch.Submit(context.Background(), "analiza")
	task := h.waitRunning(reqID, "Analizar")
	h.waitFor("call started", func() bool { return rt.calls.Load() == 1 })

	early, _ := watchdogOf(h, 10*time.Minute)
	if v, err := early.List(context.Background()); err != nil || len(v) != 0 {
		t.Fatalf("10 min is not stalled: %+v %v", v, err)
	}
	late, _ := watchdogOf(h, 41*time.Minute)
	v, err := late.List(context.Background())
	if err != nil || len(v) != 1 {
		t.Fatalf("stalled = %+v %v", v, err)
	}
	if v[0].TaskID != task.ID || v[0].Kind != application.StallNoProgress || v[0].Minutes < 40 || v[0].AgentID != "analyst" || v[0].Project {
		t.Fatalf("stall = %+v", v[0])
	}
}

func TestWatchdogIgnoresWorkWaitingForAPerson(t *testing.T) {
	h := newHarness(t, sendProposalRuntime(proposalPlan()), func(c *application.Config) { c.ApprovalTimeout = time.Hour })
	reqID, _ := h.orch.Submit(context.Background(), "enviar propuesta")
	h.awaitApproval()
	if st := h.tasks(reqID)["Preparar propuesta"].Status; st != domain.TaskAwaitingApproval {
		t.Fatalf("status = %s", st)
	}
	w, _ := watchdogOf(h, 6*time.Hour)
	if v, _ := w.List(context.Background()); len(v) != 0 {
		t.Fatalf("a task waiting for an approval is not stalled: %+v", v)
	}
	// a paused request (spend cap reached, waiting for a person) is not stalled either
	r := h.request(reqID)
	r.Status = domain.RequestPaused
	_ = h.store.UpdateRequest(context.Background(), domain.DemoOrgID, r)
	if v, _ := w.List(context.Background()); len(v) != 0 {
		t.Fatalf("paused request flagged: %+v", v)
	}
}

func TestWatchdogScanAnnouncesAndSuggestsOncePerEpisode(t *testing.T) {
	rt := firstCallHangs(singleTaskPlan())
	h := newHarness(t, rt, nil)
	reqID, _ := h.orch.Submit(context.Background(), "analiza")
	h.waitRunning(reqID, "Analizar")
	h.waitFor("call started", func() bool { return rt.calls.Load() == 1 })
	w, is := watchdogOf(h, 45*time.Minute)

	first, err := w.Scan(context.Background())
	if err != nil || len(first) != 1 || !first[0].Raised || !first[0].Announced {
		t.Fatalf("first scan = %+v %v", first, err)
	}
	second, _ := w.Scan(context.Background())
	if len(second) != 1 || second[0].Raised || second[0].Reason != "duplicate" || second[0].Announced {
		t.Fatalf("second scan = %+v", second)
	}
	its, _ := is.List(context.Background(), application.InitiativePending, 0)
	if len(its) != 1 || its[0].Signal != application.SignalStalledTask || its[0].AgentID != "analyst" || its[0].Params["task_id"] == "" {
		t.Fatalf("suggestions = %+v", its)
	}
	if its[0].Title == "" || its[0].Body != "¿La reintento, se la paso a otro o la cancelo?" {
		t.Fatalf("text = %q / %q", its[0].Title, its[0].Body)
	}
	if n := h.auditCount("task.stalled"); n != 1 {
		t.Fatalf("task.stalled audits = %d", n)
	}
	if n := h.pub.count(application.EvTaskStalled); n != 1 {
		t.Fatalf("task.stalled events = %d", n)
	}
}

func TestWatchdogSuggestionRespectsTheBudgetButStillAnnounces(t *testing.T) {
	rt := firstCallHangs(singleTaskPlan())
	h := newHarness(t, rt, nil)
	reqID, _ := h.orch.Submit(context.Background(), "analiza")
	h.waitRunning(reqID, "Analizar")
	w, is := watchdogOf(h, 45*time.Minute)
	if _, err := is.PutSettings(context.Background(), application.InitiativeSettings{Enabled: true, DailyLimit: 0, MonthlyLimit: 30}); err != nil {
		t.Fatal(err)
	}
	res, _ := w.Scan(context.Background())
	if len(res) != 1 || res[0].Raised || res[0].Reason != "daily_limit" || !res[0].Announced {
		t.Fatalf("scan = %+v", res)
	}
	// switched off: nothing is evaluated
	sig := application.DefaultSignalSettings()
	sig.StalledWork.Enabled = false
	if _, err := is.PutSettings(context.Background(), application.InitiativeSettings{Enabled: true, DailyLimit: 3, MonthlyLimit: 30, Signals: &sig}); err != nil {
		t.Fatal(err)
	}
	if res, _ := w.Scan(context.Background()); len(res) != 0 {
		t.Fatalf("disabled scan = %+v", res)
	}
}

func TestStalledRetryRerunsTheSameExecutionOnceAndIsIdempotent(t *testing.T) {
	rt := firstCallHangs(singleTaskPlan())
	h := newHarness(t, rt, nil)
	reqID, _ := h.orch.Submit(context.Background(), "analiza")
	task := h.waitRunning(reqID, "Analizar")
	h.waitFor("call started", func() bool { return rt.calls.Load() == 1 })
	w, _ := watchdogOf(h, 45*time.Minute)

	// two people press "retry" at the same time: exactly one wins
	var wg sync.WaitGroup
	var ok, conflict atomic.Int32
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := w.Recover(context.Background(), task.ID, application.StallRecoverInput{Action: application.StallRetry})
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, domain.ErrConflict):
				conflict.Add(1)
			default:
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || conflict.Load() != 1 {
		t.Fatalf("ok=%d conflict=%d", ok.Load(), conflict.Load())
	}
	h.orch.Wait()
	if rt.calls.Load() != 2 {
		t.Fatalf("runtime calls = %d, want 2 (the hung one and one retry)", rt.calls.Load())
	}
	if st := h.tasks(reqID)["Analizar"].Status; st != domain.TaskDone || h.request(reqID).Status != domain.RequestDone {
		t.Fatalf("task %s request %s", st, h.request(reqID).Status)
	}
	if n := h.auditCount("task.stall_retried"); n != 1 {
		t.Fatalf("audits = %d", n)
	}
	if _, err := w.Recover(context.Background(), task.ID, application.StallRecoverInput{Action: application.StallRetry}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("retrying a finished task must conflict: %v", err)
	}
}

func TestAcceptingAStallSuggestionRetriesInsteadOfSubmitting(t *testing.T) {
	rt := firstCallHangs(singleTaskPlan())
	h := newHarness(t, rt, nil)
	reqID, _ := h.orch.Submit(context.Background(), "analiza")
	h.waitRunning(reqID, "Analizar")
	h.waitFor("call started", func() bool { return rt.calls.Load() == 1 })
	w, is := watchdogOf(h, 45*time.Minute)
	res, _ := w.Scan(context.Background())
	if len(res) != 1 || !res[0].Raised {
		t.Fatalf("scan = %+v", res)
	}
	before, _ := h.store.ListRequests(context.Background(), domain.DemoOrgID)
	it, err := is.Accept(context.Background(), res[0].InitiativeID)
	if err != nil || it.Status != application.InitiativeAccepted || it.RequestID != reqID {
		t.Fatalf("accept = %+v %v", it, err)
	}
	h.orch.Wait()
	after, _ := h.store.ListRequests(context.Background(), domain.DemoOrgID)
	if len(after) != len(before) {
		t.Fatalf("accepting a stall suggestion must not create a request: %d -> %d", len(before), len(after))
	}
	if h.tasks(reqID)["Analizar"].Status != domain.TaskDone {
		t.Fatal("the task must have been retried")
	}
}

func TestStalledReassignStaysInTheRoleAndTheDepartment(t *testing.T) {
	rt := firstCallHangs(singleTaskPlan())
	h := newHarness(t, rt, nil)
	ctx := context.Background()
	for _, a := range []domain.Agent{
		{ID: "analyst2", Name: "Ana Dos", Role: "analyst", State: domain.StateIdle},
		{ID: "analyst3", Name: "Ana Tres", Role: "analyst", State: domain.StateIdle},
	} {
		if err := h.store.CreateAgent(ctx, domain.DemoOrgID, a); err != nil {
			t.Fatal(err)
		}
	}
	// analyst3 works in another department
	h.orch.SetAgentOffice(func(_ context.Context, _, id string) (string, error) {
		if id == "analyst3" {
			return "legal-dept", nil
		}
		return "", nil
	})
	reqID, _ := h.orch.Submit(ctx, "analiza")
	task := h.waitRunning(reqID, "Analizar")
	h.waitFor("call started", func() bool { return rt.calls.Load() == 1 })
	w, _ := watchdogOf(h, 45*time.Minute)

	d, err := w.Detail(ctx, task.ID)
	if err != nil || !d.Stalled || len(d.Candidates) != 1 || d.Candidates[0].ID != "analyst2" {
		t.Fatalf("detail = %+v %v", d, err)
	}
	for _, bad := range []string{"sales", "analyst3", "nobody"} {
		if _, err := w.Recover(ctx, task.ID, application.StallRecoverInput{Action: application.StallReassign, AgentID: bad}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("reassign to %s: %v", bad, err)
		}
	}
	if _, err := w.Recover(ctx, task.ID, application.StallRecoverInput{Action: application.StallReassign, AgentID: "analyst2"}); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	got := h.tasks(reqID)["Analizar"]
	if got.Status != domain.TaskDone || got.AgentID != "analyst2" {
		t.Fatalf("task = %s by %s", got.Status, got.AgentID)
	}
	if agents := rt.callAgents(); len(agents) != 2 || agents[0] != "analyst" || agents[1] != "analyst2" {
		t.Fatalf("calls by %v", agents)
	}
}

func stallChainPlan() func(application.PlanRequest) (application.PlanResponse, error) {
	return func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{Tasks: []application.PlannedTask{
			{Key: "a", Title: "Reunir datos", AgentID: "analyst"},
			{Key: "b", Title: "Calcular margen", AgentID: "accounting", DependsOn: []string{"a"}},
			{Key: "c", Title: "Redactar informe", AgentID: "assistant", DependsOn: []string{"b"}},
			{Key: "d", Title: "Revisar contrato", AgentID: "legal"},
		}}, nil
	}
}

func TestStalledCancelListsAndStopsTheActiveDependents(t *testing.T) {
	rt := &hangRuntime{block: func(_ int32, in application.RunTaskRequest) bool { return in.Task.Title == "Reunir datos" }}
	rt.plan = stallChainPlan()
	h := newHarness(t, rt, nil)
	ctx := context.Background()
	reqID, _ := h.orch.Submit(ctx, "informe")
	a := h.waitRunning(reqID, "Reunir datos")
	h.waitFor("independent task done", func() bool { return h.tasks(reqID)["Revisar contrato"].Status == domain.TaskDone })
	w, _ := watchdogOf(h, 45*time.Minute)

	d, err := w.Detail(ctx, a.ID)
	if err != nil || !d.Stalled || len(d.Cascade) != 2 {
		t.Fatalf("detail = %+v %v", d, err)
	}
	ts := h.tasks(reqID)
	want := []string{ts["Calcular margen"].ID, ts["Redactar informe"].ID}
	// a confirmation that does not match the cascade is refused with the right list
	_, err = w.Recover(ctx, a.ID, application.StallRecoverInput{Action: application.StallCancel, ConfirmTaskIDs: want[:1]})
	if cascade, ok := application.IsCascadeChanged(err); !ok || len(cascade) != 2 || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("partial confirmation: %v %+v", err, cascade)
	}
	if h.tasks(reqID)["Reunir datos"].Status != domain.TaskRunning {
		t.Fatal("a refused cancellation must change nothing")
	}
	res, err := w.Recover(ctx, a.ID, application.StallRecoverInput{Action: application.StallCancel, ConfirmTaskIDs: want})
	if err != nil || len(res.Cancelled) != 2 {
		t.Fatalf("cancel = %+v %v", res, err)
	}
	h.orch.Wait()
	ts = h.tasks(reqID)
	for _, title := range []string{"Reunir datos", "Calcular margen", "Redactar informe"} {
		if ts[title].Status != domain.TaskBlocked {
			t.Errorf("%s = %s, want blocked", title, ts[title].Status)
		}
	}
	if ts["Reunir datos"].Output == nil || ts["Reunir datos"].Output.Metrics["cancelled"] != true {
		t.Fatalf("cancel note missing: %+v", ts["Reunir datos"].Output)
	}
	if ts["Revisar contrato"].Status != domain.TaskDone || h.request(reqID).Status != domain.RequestDone {
		t.Fatalf("the rest of the request must finish: %s / %s", ts["Revisar contrato"].Status, h.request(reqID).Status)
	}
	if rt.calls.Load() != 2 {
		t.Fatalf("calls = %d (the cancelled dependents never run)", rt.calls.Load())
	}
}

func TestAgentsCannotRecoverStalledWork(t *testing.T) {
	rt := firstCallHangs(singleTaskPlan())
	h := newHarness(t, rt, nil)
	reqID, _ := h.orch.Submit(context.Background(), "analiza")
	task := h.waitRunning(reqID, "Analizar")
	w, _ := watchdogOf(h, 45*time.Minute)
	for _, actor := range []string{"agent:analyst", "analyst", "system"} {
		_, err := w.Recover(application.WithActor(context.Background(), actor), task.ID, application.StallRecoverInput{Action: application.StallRetry})
		if !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s: %v", actor, err)
		}
	}
}

// ---- no worker: the request survives a restart that did not resume it ----

func stoppedWhileRunning(t *testing.T) (*durableWorld, string, string) {
	t.Helper()
	w := newWorld(t)
	hang := &hangRuntime{block: func(int32, application.RunTaskRequest) bool { return true }}
	hang.plan = singleTaskPlan()
	p1 := startProcess(t, w.store, w.pub, hang, nil)
	reqID, err := p1.orch.Submit(context.Background(), "analiza")
	if err != nil {
		t.Fatal(err)
	}
	task := p1.waitRunning(reqID, "Analizar")
	p1.stop() // the process dies mid-call: the task stays running, the request running
	return w, reqID, task.ID
}

func TestOrphanStalledTaskIsResumedOnceByTheDurablePath(t *testing.T) {
	w, reqID, taskID := stoppedWhileRunning(t)
	ok := &hangRuntime{}
	ok.plan = singleTaskPlan()
	p2 := startProcess(t, w.store, w.pub, ok, nil) // no Recover: nobody resumes the request
	wd, _ := watchdogOf(p2.harness, 45*time.Minute)
	v, _ := wd.List(context.Background())
	if len(v) != 1 || v[0].TaskID != taskID || v[0].Kind != application.StallNoWorker {
		t.Fatalf("stalled = %+v", v)
	}
	res, err := wd.Recover(context.Background(), taskID, application.StallRecoverInput{Action: application.StallRetry})
	if err != nil || res.Status != "resumed" {
		t.Fatalf("recover = %+v %v", res, err)
	}
	// Recover (startup) runs now: the request is already being processed, it is not resumed twice
	if n, err := p2.orch.Recover(context.Background(), nil); err != nil || n != 0 {
		t.Fatalf("Recover resumed %d (%v)", n, err)
	}
	p2.orch.Wait()
	if ok.calls.Load() != 1 {
		t.Fatalf("runtime calls after the restart = %d, want 1", ok.calls.Load())
	}
	if p2.tasks(reqID)["Analizar"].Status != domain.TaskDone || p2.request(reqID).Status != domain.RequestDone {
		t.Fatal("the resumed request must finish")
	}
	if !p2.hasAudit("task.stall_retried") {
		t.Fatal("not audited")
	}
}

func TestRecoverFirstOwnsTheRequestAndARetryNeverStartsASecondExecution(t *testing.T) {
	w, reqID, taskID := stoppedWhileRunning(t)
	gate := make(chan struct{})
	slow := &hangRuntime{block: func(int32, application.RunTaskRequest) bool { <-gate; return false }} // ignores its context
	slow.plan = singleTaskPlan()
	p2 := startProcess(t, w.store, w.pub, slow, nil)
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	if n, err := p2.orch.Recover(context.Background(), nil); err != nil || n != 1 {
		t.Fatalf("Recover = %d %v", n, err)
	}
	p2.waitFor("resumed call", func() bool { return slow.calls.Load() == 1 })
	now, _ := watchdogOf(p2.harness, 0)
	// the durable resume owns the request and the task has a fresh execution: nothing to recover
	if v, _ := now.List(context.Background()); len(v) != 0 {
		t.Fatalf("stalled right after Recover = %+v", v)
	}
	if _, err := now.Recover(context.Background(), taskID, application.StallRecoverInput{Action: application.StallRetry}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("retry of a resumed task: %v", err)
	}
	// 45 minutes later that execution is stuck: the retry is an order to it, never a second run
	later, _ := watchdogOf(p2.harness, 45*time.Minute)
	v, _ := later.List(context.Background())
	if len(v) != 1 || v[0].Kind != application.StallNoProgress {
		t.Fatalf("stalled = %+v", v)
	}
	res, err := later.Recover(context.Background(), taskID, application.StallRecoverInput{Action: application.StallRetry})
	if err != nil || res.Status != "requested" {
		t.Fatalf("recover = %+v %v (the call ignores its context: the order waits)", res, err)
	}
	if _, err := later.Recover(context.Background(), taskID, application.StallRecoverInput{Action: application.StallRetry}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second retry while the first waits: %v", err)
	}
	close(gate)
	p2.orch.Wait()
	if slow.calls.Load() != 1 || p2.request(reqID).Status != domain.RequestDone || p2.tasks(reqID)["Analizar"].Status != domain.TaskDone {
		t.Fatalf("calls = %d status %s", slow.calls.Load(), p2.request(reqID).Status)
	}
}

func TestOrphanStalledCancelBlocksAndClosesTheRequest(t *testing.T) {
	w, reqID, taskID := stoppedWhileRunning(t)
	ok := &hangRuntime{}
	ok.plan = singleTaskPlan()
	p2 := startProcess(t, w.store, w.pub, ok, nil)
	wd, _ := watchdogOf(p2.harness, 45*time.Minute)
	res, err := wd.Recover(context.Background(), taskID, application.StallRecoverInput{Action: application.StallCancel, ConfirmTaskIDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	p2.orch.Wait()
	if p2.tasks(reqID)["Analizar"].Status != domain.TaskBlocked || ok.calls.Load() != 0 {
		t.Fatalf("task %s calls %d (%+v)", p2.tasks(reqID)["Analizar"].Status, ok.calls.Load(), res)
	}
	if st := p2.request(reqID).Status; st != domain.RequestFailed {
		t.Fatalf("request = %s, want failed (nothing was completed)", st)
	}
}

// orgStore is the memory store with the tenant check Postgres does by RLS: another organization sees nothing.
type orgStore struct{ *memory.Store }

func (s orgStore) ListTasks(ctx context.Context, org, agent, status string) ([]domain.Task, error) {
	if org != domain.DemoOrgID {
		return []domain.Task{}, nil
	}
	return s.Store.ListTasks(ctx, org, agent, status)
}

func (s orgStore) GetTask(ctx context.Context, org, id string) (domain.Task, error) {
	if org != domain.DemoOrgID {
		return domain.Task{}, domain.ErrNotFound
	}
	return s.Store.GetTask(ctx, org, id)
}

func TestStalledWorkIsPerOrganization(t *testing.T) {
	rt := firstCallHangs(singleTaskPlan())
	cfg := application.DefaultConfig()
	cfg.IdleDelay, cfg.RetryBase = 0, time.Millisecond
	mem := newStore(t)
	pub := &capture{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: mem, Pub: pub, Log: log}
	appr := application.NewApprovals(cfg, mem, rec)
	q := &application.Queries{Store: mem, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	orch := application.NewOrchestrator(ctx, cfg, orgStore{mem}, rt, memory.NewLocker(), rec, appr, q, log)
	h := &harness{t, mem, pub, orch, appr, q}
	reqID, _ := h.orch.Submit(context.Background(), "analiza")
	task := h.waitRunning(reqID, "Analizar")
	w, _ := watchdogOf(h, 45*time.Minute)
	if v, _ := w.List(context.Background()); len(v) != 1 {
		t.Fatalf("own organization = %+v", v)
	}
	other := application.WithOrg(context.Background(), "other-org")
	if v, _ := w.List(other); len(v) != 0 {
		t.Fatalf("another organization sees %+v", v)
	}
	if _, err := w.Recover(other, task.ID, application.StallRecoverInput{Action: application.StallRetry}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("another organization recovered: %v", err)
	}
	if _, err := w.Detail(other, task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("another organization reads the detail: %v", err)
	}
}
