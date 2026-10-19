package application_test

import (
	"context"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Durable holds across a restart (hold.go): recovery, wake-up by the watcher and expiry of stale holds. The
// MemoryHolds plays the part of the Postgres store: it is shared by the "processes", as the table would be.

func holdRuntime() *fakeRuntime {
	return &fakeRuntime{plan: proposalPlan(), runTask: func(application.RunTaskRequest) (application.RunTaskResponse, error) {
		return okResult("hecho"), nil
	}}
}

func heldProcess(t *testing.T, w *durableWorld, holds application.HoldStore, g application.TaskGate) *process {
	t.Helper()
	p := startProcess(t, w.store, w.pub, holdRuntime(), nil)
	p.orch.SetHoldStore(holds)
	p.orch.SetTaskGate(g)
	return p
}

func submitHeld(t *testing.T, p *process) string {
	t.Helper()
	id, err := p.orch.SubmitPlan(context.Background(), "x", application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "A", AgentID: "sales"}}})
	if err != nil {
		t.Fatal(err)
	}
	p.waitFor("hold recorded", func() bool { return len(p.orch.Holds(context.Background())) == 1 })
	return id
}

func TestHoldSurvivesRestartAndTheTaskResumes(t *testing.T) {
	w := newWorld(t)
	holds := &application.MemoryHolds{}
	until := time.Now().Add(time.Hour)
	p1 := heldProcess(t, w, holds, &untilGate{until: until})
	id := submitHeld(t, p1)
	p1.stop() // the process goes away while the task is held

	list, _ := holds.ListHolds(context.Background(), org)
	if len(list) != 1 || list[0].RequestID != id || !list[0].WakeAt.Equal(until.UTC()) {
		t.Fatalf("a shutdown must keep the hold: %+v", list)
	}

	// The next process: the opening is here (the gate runs the task) and Recover brings the request back.
	g2 := &untilGate{until: until}
	g2.open.Store(true)
	p2 := heldProcess(t, w, holds, g2)
	if n, err := p2.orch.Recover(context.Background(), nil); err != nil || n != 1 {
		t.Fatalf("recover = %d, %v", n, err)
	}
	p2.waitFor("request done and hold removed", func() bool {
		l, _ := holds.ListHolds(context.Background(), org)
		return p2.request(id).Status == domain.RequestDone && len(l) == 0
	})
}

func TestRestartKeepsHoldingUntilTheSameOpening(t *testing.T) {
	w := newWorld(t)
	holds := &application.MemoryHolds{}
	until := time.Now().Add(time.Hour)
	p1 := heldProcess(t, w, holds, &untilGate{until: until})
	id := submitHeld(t, p1)
	p1.stop()

	p2 := heldProcess(t, w, holds, &untilGate{until: until}) // still closed
	if n, err := p2.orch.Recover(context.Background(), nil); err != nil || n != 1 {
		t.Fatalf("recover = %d, %v", n, err)
	}
	p2.waitFor("held again", func() bool { return len(p2.orch.Holds(context.Background())) == 1 })
	time.Sleep(100 * time.Millisecond)
	if got := p2.request(id).Status; got == domain.RequestDone || got == domain.RequestFailed {
		t.Fatalf("a closed schedule must not run the task: %s", got)
	}
	if n := p2.orch.WakeDueHolds(context.Background(), nil); n != 0 {
		t.Fatalf("the opening has not passed: resumed %d", n)
	}
}

func TestWakeDueHoldsResumesAnUnattendedRequest(t *testing.T) {
	w := newWorld(t)
	holds := &application.MemoryHolds{}
	p1 := heldProcess(t, w, holds, &untilGate{until: time.Now().Add(time.Hour)})
	id := submitHeld(t, p1)
	p1.stop()

	// The opening passed while nobody ran the request: the hold is due.
	list, _ := holds.ListHolds(context.Background(), org)
	h := list[0]
	h.WakeAt = time.Now().Add(-time.Minute)
	_ = holds.PutHold(context.Background(), org, h)

	g2 := &untilGate{}
	g2.open.Store(true)
	p2 := heldProcess(t, w, holds, g2)
	if n := p2.orch.WakeDueHolds(context.Background(), nil); n != 1 {
		t.Fatalf("resumed = %d, want 1", n)
	}
	p2.waitFor("request done and hold removed", func() bool {
		l, _ := holds.ListHolds(context.Background(), org)
		return p2.request(id).Status == domain.RequestDone && len(l) == 0
	})
}

func TestWakeDueHoldsDropsStaleHoldsAndKeepsFutureOnes(t *testing.T) {
	w := newWorld(t)
	holds := &application.MemoryHolds{}
	ctx := context.Background()
	p := heldProcess(t, w, holds, nil)
	past, future := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
	_ = holds.PutHold(ctx, org, application.Hold{TaskID: "gone-task", RequestID: "gone-req", WakeAt: past})
	_ = holds.PutHold(ctx, org, application.Hold{TaskID: "later", RequestID: "gone-req", WakeAt: future})
	if n := p.orch.WakeDueHolds(ctx, nil); n != 0 {
		t.Fatalf("nothing to resume, got %d", n)
	}
	l, _ := holds.ListHolds(ctx, org)
	if len(l) != 1 || l[0].TaskID != "later" {
		t.Fatalf("the expired hold of a vanished task is dropped, the future one stays: %+v", l)
	}
}

func TestWakeDueHoldsDropsHoldOfFinishedRequest(t *testing.T) {
	h := newHarness(t, holdRuntime(), nil)
	holds := &application.MemoryHolds{}
	h.orch.SetHoldStore(holds)
	ctx := context.Background()
	id, err := h.orch.SubmitPlan(ctx, "x", application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "A", AgentID: "sales"}}})
	if err != nil {
		t.Fatal(err)
	}
	h.waitFor("done", func() bool { return h.request(id).Status == domain.RequestDone })
	var taskID string
	for _, tk := range h.tasks(id) {
		taskID = tk.ID
	}
	_ = holds.PutHold(ctx, domain.DemoOrgID, application.Hold{TaskID: taskID, RequestID: id, WakeAt: time.Now().Add(-time.Second)})
	if n := h.orch.WakeDueHolds(ctx, nil); n != 0 {
		t.Fatalf("a finished task is not resumed: %d", n)
	}
	if l, _ := holds.ListHolds(ctx, domain.DemoOrgID); len(l) != 0 {
		t.Fatalf("stale hold must be dropped: %+v", l)
	}
}

func TestHoldWatcherStopsWithItsContext(t *testing.T) {
	h := newHarness(t, holdRuntime(), nil)
	h.orch.SetHoldStore(&application.MemoryHolds{})
	ctx, cancel := context.WithCancel(context.Background())
	done := h.orch.StartHoldWatcher(ctx, 5*time.Millisecond, nil)
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the watcher must stop when its context ends")
	}
}
