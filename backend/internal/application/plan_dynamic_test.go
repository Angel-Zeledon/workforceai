package application_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

func dynChain(broken *atomic.Bool, release chan struct{}) *fakeRuntime {
	rt := chainRuntime(broken, nil)
	inner := rt.runTask
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		if in.Task.Title == "D" && release != nil {
			<-release
		}
		return inner(in)
	}
	return rt
}

func TestAmendRunReplacesFailedTaskAfterTheRunEnded(t *testing.T) {
	var broken atomic.Bool
	broken.Store(true)
	h := newHarness(t, dynChain(&broken, nil), func(c *application.Config) { c.MaxRetries, c.TaskMaxAttempts = 1, 1 })
	reqID, _ := h.orch.Submit(context.Background(), "cadena")
	h.orch.Wait()
	ts := h.tasks(reqID)
	a := application.RunAmendment{
		Add:     []application.NewTaskSpec{{Key: "r", Title: "R", AgentID: "analyst"}},
		Repoint: map[string][]string{ts["B"].ID: {"r"}},
		Requeue: []string{ts["B"].ID, ts["C"].ID},
	}
	res, err := h.orch.AmendRun(application.WithActor(context.Background(), "alice"), reqID, a)
	if err != nil {
		t.Fatal(err)
	}
	if res.Live || res.Added["r"] == "" || len(res.Requeued) != 2 {
		t.Fatalf("result %+v", res)
	}
	h.orch.Wait()
	ts = h.tasks(reqID)
	for _, title := range []string{"R", "B", "C", "D"} {
		if ts[title].Status != domain.TaskDone {
			t.Fatalf("%s = %s", title, ts[title].Status)
		}
	}
	if ts["A"].Status != domain.TaskFailed {
		t.Fatalf("the replaced task stays failed in the record: %s", ts["A"].Status)
	}
	if got := ts["B"].DependsOn; len(got) != 1 || got[0] != res.Added["r"] {
		t.Fatalf("B depends on %v, want the new task", got)
	}
	if h.request(reqID).Status != domain.RequestDone || !h.hasAudit("request.plan_amended") {
		t.Fatalf("request %s / audit missing", h.request(reqID).Status)
	}
}

func TestAmendRunAddsToTheRunningScheduler(t *testing.T) {
	var broken atomic.Bool
	broken.Store(true)
	release := make(chan struct{})
	h := newHarness(t, dynChain(&broken, release), func(c *application.Config) { c.MaxRetries, c.TaskMaxAttempts = 1, 1 })
	reqID, _ := h.orch.Submit(context.Background(), "cadena")
	h.waitFor("A failed and D running", func() bool {
		ts := h.tasks(reqID)
		return ts["A"].Status == domain.TaskFailed && ts["D"].Status == domain.TaskRunning && ts["C"].Status == domain.TaskBlocked
	})
	ts := h.tasks(reqID)
	res, err := h.orch.AmendRun(context.Background(), reqID, application.RunAmendment{
		Add:     []application.NewTaskSpec{{Key: "r", Title: "R", AgentID: "analyst"}, {Key: "e", Title: "E", AgentID: "analyst", DependsOn: []string{"r", ts["D"].ID}}},
		Repoint: map[string][]string{ts["B"].ID: {"r"}},
		Requeue: []string{ts["B"].ID, ts["C"].ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Live {
		t.Fatal("the run was still going: the change must go to its scheduler")
	}
	h.waitFor("R, B and C done while D still runs", func() bool {
		ts := h.tasks(reqID)
		return ts["R"].Status == domain.TaskDone && ts["B"].Status == domain.TaskDone && ts["C"].Status == domain.TaskDone
	})
	if h.tasks(reqID)["E"].Status != domain.TaskPending {
		t.Fatal("E waits for D")
	}
	close(release)
	h.orch.Wait()
	for title, task := range h.tasks(reqID) {
		if title != "A" && task.Status != domain.TaskDone {
			t.Fatalf("%s = %s", title, task.Status)
		}
	}
	if h.request(reqID).Status != domain.RequestDone {
		t.Fatalf("request = %s", h.request(reqID).Status)
	}
}

func TestAmendRunRefusesRunningTasksCyclesAndUnknowns(t *testing.T) {
	var broken atomic.Bool
	broken.Store(true)
	release := make(chan struct{})
	h := newHarness(t, dynChain(&broken, release), func(c *application.Config) { c.MaxRetries, c.TaskMaxAttempts = 1, 1 })
	reqID, _ := h.orch.Submit(context.Background(), "cadena")
	h.waitFor("D running", func() bool {
		return h.tasks(reqID)["D"].Status == domain.TaskRunning && h.tasks(reqID)["C"].Status == domain.TaskBlocked
	})
	ts := h.tasks(reqID)
	ctx := context.Background()
	if _, err := h.orch.AmendRun(ctx, reqID, application.RunAmendment{Repoint: map[string][]string{ts["D"].ID: {ts["B"].ID}}}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("editing a running task: %v", err)
	}
	if _, err := h.orch.AmendRun(ctx, reqID, application.RunAmendment{Drop: []string{ts["A"].ID}}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("dropping a failed task: %v", err)
	}
	if _, err := h.orch.AmendRun(ctx, reqID, application.RunAmendment{Repoint: map[string][]string{ts["B"].ID: {ts["C"].ID}}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a cycle: %v", err)
	}
	if _, err := h.orch.AmendRun(ctx, reqID, application.RunAmendment{Add: []application.NewTaskSpec{{Key: "x", Title: "X", DependsOn: []string{"nope"}}}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown dependency: %v", err)
	}
	if got := h.tasks(reqID); len(got) != 4 || got["B"].DependsOn[0] != ts["A"].ID {
		t.Fatalf("a refused change must leave the plan untouched: %d tasks", len(got))
	}
	close(release)
	h.orch.Wait()
}

func TestAmendRunDropsAPendingTaskAndDependentsBypassIt(t *testing.T) {
	var broken atomic.Bool
	release := make(chan struct{})
	rt := chainRuntime(&broken, nil)
	inner := rt.runTask
	rt.runTask = func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		if in.Task.Title == "A" {
			<-release
		}
		return inner(in)
	}
	h := newHarness(t, rt, nil)
	reqID, _ := h.orch.Submit(context.Background(), "cadena")
	h.waitFor("A running", func() bool { return h.tasks(reqID)["A"].Status == domain.TaskRunning })
	ts := h.tasks(reqID)
	res, err := h.orch.AmendRun(context.Background(), reqID, application.RunAmendment{Drop: []string{ts["B"].ID}, Repoint: map[string][]string{ts["C"].ID: {ts["A"].ID}}})
	if err != nil || !res.Live {
		t.Fatalf("amend: %v %+v", err, res)
	}
	close(release)
	h.orch.Wait()
	ts = h.tasks(reqID)
	if ts["B"].Status != domain.TaskBlocked || ts["C"].Status != domain.TaskDone || ts["A"].Status != domain.TaskDone {
		t.Fatalf("A=%s B=%s C=%s", ts["A"].Status, ts["B"].Status, ts["C"].Status)
	}
	if h.request(reqID).Status != domain.RequestDone {
		t.Fatalf("request = %s", h.request(reqID).Status)
	}
}
