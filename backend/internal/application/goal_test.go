package application_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// fakeGoals: "goal-ok" is linkable and gives a note to every agent but "legal"; anything else is refused.
type fakeGoals struct {
	mu     sync.Mutex
	checks []string
}

func (f *fakeGoals) CheckLink(_ context.Context, org, user, id string) error {
	f.mu.Lock()
	f.checks = append(f.checks, org+"|"+user+"|"+id)
	f.mu.Unlock()
	if id != "goal-ok" {
		return fmt.Errorf("%w: unknown goal", domain.ErrInvalid)
	}
	return nil
}

func (f *fakeGoals) AgentNote(_ context.Context, _, id, agent string) (application.RecalledMemory, bool) {
	if id != "goal-ok" || agent == "legal" {
		return application.RecalledMemory{}, false
	}
	return application.RecalledMemory{Ref: "goal:" + id, Kind: "goal", Title: "Subir ventas T4", Snippet: "15 % más"}, true
}

type goalSpy struct {
	mu    sync.Mutex
	plans []application.PlanRequest
	runs  []application.RunTaskRequest
}

func goalRuntime(spy *goalSpy, agents ...string) *fakeRuntime {
	return &fakeRuntime{
		plan: func(in application.PlanRequest) (application.PlanResponse, error) {
			spy.mu.Lock()
			spy.plans = append(spy.plans, in)
			spy.mu.Unlock()
			var ts []application.PlannedTask
			for i, a := range agents {
				ts = append(ts, application.PlannedTask{Key: fmt.Sprint("t", i), Title: "Tarea de " + a, AgentID: a})
			}
			return application.PlanResponse{Tasks: ts}, nil
		},
		runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
			spy.mu.Lock()
			spy.runs = append(spy.runs, in)
			spy.mu.Unlock()
			return okResult("hecho"), nil
		},
	}
}

func TestGoalReachesThePlannerAndTheTasksAsData(t *testing.T) {
	spy := &goalSpy{}
	h := newHarness(t, goalRuntime(spy, "sales", "legal"), nil)
	g := &fakeGoals{}
	h.orch.SetGoals(g)
	ctx := application.WithGoal(application.WithActor(context.Background(), "u-ana"), "goal-ok")
	id, err := h.orch.Submit(ctx, "prepara la campaña de diciembre")
	if err != nil {
		t.Fatal(err)
	}
	h.waitFor("done", func() bool { r, _ := h.store.GetRequest(context.Background(), org, id); return r.Status == domain.RequestDone })
	r, _ := h.store.GetRequest(context.Background(), org, id)
	if r.GoalID != "goal-ok" {
		t.Fatalf("stored goal = %q", r.GoalID)
	}
	if len(g.checks) != 1 || g.checks[0] != org+"|u-ana|goal-ok" {
		t.Fatalf("checks = %v", g.checks)
	}
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if len(spy.plans) != 1 || len(spy.plans[0].Recalled) != 1 || spy.plans[0].Recalled[0].Ref != "goal:goal-ok" {
		t.Fatalf("plan recalled = %+v", spy.plans)
	}
	seen := map[string]bool{}
	for _, in := range spy.runs {
		hasGoal := len(in.Recalled) > 0 && in.Recalled[0].Ref == "goal:goal-ok"
		seen[in.Task.AgentID] = hasGoal
	}
	if !seen["sales"] || seen["legal"] {
		t.Fatalf("goal per agent = %v (sales gets it first, legal — sealed — does not)", seen)
	}
}

func TestRequestWithABadGoalIsNotCreated(t *testing.T) {
	spy := &goalSpy{}
	h := newHarness(t, goalRuntime(spy, "sales"), nil)
	before, _ := h.store.ListRequests(context.Background(), org)
	if _, err := h.orch.Submit(application.WithGoal(context.Background(), "goal-x"), "algo"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("without a goals service: %v", err)
	}
	h.orch.SetGoals(&fakeGoals{})
	if _, err := h.orch.Submit(application.WithGoal(context.Background(), "goal-other-org"), "algo"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("refused goal: %v", err)
	}
	if after, _ := h.store.ListRequests(context.Background(), org); len(after) != len(before) {
		t.Fatalf("a request was created: %d -> %d", len(before), len(after))
	}
}

func TestNoGoalChangesNothing(t *testing.T) {
	spy := &goalSpy{}
	h := newHarness(t, goalRuntime(spy, "sales"), nil)
	h.orch.SetGoals(&fakeGoals{})
	id, err := h.orch.Submit(context.Background(), "sin objetivo")
	if err != nil {
		t.Fatal(err)
	}
	h.waitFor("done", func() bool { r, _ := h.store.GetRequest(context.Background(), org, id); return r.Status == domain.RequestDone })
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if len(spy.plans) != 1 || spy.plans[0].Recalled != nil {
		t.Fatalf("plan = %+v", spy.plans)
	}
	for _, in := range spy.runs {
		if len(in.Recalled) != 0 {
			t.Fatalf("recalled = %+v", in.Recalled)
		}
	}
	if r, _ := h.store.GetRequest(context.Background(), org, id); r.GoalID != "" {
		t.Fatalf("goal = %q", r.GoalID)
	}
}
