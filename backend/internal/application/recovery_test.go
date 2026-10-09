package application_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// ---- task-level retry ----

func singleTaskPlan() func(application.PlanRequest) (application.PlanResponse, error) {
	return func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "Analizar", AgentID: "analyst"}}}, nil
	}
}

func costly() application.RunTaskResponse {
	r := okResult("listo")
	r.Usage = application.Usage{InputTokens: 1000, OutputTokens: 500, Model: "m"}
	return r
}

func TestTaskRetriesTransientFailureThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	rt := &fakeRuntime{plan: singleTaskPlan(), runTask: func(application.RunTaskRequest) (application.RunTaskResponse, error) {
		if calls.Add(1) == 1 {
			return application.RunTaskResponse{}, errors.New("invalid structured output")
		}
		return costly(), nil
	}}
	h := newHarness(t, rt, func(c *application.Config) { c.MaxRetries, c.TaskMaxAttempts = 1, 2 })
	reqID, _ := h.orch.Submit(context.Background(), "analiza")
	h.orch.Wait()
	if calls.Load() != 2 {
		t.Fatalf("runtime calls = %d, want 2", calls.Load())
	}
	task := h.tasks(reqID)["Analizar"]
	if task.Status != domain.TaskDone || h.request(reqID).Status != domain.RequestDone {
		t.Fatalf("task %s request %s", task.Status, h.request(reqID).Status)
	}
	if task.Output == nil || task.Output.Metrics["attempts"] != 2 {
		t.Fatalf("attempts not recorded: %+v", task.Output)
	}
	if !h.hasAudit("task.retry") || h.pub.count(domain.EvTaskRetrying) != 1 {
		t.Fatal("retry must be audited and announced once")
	}
	// Only the attempt that returned usage is billed (the failed one held a
	// reservation that was released).
	if n := h.auditCount("runtime.usage"); n != 1 {
		t.Fatalf("usage entries = %d, want 1", n)
	}
	if task.CostUSD <= 0 {
		t.Fatal("the successful attempt's cost must be counted")
	}
	if st, _ := h.orch.Budget().Status(context.Background()); st.Org.ReservedUSD != 0 {
		t.Fatalf("reservation leaked: %+v", st.Org)
	}
}

func TestTaskRetryIsBounded(t *testing.T) {
	var calls atomic.Int32
	rt := &fakeRuntime{plan: singleTaskPlan(), runTask: func(application.RunTaskRequest) (application.RunTaskResponse, error) {
		calls.Add(1)
		return application.RunTaskResponse{}, errors.New("boom")
	}}
	h := newHarness(t, rt, func(c *application.Config) { c.MaxRetries, c.TaskMaxAttempts = 1, 3 })
	reqID, _ := h.orch.Submit(context.Background(), "analiza")
	h.orch.Wait()
	if calls.Load() != 3 {
		t.Fatalf("runtime calls = %d, want exactly 3 (bounded by TaskMaxAttempts)", calls.Load())
	}
	if h.tasks(reqID)["Analizar"].Status != domain.TaskFailed || h.request(reqID).Status != domain.RequestFailed {
		t.Fatal("task and request must end failed")
	}
}

func TestTaskRetryDisabledWithOneAttempt(t *testing.T) {
	var calls atomic.Int32
	rt := &fakeRuntime{plan: singleTaskPlan(), runTask: func(application.RunTaskRequest) (application.RunTaskResponse, error) {
		calls.Add(1)
		return application.RunTaskResponse{}, errors.New("boom")
	}}
	h := newHarness(t, rt, func(c *application.Config) { c.MaxRetries, c.TaskMaxAttempts = 1, 1 })
	h.orch.Submit(context.Background(), "analiza")
	h.orch.Wait()
	if calls.Load() != 1 || h.pub.count(domain.EvTaskRetrying) != 0 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestNonRetryableFailureIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	rt := &fakeRuntime{plan: singleTaskPlan(), runTask: func(application.RunTaskRequest) (application.RunTaskResponse, error) {
		calls.Add(1)
		return application.RunTaskResponse{}, errors.Join(errors.New("status 422"), application.ErrNonRetryable)
	}}
	h := newHarness(t, rt, func(c *application.Config) { c.MaxRetries, c.TaskMaxAttempts = 3, 3 })
	reqID, _ := h.orch.Submit(context.Background(), "analiza")
	h.orch.Wait()
	if calls.Load() != 1 {
		t.Fatalf("runtime calls = %d, want 1 (neither call nor task retry)", calls.Load())
	}
	if h.tasks(reqID)["Analizar"].Status != domain.TaskFailed {
		t.Fatal("task must fail")
	}
}

func TestRejectedApprovalIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	rt := sendProposalRuntime(proposalPlan())
	inner := rt.runTask
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		calls.Add(1)
		return inner(in)
	}
	h := newHarness(t, rt, func(c *application.Config) { c.TaskMaxAttempts = 3 })
	reqID, _ := h.orch.Submit(context.Background(), "enviar propuesta")
	ap := h.awaitApproval()
	if _, err := h.appr.Decide(context.Background(), ap.ID, "reject", "no"); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	if calls.Load() != 1 || h.tasks(reqID)["Preparar propuesta"].Status != domain.TaskBlocked {
		t.Fatalf("a rejection must not re-run the task (calls=%d)", calls.Load())
	}
}

// ---- manual recovery ----

// chainRuntime: a -> b -> c, plus an independent d. "a" fails while broken is set.
func chainRuntime(broken *atomic.Bool, seen *atomic.Value) *fakeRuntime {
	return &fakeRuntime{
		plan: func(application.PlanRequest) (application.PlanResponse, error) {
			return application.PlanResponse{Tasks: []application.PlannedTask{
				{Key: "a", Title: "A", AgentID: "analyst"},
				{Key: "b", Title: "B", AgentID: "analyst", DependsOn: []string{"a"}},
				{Key: "c", Title: "C", AgentID: "analyst", DependsOn: []string{"b"}},
				{Key: "d", Title: "D", AgentID: "analyst"},
			}}, nil
		},
		runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
			if in.Task.Title == "A" && broken.Load() {
				return application.RunTaskResponse{}, errors.New("boom")
			}
			if in.Task.Title == "B" && seen != nil {
				var s []string
				for _, d := range in.Context.DependencyOutputs {
					s = append(s, d.Output.Summary)
				}
				seen.Store(strings.Join(s, "|"))
			}
			return okResult("hecho: " + in.Task.Title), nil
		},
	}
}

func failedChain(t *testing.T, seen *atomic.Value) (*harness, string, *atomic.Bool) {
	t.Helper()
	var broken atomic.Bool
	broken.Store(true)
	h := newHarness(t, chainRuntime(&broken, seen), func(c *application.Config) { c.MaxRetries, c.TaskMaxAttempts = 1, 1 })
	reqID, _ := h.orch.Submit(context.Background(), "cadena")
	h.orch.Wait()
	ts := h.tasks(reqID)
	if ts["A"].Status != domain.TaskFailed || ts["B"].Status != domain.TaskBlocked || ts["C"].Status != domain.TaskBlocked || ts["D"].Status != domain.TaskDone {
		t.Fatalf("setup: A=%s B=%s C=%s D=%s", ts["A"].Status, ts["B"].Status, ts["C"].Status, ts["D"].Status)
	}
	return h, reqID, &broken
}

func TestManualRetryRequeuesTaskAndUnblocksDependents(t *testing.T) {
	h, reqID, broken := failedChain(t, nil)
	broken.Store(false)
	ctx := application.WithActor(context.Background(), "alice")
	a := h.tasks(reqID)["A"]
	res, err := h.orch.RetryTask(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Requeued) != 3 {
		t.Fatalf("requeued = %v, want A, B and C", res.Requeued)
	}
	h.orch.Wait()
	for title, task := range h.tasks(reqID) {
		if task.Status != domain.TaskDone {
			t.Fatalf("task %s = %s after the retry", title, task.Status)
		}
	}
	if r := h.request(reqID); r.Status != domain.RequestDone {
		t.Fatalf("request = %s", r.Status)
	}
	var found bool
	for _, e := range h.store.Audit() {
		if e.Action == "task.retried" && e.Actor == "alice" && e.EntityID == a.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("the manual retry must be audited with the human actor")
	}
}

func TestManualRetryAgainFailingRestoresRequestStatus(t *testing.T) {
	h, reqID, _ := failedChain(t, nil) // still broken
	before := h.request(reqID).Status
	if _, err := h.orch.RetryTask(context.Background(), h.tasks(reqID)["A"].ID); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	if got := h.request(reqID).Status; got != before {
		t.Fatalf("request = %s, want %s (nothing new finished)", got, before)
	}
	if h.tasks(reqID)["A"].Status != domain.TaskFailed {
		t.Fatal("A must be failed again")
	}
}

func TestManualSkipLetsDependentsRunWithANote(t *testing.T) {
	var seen atomic.Value
	h, reqID, _ := failedChain(t, &seen)
	ctx := application.WithActor(context.Background(), "alice")
	a := h.tasks(reqID)["A"]
	if _, err := h.orch.SkipTask(ctx, a.ID, "  "); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty reason: %v", err)
	}
	res, err := h.orch.SkipTask(ctx, a.ID, "la fuente ya no existe")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Requeued) != 2 {
		t.Fatalf("requeued = %v, want B and C", res.Requeued)
	}
	h.orch.Wait()
	ts := h.tasks(reqID)
	for _, title := range []string{"A", "B", "C", "D"} {
		if ts[title].Status != domain.TaskDone {
			t.Fatalf("%s = %s", title, ts[title].Status)
		}
	}
	if ts["A"].Output.Metrics["skipped"] != true || ts["A"].Output.Metrics["skip_reason"] != "la fuente ya no existe" {
		t.Fatalf("skip not recorded: %+v", ts["A"].Output.Metrics)
	}
	if s, _ := seen.Load().(string); !strings.Contains(s, "SKIPPED by alice") || !strings.Contains(s, "NOT available") {
		t.Fatalf("B must see that its input is missing, saw %q", s)
	}
	var reason string
	for _, e := range h.store.Audit() {
		if e.Action == "task.skipped" && e.Actor == "alice" {
			m, _ := e.Details.(map[string]any)
			reason, _ = m["reason"].(string)
		}
	}
	if reason != "la fuente ya no existe" {
		t.Fatal("the skip must be audited with its reason")
	}
}

func TestManualRecoveryIsHumanOnlyAndGuarded(t *testing.T) {
	h, reqID, broken := failedChain(t, nil)
	broken.Store(false)
	a := h.tasks(reqID)["A"]
	for _, actor := range []string{"agent:analyst", "analyst", "system", "orchestrator"} {
		ctx := application.WithActor(context.Background(), actor)
		if _, err := h.orch.RetryTask(ctx, a.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("actor %q retry: %v, want forbidden", actor, err)
		}
		if _, err := h.orch.SkipTask(ctx, a.ID, "x"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("actor %q skip: %v, want forbidden", actor, err)
		}
	}
	if h.tasks(reqID)["A"].Status != domain.TaskFailed {
		t.Fatal("a refused recovery must not change anything")
	}
	if !h.hasAudit("task.retried_refused") {
		t.Fatal("the refusal must be audited")
	}
	// A task that is not failed/blocked cannot be recovered.
	if _, err := h.orch.RetryTask(context.Background(), h.tasks(reqID)["D"].ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("retry of a done task: %v", err)
	}
	// A dependent whose dependency is not finished cannot be retried alone.
	if _, err := h.orch.RetryTask(context.Background(), h.tasks(reqID)["B"].ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("retry of B before A: %v", err)
	}
	// The kill switch (guard) blocks recovery.
	h.orch.SetConnections(guardFake{deny: map[string]string{"*": "kill_switch_active"}}, nil, nil)
	if _, err := h.orch.RetryTask(context.Background(), a.ID); !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "kill_switch_active") {
		t.Fatalf("retry under the kill switch: %v", err)
	}
	if _, err := h.orch.SkipTask(context.Background(), a.ID, "x"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("skip under the kill switch: %v", err)
	}
}

// ---- project approvals never expire ----

// projectOwner makes the orchestrator treat every request as a project's.
type projectOwner struct{}

func (projectOwner) GateTask(context.Context, string, domain.Task) application.GateDecision {
	return application.GateDecision{}
}
func (projectOwner) GateResolved(context.Context, string, domain.Task, string, bool) {}
func (projectOwner) OwnsRequest(context.Context, string, string) bool                { return true }
func (projectOwner) ResumeRequest(context.Context, string, string) bool              { return true }

func TestProjectApprovalWaitsPastTimeoutWithReminders(t *testing.T) {
	h := newHarness(t, sendProposalRuntime(proposalPlan()), func(c *application.Config) {
		c.ApprovalTimeout, c.ProjectReminderEvery = 60*time.Millisecond, 25*time.Millisecond
	})
	h.orch.SetTaskGate(projectOwner{})
	reqID, _ := h.orch.Submit(context.Background(), "enviar propuesta")
	ap := h.awaitApproval()
	h.waitFor("two reminders", func() bool { return h.pub.count(domain.EvApprovalReminder) >= 2 })
	time.Sleep(100 * time.Millisecond) // well past the interactive timeout
	if cur, _ := h.store.GetApproval(context.Background(), domain.DemoOrgID, ap.ID); cur.Status != domain.ApprovalPending {
		t.Fatalf("a project approval must not expire, status = %s", cur.Status)
	}
	if _, err := h.appr.Decide(context.Background(), ap.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	if r := h.request(reqID); r.Status != domain.RequestDone {
		t.Fatalf("request = %s", r.Status)
	}
	before := h.pub.count(domain.EvApprovalReminder)
	time.Sleep(60 * time.Millisecond)
	if h.pub.count(domain.EvApprovalReminder) != before {
		t.Fatal("no reminders after the decision")
	}
}

func TestProjectApprovalTimeoutIsOptional(t *testing.T) {
	h := newHarness(t, sendProposalRuntime(proposalPlan()), func(c *application.Config) {
		c.ApprovalTimeout, c.ProjectReminderEvery, c.ProjectApprovalTimeout = time.Hour, 0, 80*time.Millisecond
	})
	h.orch.SetTaskGate(projectOwner{})
	h.orch.Submit(context.Background(), "enviar propuesta")
	ap := h.awaitApproval()
	h.waitFor("auto-reject", func() bool {
		cur, _ := h.store.GetApproval(context.Background(), domain.DemoOrgID, ap.ID)
		return cur.Status == domain.ApprovalRejected
	})
}

func TestInteractiveApprovalStillAutoRejects(t *testing.T) {
	h := newHarness(t, sendProposalRuntime(proposalPlan()), func(c *application.Config) {
		c.ApprovalTimeout, c.ProjectReminderEvery = 80*time.Millisecond, 20*time.Millisecond
	})
	reqID, _ := h.orch.Submit(context.Background(), "enviar propuesta")
	ap := h.awaitApproval()
	h.orch.Wait()
	cur, _ := h.store.GetApproval(context.Background(), domain.DemoOrgID, ap.ID)
	if cur.Status != domain.ApprovalRejected || h.tasks(reqID)["Preparar propuesta"].Status != domain.TaskBlocked {
		t.Fatalf("an interactive approval keeps auto-rejecting: %s", cur.Status)
	}
	if h.pub.count(domain.EvApprovalReminder) != 0 {
		t.Fatal("interactive approvals get no reminders")
	}
}

func TestProjectApprovalSurvivesRestartPastOldDeadline(t *testing.T) {
	store, pub := newStore(t), &capture{}
	cfg := func(c *application.Config) {
		c.ApprovalTimeout, c.ProjectReminderEvery = 120*time.Millisecond, 30*time.Millisecond
	}
	p1 := startProcess(t, store, pub, sendProposalRuntime(proposalPlan()), cfg)
	p1.orch.SetTaskGate(projectOwner{})
	reqID, _ := p1.orch.Submit(context.Background(), "enviar propuesta")
	ap := p1.awaitApproval()
	p1.stop()
	time.Sleep(200 * time.Millisecond) // the original 120 ms deadline is long gone

	p2 := startProcess(t, store, pub, sendProposalRuntime(proposalPlan()), cfg)
	p2.orch.SetTaskGate(projectOwner{})
	if n, err := p2.orch.Recover(context.Background(), nil); err != nil || n != 1 {
		t.Fatalf("recover = %d, %v", n, err)
	}
	before := pub.count(domain.EvApprovalReminder)
	p2.waitFor("reminders after the restart", func() bool { return pub.count(domain.EvApprovalReminder) > before })
	if cur, _ := store.GetApproval(context.Background(), domain.DemoOrgID, ap.ID); cur.Status != domain.ApprovalPending {
		t.Fatalf("approval = %s: the restart must not expire a project approval", cur.Status)
	}
	if _, err := p2.appr.Decide(context.Background(), ap.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	p2.orch.Wait()
	if r := p2.request(reqID); r.Status != domain.RequestDone {
		t.Fatalf("request = %s", r.Status)
	}
	if c := p2.auditCount("tool.executed"); c != 1 {
		t.Fatalf("tool.executed = %d, want exactly 1", c)
	}
}

func TestProjectBudgetPauseWaitsWithRemindersInsteadOfFailing(t *testing.T) {
	rt := costRuntime(chainPlan("analyst"), 0.04, 0.05)
	h := newHarness(t, rt, func(c *application.Config) {
		c.PauseTimeout, c.ProjectReminderEvery = 50*time.Millisecond, 30*time.Millisecond
	})
	h.orch.SetTaskGate(projectOwner{})
	reqID, _ := h.orch.Submit(application.WithBudgetCap(context.Background(), 0.01), "x")
	h.proceed(reqID)
	h.waitFor("paused", func() bool { return h.request(reqID).Status == domain.RequestPaused })
	h.waitFor("reminders", func() bool { return h.pub.count(domain.EvBudgetExceeded) >= 3 })
	time.Sleep(80 * time.Millisecond) // past the interactive PauseTimeout
	if st := h.request(reqID).Status; st != domain.RequestPaused {
		t.Fatalf("a project pause must not fail after the timeout, status = %s", st)
	}
	if err := h.orch.Budget().SetCap(context.Background(), domain.ScopeRequest, reqID, 10); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	if st := h.request(reqID).Status; st != domain.RequestDone {
		t.Fatalf("status = %s, want done once the cap was raised", st)
	}
}
