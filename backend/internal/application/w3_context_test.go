package application_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// w3Runtime records the synthesis calls and the run-task contexts.
type w3Runtime struct {
	*fakeRuntime
	mu     sync.Mutex
	synth  []application.SynthesizeRequest
	runCtx map[string]application.RunContext // by task title
}

func (w *w3Runtime) Synthesize(_ context.Context, in application.SynthesizeRequest) (application.SynthesizeResponse, error) {
	w.mu.Lock()
	w.synth = append(w.synth, in)
	w.mu.Unlock()
	return application.SynthesizeResponse{Title: "Informe", Summary: "ok " + in.Stage,
		Sections: []domain.Section{{Heading: "Resumen", Body: "x"}},
		Usage:    application.Usage{Model: "m", InputTokens: 10, OutputTokens: 10, CostUSD: 0.001}}, nil
}

func (w *w3Runtime) RunTask(_ context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
	w.mu.Lock()
	w.runCtx[in.Task.Title] = in.Context
	w.mu.Unlock()
	r := okResult(strings.Repeat("resultado ", 100))
	r.Output.Findings = []string{"f"}
	return r, nil
}

func newW3(t *testing.T, tasks []application.PlannedTask, mutate func(*application.Config)) (*harness, *w3Runtime, string) {
	rt := &w3Runtime{runCtx: map[string]application.RunContext{}, fakeRuntime: &fakeRuntime{
		plan: func(application.PlanRequest) (application.PlanResponse, error) {
			return application.PlanResponse{Tasks: tasks}, nil
		}}}
	h := newHarness(t, rt, func(c *application.Config) {
		c.ConfirmThresholdUSD = 0
		if mutate != nil {
			mutate(c)
		}
	})
	id, err := h.orch.Submit(context.Background(), "proyecto grande")
	if err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	return h, rt, id
}

func manyTasks(n int) []application.PlannedTask {
	var ts []application.PlannedTask
	for i := 0; i < n; i++ {
		ts = append(ts, application.PlannedTask{Key: fmt.Sprintf("k%d", i), Title: fmt.Sprintf("T%02d", i), AgentID: "analyst"})
	}
	return ts
}

func TestSmallRequestKeepsSinglePassSynthesis(t *testing.T) {
	h, rt, id := newW3(t, manyTasks(3), nil)
	if got := h.request(id).Status; got != domain.RequestDone {
		t.Fatalf("status = %s", got)
	}
	if len(rt.synth) != 1 || rt.synth[0].Stage != "" || len(rt.synth[0].Outputs) != 3 {
		t.Fatalf("synth calls = %+v", rt.synth)
	}
}

func TestLargeRequestSynthesisIsHierarchicalAndBounded(t *testing.T) {
	h, rt, id := newW3(t, manyTasks(40), func(c *application.Config) {
		c.SynthTokenBudget, c.SynthMaxGroups = 600, 4
	})
	req := h.request(id)
	if req.Status != domain.RequestDone || req.ReportID == nil {
		t.Fatalf("status = %s", req.Status)
	}
	if n := len(rt.synth); n < 3 || n > 5 {
		t.Fatalf("synthesis calls = %d, want 3..5 (groups<=4 plus final)", n)
	}
	final := rt.synth[len(rt.synth)-1]
	if final.Stage != "final" || final.Parts != len(rt.synth)-1 {
		t.Fatalf("last call = %+v", final)
	}
	for i, c := range rt.synth[:len(rt.synth)-1] {
		if c.Stage != "group" || c.Part != i+1 {
			t.Fatalf("call %d = stage %q part %d", i, c.Stage, c.Part)
		}
	}
	// every call is accounted in the request cost (0.001 each).
	if req.CostUSD < 0.001*float64(len(rt.synth))-1e-9 {
		t.Errorf("cost %.4f does not include %d synthesis calls", req.CostUSD, len(rt.synth))
	}
}

func TestDependencyBudgetEnforcedInRun(t *testing.T) {
	tasks := manyTasks(30)
	var deps []string
	for _, tk := range tasks {
		deps = append(deps, tk.Key)
	}
	tasks = append(tasks, application.PlannedTask{Key: "sink", Title: "Sink", AgentID: "analyst", DependsOn: deps})
	_, rt, _ := newW3(t, tasks, func(c *application.Config) { c.DepContextTokenBudget = 400 })
	rc := rt.runCtx["Sink"]
	if len(rc.DependencyOutputs)+rc.DependencyOmitted != 30 || len(rc.DependencyOutputs) == 0 {
		t.Fatalf("deps=%d omitted=%d", len(rc.DependencyOutputs), rc.DependencyOmitted)
	}
	tok := 0
	for _, d := range rc.DependencyOutputs {
		tok += (len(d.Output.Summary) + 3) / 4
		if d.Ref == "" {
			t.Fatal("missing ref")
		}
	}
	if tok > 400 {
		t.Fatalf("dependency context = %d tokens, budget 400", tok)
	}
}

// ownerGate marks every request as a project (no gating).
type ownerGate struct{}

func (ownerGate) GateTask(context.Context, string, domain.Task) application.GateDecision {
	return application.GateDecision{}
}
func (ownerGate) GateResolved(context.Context, string, domain.Task, string, bool) {}
func (ownerGate) OwnsRequest(context.Context, string, string) bool                { return true }
func (ownerGate) ResumeRequest(context.Context, string, string) bool              { return true }

func TestProjectContextOnlyForProjectRequests(t *testing.T) {
	chain := []application.PlannedTask{
		{Key: "a", Title: "A", AgentID: "analyst"},
		{Key: "b", Title: "B", AgentID: "analyst", DependsOn: []string{"a"}},
		{Key: "c", Title: "C", AgentID: "analyst", DependsOn: []string{"b"}},
	}
	_, plain, _ := newW3(t, chain, nil)
	if plain.runCtx["C"].ProjectContext != nil {
		t.Error("an ordinary request must not get a project context")
	}

	rt := &w3Runtime{runCtx: map[string]application.RunContext{}, fakeRuntime: &fakeRuntime{
		plan: func(application.PlanRequest) (application.PlanResponse, error) {
			return application.PlanResponse{Tasks: chain}, nil
		}}}
	h := newHarness(t, rt, func(c *application.Config) { c.ConfirmThresholdUSD = 0 })
	h.orch.SetTaskGate(ownerGate{})
	if _, err := h.orch.Submit(context.Background(), "proyecto"); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	pc := rt.runCtx["C"].ProjectContext
	if pc == nil || len(pc.Index) != 1 || pc.Index[0].Title != "A" || pc.Index[0].Ref == "" {
		t.Fatalf("project context of C = %+v (want the index of A only, B is a direct dependency)", pc)
	}
	if rt.runCtx["A"].ProjectContext != nil {
		t.Error("the first task has nothing to index")
	}
}
