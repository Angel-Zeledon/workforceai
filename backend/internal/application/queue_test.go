package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/counters"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
)

// concurrency counts runtime calls in flight.
type concurrency struct {
	mu        sync.Mutex
	cur, peak int
}

func (c *concurrency) enter() {
	c.mu.Lock()
	c.cur++
	c.peak = max(c.peak, c.cur)
	c.mu.Unlock()
	time.Sleep(15 * time.Millisecond)
	c.mu.Lock()
	c.cur--
	c.mu.Unlock()
}

func (c *concurrency) max() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peak
}

func twoTaskRuntime(c *concurrency) *fakeRuntime {
	return &fakeRuntime{
		plan: func(application.PlanRequest) (application.PlanResponse, error) {
			c.enter()
			return application.PlanResponse{Objectives: []string{"x"}, Tasks: []application.PlannedTask{
				{Key: "a", Title: "Uno", AgentID: "analyst"}, {Key: "b", Title: "Dos", AgentID: "accounting"}}}, nil
		},
		runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
			c.enter()
			return okResult("hecho: " + in.Task.Title), nil
		},
	}
}

func (h *harness) eventsOf(typ string) []domain.Event {
	h.pub.mu.Lock()
	defer h.pub.mu.Unlock()
	var out []domain.Event
	for _, e := range h.pub.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// A1 step 6: MAX_PARALLEL_PER_ORG caps the runtime calls of an organization
// across requests; the rest wait visibly (request.queued) and still finish.
func TestPerOrgQueueCapsParallelCallsAndIsVisible(t *testing.T) {
	c := &concurrency{}
	h := newHarness(t, twoTaskRuntime(c), func(cfg *application.Config) { cfg.MaxParallelPerOrg = 1 })
	id1, err := h.orch.Submit(context.Background(), "primera")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := h.orch.Submit(application.WithWorkPriority(context.Background(), application.PrioritySchedule), "segunda")
	if err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	for _, id := range []string{id1, id2} {
		if st := h.request(id).Status; st != domain.RequestDone {
			t.Fatalf("request %s = %s, want done", id, st)
		}
	}
	if c.max() != 1 {
		t.Fatalf("peak runtime calls = %d, want 1", c.max())
	}
	queued := h.eventsOf(domain.EvRequestQueued)
	if len(queued) == 0 {
		t.Fatal("no request.queued event while the organization was full")
	}
	sawSchedule := false
	for _, e := range queued {
		p := e.Payload.(map[string]any)
		if p["request_id"] == "" || p["max_parallel_per_org"] != 1 {
			t.Fatalf("payload = %+v", p)
		}
		if p["request_id"] == id2 && p["priority"] == "schedule" {
			sawSchedule = true
		}
	}
	if !sawSchedule {
		t.Fatal("the scheduled request must queue with priority schedule")
	}
	if len(h.eventsOf(domain.EvRequestDequeued)) == 0 {
		t.Fatal("no request.dequeued event")
	}
	if !h.hasAudit("request.queued") {
		t.Fatal("queueing must be audited")
	}
	// one activity line per queued request at most
	acts := h.eventsOf(domain.EvActivityLogged)
	lines := map[string]int{}
	for _, e := range acts {
		item := e.Payload.(map[string]any)["item"].(domain.ActivityItem)
		if item.Kind == domain.EvRequestQueued {
			lines[item.Text]++
		}
	}
	for txt, n := range lines {
		if n > 2 {
			t.Fatalf("activity %q repeated %d times", txt, n)
		}
	}
}

// Below the cap, the parallel tasks of one request still run together: both
// tasks must be inside the runtime at the same time (barrier) to finish.
func TestPerOrgQueueDefaultKeepsParallelism(t *testing.T) {
	if application.DefaultConfig().MaxParallelPerOrg != 8 {
		t.Fatal("default MAX_PARALLEL_PER_ORG must be 8")
	}
	var mu sync.Mutex
	arrived := 0
	both := make(chan struct{})
	rt := &fakeRuntime{
		plan: twoTaskRuntime(&concurrency{}).plan,
		runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
			mu.Lock()
			if arrived++; arrived == 2 {
				close(both)
			}
			mu.Unlock()
			select {
			case <-both:
				return okResult("hecho: " + in.Task.Title), nil
			case <-time.After(3 * time.Second):
				return application.RunTaskResponse{}, errors.New("the other task never ran in parallel")
			}
		},
	}
	h := newHarness(t, rt, func(cfg *application.Config) { cfg.MaxRetries = 1 })
	id, _ := h.orch.Submit(context.Background(), "x")
	h.orch.Wait()
	if st := h.request(id).Status; st != domain.RequestDone {
		t.Fatalf("status %s: tasks did not run in parallel below the cap", st)
	}
	if len(h.eventsOf(domain.EvRequestQueued)) != 0 {
		t.Fatal("nothing must queue below the cap")
	}
}

// A paused agent (also kill switch / operating hours: same guard) does not
// hold a slot: its task waits before the runtime call, the rest of the org works.
func TestPausedTaskDoesNotHoldAnOrgSlot(t *testing.T) {
	c := &concurrency{}
	h := newHarness(t, twoTaskRuntime(c), func(cfg *application.Config) { cfg.MaxParallelPerOrg = 1 })
	h.orch.SetConnections(guardFake{deny: map[string]string{"analyst": "agent_paused"}}, nil, nil)
	h.orch.SetGuardPoll(5 * time.Millisecond)
	id, _ := h.orch.Submit(context.Background(), "x")
	h.waitFor("the other agent's task to finish", func() bool {
		return h.tasks(id)["Dos"].Status == domain.TaskDone
	})
	if st := h.tasks(id)["Uno"].Status; st == domain.TaskDone || st == domain.TaskRunning {
		t.Fatalf("paused agent's task status = %s", st)
	}
	if !h.hasAudit("task.paused") {
		t.Fatal("the pause must stay visible")
	}
}

// A1 step 6 / windowed limits: the counters survive a restart (a new
// PolicyService and orchestrator over the same counters store).
func TestWindowedLimitCountersSurviveRestart(t *testing.T) {
	g := policy.Governance{Limits: []policy.LimitRule{{ID: "mail.window", Tool: "email", Action: "send*", WindowSeconds: 3600, MaxCalls: 2}}}
	send := application.ToolRequest{Tool: "email", Action: "send", Risk: "low", Args: map[string]any{"to": "a@acme.com"}}
	cs := counters.NewMemStore()

	h1 := newHarness(t, toolRuntime(send, send), nil)
	h1.setRules(gov(g))
	h1.orch.SetPolicy(&application.PolicyService{Store: h1.store, Cfg: application.DefaultConfig(), Counters: cs})
	h1.orch.Submit(context.Background(), "x")
	h1.orch.Wait()
	if n := len(h1.auditEntries("tool.executed")); n != 2 {
		t.Fatalf("first process executed %d, want 2", n)
	}

	// "restart": fresh process state, same persisted counters.
	h2 := newHarness(t, toolRuntime(send), nil)
	h2.setRules(gov(g))
	h2.orch.SetPolicy(&application.PolicyService{Store: h2.store, Cfg: application.DefaultConfig(), Counters: cs})
	h2.orch.Submit(context.Background(), "x")
	h2.orch.Wait()
	if n := len(h2.auditEntries("tool.executed")); n != 0 {
		t.Fatalf("after restart executed %d, want 0 (window already used)", n)
	}
	den := h2.auditEntries("tool.denied")
	if len(den) != 1 || den[0].Details.(map[string]any)["reason"] != "policy:limit:mail.window" {
		t.Fatalf("denied = %+v", den)
	}

	// Without persistence a restart starts from zero (previous behavior).
	h3 := newHarness(t, toolRuntime(send), nil)
	h3.setRules(gov(g))
	h3.orch.SetPolicy(&application.PolicyService{Store: h3.store, Cfg: application.DefaultConfig()})
	h3.orch.Submit(context.Background(), "x")
	h3.orch.Wait()
	if n := len(h3.auditEntries("tool.executed")); n != 1 {
		t.Fatalf("in-memory counters: executed %d, want 1", n)
	}
}
