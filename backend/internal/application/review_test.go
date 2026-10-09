package application_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/roles"
)

// reviewGate is a TaskGate (no gating) that also decides which tasks are reviewed.
type reviewGate struct {
	mode     string
	criteria []string
}

func (g *reviewGate) GateTask(context.Context, string, domain.Task) application.GateDecision {
	return application.GateDecision{}
}
func (g *reviewGate) GateResolved(context.Context, string, domain.Task, string, bool) {}
func (g *reviewGate) ReviewSpec(context.Context, string, domain.Task) application.ReviewSpec {
	return application.ReviewSpec{Mode: g.mode, Criteria: g.criteria}
}

// reviewRT is a runtime with a scripted reviewer.
type reviewRT struct {
	*fakeRuntime
	mu       sync.Mutex
	verdicts []string // verdict of the n-th review call (the last one repeats)
	reviews  []application.ReviewRequest
	runs     []application.RunTaskRequest
	synth    []application.SynthesizeRequest
	conf     float64
}

func (r *reviewRT) Review(_ context.Context, in application.ReviewRequest) (application.ReviewResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := min(len(r.reviews), len(r.verdicts)-1)
	r.reviews = append(r.reviews, in)
	v := r.verdicts[i]
	return application.ReviewResponse{Verdict: v, Reasons: []string{"reason " + v},
		Usage: application.Usage{Model: "m", InputTokens: 10, OutputTokens: 10, CostUSD: 0.01}}, nil
}

func (r *reviewRT) RunTask(_ context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
	r.mu.Lock()
	r.runs = append(r.runs, in)
	r.mu.Unlock()
	res := okResult("hecho: " + in.Task.Title)
	if r.conf > 0 {
		res.Output.Confidence = r.conf
	}
	res.Usage = application.Usage{Model: "m", InputTokens: 10, OutputTokens: 10, CostUSD: 0.02}
	return res, nil
}

func (r *reviewRT) Synthesize(_ context.Context, in application.SynthesizeRequest) (application.SynthesizeResponse, error) {
	r.mu.Lock()
	r.synth = append(r.synth, in)
	r.mu.Unlock()
	return application.SynthesizeResponse{Title: "Informe", Summary: "ok", Sections: []domain.Section{{Heading: "Resumen", Body: "x"}}}, nil
}

func oneTask(agent string) func(application.PlanRequest) (application.PlanResponse, error) {
	return func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "Calcular total", AgentID: agent}}}, nil
	}
}

// runReview runs a one-task request with a gate that reviews with the given mode.
func runReview(t *testing.T, rt *reviewRT, gate *reviewGate, mutate func(*application.Config)) (*harness, string, domain.Task) {
	t.Helper()
	h := newHarness(t, rt, func(c *application.Config) {
		c.ConfirmThresholdUSD = 0
		if mutate != nil {
			mutate(c)
		}
	})
	if gate != nil {
		h.orch.SetTaskGate(gate)
	}
	id, err := h.orch.Submit(context.Background(), "calcula el total")
	if err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	var task domain.Task
	for _, tk := range h.tasks(id) {
		task = tk
	}
	return h, id, task
}

func newReviewRT(conf float64, verdicts ...string) *reviewRT {
	rt := &reviewRT{fakeRuntime: &fakeRuntime{plan: oneTask("analyst")}, verdicts: verdicts, conf: conf}
	return rt
}

var crit = []string{"incluye el total", "cita la fuente"}

func TestReviewPassKeepsOutputAndAccountsCost(t *testing.T) {
	rt := newReviewRT(0, "pass")
	h, id, task := runReview(t, rt, &reviewGate{mode: application.ReviewAlways, criteria: crit}, nil)
	if task.Status != domain.TaskDone || task.Output == nil || task.Output.Review == nil {
		t.Fatalf("task = %+v", task)
	}
	r := task.Output.Review
	if r.Verdict != domain.VerdictPass || r.Reworks != 0 || r.Exhausted || r.Reviewer != "assistant" {
		t.Fatalf("review = %+v", r)
	}
	if len(rt.runs) != 1 || len(rt.reviews) != 1 {
		t.Fatalf("runs=%d reviews=%d", len(rt.runs), len(rt.reviews))
	}
	if got := rt.runs[0].Task.Acceptance; len(got) != 2 {
		t.Fatalf("worker did not receive the criteria: %v", got)
	}
	if rt.reviews[0].Reviewer.Role != "reviewer" || len(rt.reviews[0].Acceptance) != 2 {
		t.Fatalf("review request = %+v", rt.reviews[0])
	}
	// run 0.02 + review 0.01 (+ synthesis usage is zero in this runtime)
	if c := h.request(id).CostUSD; c < 0.0299 || c > 0.0301 {
		t.Fatalf("request cost = %v, want 0.03 (run + review)", c)
	}
	if !h.hasAudit("task.reviewed") || len(h.eventsOf(domain.EvTaskReviewed)) != 1 {
		t.Fatal("missing task.reviewed audit/event")
	}
	// the quality section reaches the synthesis input and the report
	last := rt.synth[len(rt.synth)-1].Outputs
	if q := last[len(last)-1]; q.TaskID != "quality" || q.Output.Metrics["pass"] != 1 {
		t.Fatalf("quality input = %+v", q)
	}
}

func TestReviewReworkRunsAgainWithNotes(t *testing.T) {
	rt := newReviewRT(0, "rework", "pass")
	h, id, task := runReview(t, rt, &reviewGate{mode: application.ReviewAlways, criteria: crit}, nil)
	if len(rt.runs) != 2 || len(rt.reviews) != 2 {
		t.Fatalf("runs=%d reviews=%d", len(rt.runs), len(rt.reviews))
	}
	if rt.runs[0].Rework != nil {
		t.Fatal("the first run must not be a rework")
	}
	rw := rt.runs[1].Rework
	if rw == nil || rw.Attempt != 1 || len(rw.Notes) == 0 || !strings.Contains(rw.Notes[0], "reason rework") {
		t.Fatalf("rework = %+v", rw)
	}
	if rt.reviews[1].Round != 1 {
		t.Fatalf("second review round = %d", rt.reviews[1].Round)
	}
	r := task.Output.Review
	if task.Status != domain.TaskDone || r.Verdict != domain.VerdictPass || r.Reworks != 1 || r.Exhausted {
		t.Fatalf("task=%s review=%+v", task.Status, r)
	}
	// 2 runs (0.04) + 2 reviews (0.02)
	if c := h.request(id).CostUSD; c < 0.0599 || c > 0.0601 {
		t.Fatalf("request cost = %v, want 0.06", c)
	}
}

func TestReworkIsBounded(t *testing.T) {
	for _, tc := range []struct{ max, runs int }{{0, 1}, {1, 2}, {2, 3}, {9, 4}} { // 9 is clamped to 3
		rt := newReviewRT(0, "rework")
		_, _, task := runReview(t, rt, &reviewGate{mode: application.ReviewAlways, criteria: crit}, func(c *application.Config) { c.QualityMaxRework = tc.max })
		if len(rt.runs) != tc.runs || len(rt.reviews) != tc.runs {
			t.Fatalf("max=%d: runs=%d reviews=%d, want %d", tc.max, len(rt.runs), len(rt.reviews), tc.runs)
		}
		r := task.Output.Review
		if task.Status != domain.TaskDone || r.Verdict != domain.VerdictRework || !r.Exhausted || r.Reworks != tc.runs-1 {
			t.Fatalf("max=%d: task=%s review=%+v", tc.max, task.Status, r)
		}
	}
	// default: one rework
	if got := application.DefaultConfig().QualityMaxRework; got != 1 {
		t.Fatalf("default QUALITY_MAX_REWORK = %d", got)
	}
}

func TestReviewFailFailsTheTask(t *testing.T) {
	rt := newReviewRT(0, "fail")
	h, _, task := runReview(t, rt, &reviewGate{mode: application.ReviewAlways, criteria: crit}, nil)
	if task.Status != domain.TaskFailed || task.Output == nil || task.Output.Review == nil || task.Output.Review.Verdict != domain.VerdictFail {
		t.Fatalf("task = %+v", task)
	}
	if len(rt.runs) != 1 {
		t.Fatalf("a failed review must not rerun: %d runs", len(rt.runs))
	}
	if !h.hasAudit("task.reviewed") {
		t.Fatal("missing audit")
	}
}

func TestReviewIsOffByDefault(t *testing.T) {
	// no gate at all (the $50,000 flow), a gate that says off, and a gate with a mode but no criteria
	for name, gate := range map[string]*reviewGate{
		"no gate": nil, "off": {mode: application.ReviewOff, criteria: crit}, "no criteria": {mode: application.ReviewAlways},
	} {
		rt := newReviewRT(0, "rework")
		h, id, task := runReview(t, rt, gate, nil)
		if len(rt.reviews) != 0 || len(rt.runs) != 1 || task.Output.Review != nil {
			t.Fatalf("%s: reviewed (reviews=%d runs=%d)", name, len(rt.reviews), len(rt.runs))
		}
		if rt.runs[0].Task.Acceptance != nil {
			t.Fatalf("%s: criteria leaked into the run request", name)
		}
		for _, o := range rt.synth[len(rt.synth)-1].Outputs {
			if o.TaskID == "quality" {
				t.Fatalf("%s: quality input without reviews", name)
			}
		}
		if h.request(id).Status != domain.RequestDone {
			t.Fatalf("%s: status %s", name, h.request(id).Status)
		}
	}
}

func TestLowConfidenceModeOnlyReviewsUnsureOutputs(t *testing.T) {
	rt := newReviewRT(0.9, "pass")
	runReview(t, rt, &reviewGate{mode: application.ReviewLowConfidence, criteria: crit}, nil)
	if len(rt.reviews) != 0 {
		t.Fatal("a confident output must not be reviewed in low_confidence mode")
	}
	rt = newReviewRT(0.3, "pass")
	_, _, task := runReview(t, rt, &reviewGate{mode: application.ReviewLowConfidence, criteria: crit}, nil)
	if len(rt.reviews) != 1 || task.Output.Review == nil {
		t.Fatal("an unsure output must be reviewed")
	}
}

// noReviewRT is a runtime without the optional review capability.
type noReviewRT struct{ *fakeRuntime }

func TestRuntimeWithoutReviewerNeverBlocksWork(t *testing.T) {
	rt := &noReviewRT{&fakeRuntime{plan: oneTask("analyst")}}
	h := newHarness(t, rt, func(c *application.Config) { c.ConfirmThresholdUSD = 0 })
	h.orch.SetTaskGate(&reviewGate{mode: application.ReviewAlways, criteria: crit})
	id, _ := h.orch.Submit(context.Background(), "x")
	h.orch.Wait()
	for _, tk := range h.tasks(id) {
		if tk.Status != domain.TaskDone || tk.Output.Review != nil {
			t.Fatalf("task = %+v", tk)
		}
	}
	if !h.hasAudit("task.review_skipped") {
		t.Fatal("a skipped review must be audited")
	}
}

func hireAuditor(t *testing.T, h *harness) string {
	t.Helper()
	a, err := h.orch.HireFromTemplate(context.Background(), application.HireInput{TemplateID: "internal_auditor"})
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func TestAuditorIsTheReviewerWhenPresent(t *testing.T) {
	rt := newReviewRT(0, "pass")
	h := newHarness(t, rt, func(c *application.Config) { c.ConfirmThresholdUSD = 0 })
	aud := hireAuditor(t, h)
	h.orch.SetTaskGate(&reviewGate{mode: application.ReviewAlways, criteria: crit})
	id, _ := h.orch.Submit(context.Background(), "x")
	h.orch.Wait()
	if len(rt.reviews) != 1 || rt.reviews[0].Reviewer.ID != aud || rt.reviews[0].Reviewer.Role != "internal_auditor" || len(rt.reviews[0].Reviewer.Tools) != 0 {
		t.Fatalf("reviewer = %+v", rt.reviews)
	}
	for _, tk := range h.tasks(id) {
		if tk.Output.Review == nil || tk.Output.Review.Reviewer != aud {
			t.Fatalf("review = %+v", tk.Output.Review)
		}
	}
	// the auditor has no tools and no way to approve anything
	tp, _ := roles.Get("internal_auditor")
	if len(tp.Tools) != 0 || tp.RiskTier != "green" || tp.Seed != nil {
		t.Fatalf("template = %+v", tp)
	}
}

func TestAuditorCannotClaimVerifiedWithoutEvidence(t *testing.T) {
	for name, tc := range map[string]struct {
		evidence []string
		want     string
	}{
		"none":     {nil, application.AuditUnverified},
		"one only": {[]string{"task:a.metrics.total=10"}, application.AuditUnverified},
		"two":      {[]string{"task:a.metrics.total=10", "task:b.metrics.total=10"}, application.AuditVerified},
	} {
		rt := &fakeRuntime{}
		h := newHarness(t, rt, func(c *application.Config) { c.ConfirmThresholdUSD = 0 })
		aud := hireAuditor(t, h)
		rt.plan = oneTask(aud)
		rt.runTask = func(application.RunTaskRequest) (application.RunTaskResponse, error) {
			r := okResult("verificado")
			r.Output.Metrics = map[string]any{"audit_status": "verified"}
			r.Output.Evidence = tc.evidence
			return r, nil
		}
		id, _ := h.orch.Submit(context.Background(), "audita")
		h.orch.Wait()
		for _, tk := range h.tasks(id) {
			if got := tk.Output.Metrics["audit_status"]; got != tc.want {
				t.Fatalf("%s: audit_status = %v, want %s", name, got, tc.want)
			}
		}
	}
}
