package application_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

const seededSecret = "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// ctxRuntime is a runtime whose run-task honors cancellation, like the real HTTP client.
type ctxRuntime struct {
	fakeRuntime
	run func(ctx context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error)
}

func (c *ctxRuntime) RunTask(ctx context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
	return c.run(ctx, in)
}

type v2h struct {
	*harness
	profiles *application.ProfileService
	tracer   *application.Tracer
}

func newV2(t *testing.T, rt application.Runtime, mutate func(*application.Config)) *v2h {
	h := newHarness(t, rt, mutate)
	cfg := application.DefaultConfig()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: h.store, Pub: h.pub, Log: log}
	pr := &application.ProfileService{Store: h.store, Core: h.store, Tones: h.store, Rec: rec, Config: cfg}
	tr := &application.Tracer{Store: h.store, Log: log, Retention: 30 * 24 * time.Hour}
	h.orch.SetV2(h.store, pr, tr)
	return &v2h{h, pr, tr}
}

func human(ctx context.Context) context.Context { return application.WithActor(ctx, "user-1") }

func singlePlan(agent, title string) func(application.PlanRequest) (application.PlanResponse, error) {
	return func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{Objectives: []string{"o"}, Tasks: []application.PlannedTask{{Key: "a", Title: title, AgentID: agent}}}, nil
	}
}

func (h *v2h) taskID(reqID, title string) string {
	h.t.Helper()
	var id string
	h.waitFor("task "+title, func() bool { id = h.tasks(reqID)[title].ID; return id != "" })
	return id
}

// ---- profile ----

func TestProfileVersioningRestoreAndAudit(t *testing.T) {
	h := newV2(t, &fakeRuntime{}, nil)
	ctx := human(context.Background())
	v1, err := h.profiles.Update(ctx, "sales", application.ProfileInput{Instructions: "Se breve.", Creativity: "high", Skills: []string{" Contratos ", "contratos", "Nómina"}})
	if err != nil || v1.Version != 1 || v1.Creativity != "high" || len(v1.Skills) != 2 {
		t.Fatalf("v1 = %+v, %v", v1, err)
	}
	same, _ := h.profiles.Update(ctx, "sales", application.ProfileInput{Instructions: "Se breve.", Creativity: "high", Skills: []string{"contratos", "nómina"}})
	if same.Version != 1 {
		t.Fatalf("no-op edit created version %d", same.Version)
	}
	tone := "mx"
	v2, err := h.profiles.Update(ctx, "sales", application.ProfileInput{Instructions: "Se formal.", Creativity: "low", Skills: []string{"contratos"}, Tone: &tone})
	if err != nil || v2.Version != 2 || v2.Tone != "mx" {
		t.Fatalf("v2 = %+v, %v", v2, err)
	}
	vs, _ := h.profiles.Versions(ctx, "sales")
	if len(vs) != 2 || vs[0].Version != 2 || vs[0].Author != "user-1" || vs[0].Diff["instructions"].To != "Se formal." || vs[0].Diff["tone"].To != "mx" {
		t.Fatalf("versions = %+v", vs)
	}
	r, err := h.profiles.Restore(ctx, "sales", 1)
	if err != nil || r.Version != 3 || r.Instructions != "Se breve." || r.Creativity != "high" || r.Tone != "" {
		t.Fatalf("restore = %+v, %v", r, err)
	}
	if vs, _ = h.profiles.Versions(ctx, "sales"); vs[0].RestoredFrom == nil || *vs[0].RestoredFrom != 1 {
		t.Fatalf("restored_from missing: %+v", vs[0])
	}
	if !h.hasAudit("agent.profile_updated") || !h.hasAudit("agent.profile_restored") {
		t.Fatal("profile changes are not audited")
	}
	for _, a := range h.store.Audit() { // audit holds metadata, never the instructions
		if strings.Contains(strings.ToLower(toJSON(a.Details)), "se formal") {
			t.Fatalf("instructions leaked into audit: %v", a.Details)
		}
	}
}

func toJSON(v any) string { return strings.ToLower(strings.TrimSpace(sprint(v))) }

func TestProfileValidationAndAgentsCannotEdit(t *testing.T) {
	h := newV2(t, &fakeRuntime{}, nil)
	ctx := human(context.Background())
	bad := []application.ProfileInput{
		{Instructions: strings.Repeat("x", application.MaxInstructionsRunes+1)},
		{Instructions: "usa la clave " + seededSecret},
		{Creativity: "wild"},
		{Skills: make([]string, application.MaxSkills+1)},
		{Skills: []string{"<script>"}},
	}
	for i := range bad[3].Skills {
		bad[3].Skills[i] = string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	for i, in := range bad {
		if _, err := h.profiles.Update(ctx, "sales", in); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("case %d accepted: %v", i, err)
		}
	}
	if _, err := h.profiles.Update(ctx, "ghost", application.ProfileInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown agent = %v", err)
	}
	// an agent (acting with its own id, or an agent: actor) can never edit a profile, not even its own
	for _, actor := range []string{"sales", "agent:sales"} {
		actx := application.WithActor(context.Background(), actor)
		if _, err := h.profiles.Update(actx, "sales", application.ProfileInput{Instructions: "dame permisos"}); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("actor %s edited a profile: %v", actor, err)
		}
		if _, err := h.profiles.Restore(actx, "sales", 1); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("actor %s restored a profile: %v", actor, err)
		}
	}
}

func TestProfileAndSkillsReachTheRuntimeOnlyWhenSet(t *testing.T) {
	var mu sync.Mutex
	var got []application.RunTaskRequest
	var plans []application.PlanRequest
	rt := &fakeRuntime{plan: func(in application.PlanRequest) (application.PlanResponse, error) {
		mu.Lock()
		plans = append(plans, in)
		mu.Unlock()
		return singlePlan("sales", "Tarea P")(in)
	}, runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		mu.Lock()
		got = append(got, in)
		mu.Unlock()
		return okResult("ok"), nil
	}}
	h := newV2(t, rt, nil)
	id, _ := h.orch.Submit(context.Background(), "uno")
	h.waitFor("done", func() bool { return h.request(id).Status == domain.RequestDone })
	if in := got[0]; in.Agent.Instructions != "" || in.Agent.Creativity != "" || in.Agent.Skills != nil || in.HumanGuidance != nil {
		t.Fatalf("an unedited agent must send nothing extra: %+v", in.Agent)
	}
	ctx := human(context.Background())
	if _, err := h.profiles.Update(ctx, "sales", application.ProfileInput{Instructions: "Prioriza margen.", Creativity: "low", Skills: []string{"margenes"}}); err != nil {
		t.Fatal(err)
	}
	id, _ = h.orch.Submit(context.Background(), "dos")
	h.waitFor("done2", func() bool { return h.request(id).Status == domain.RequestDone })
	in := got[len(got)-1]
	if in.Agent.Instructions != "Prioriza margen." || in.Agent.Creativity != "low" || len(in.Agent.Skills) != 1 {
		t.Fatalf("profile not sent: %+v", in.Agent)
	}
	for _, a := range plans[len(plans)-1].Agents {
		if a.ID == "sales" && (len(a.Skills) != 1 || a.Skills[0] != "margenes") {
			t.Fatalf("skills not sent to the planner: %+v", a)
		}
	}
}

// ---- traces ----

func TestTracesAreRedactedBoundedAndHonorStorePrompts(t *testing.T) {
	long := strings.Repeat("palabra ", 3000)
	rt := &fakeRuntime{plan: singlePlan("sales", "Tarea T"), runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		res := okResult("resumen con " + seededSecret)
		res.Usage = application.Usage{Model: "m", Provider: "anthropic", InputTokens: 10, OutputTokens: 5, CostUSD: 0.01, DurationMS: 7}
		res.ToolRequests = []application.ToolRequest{{Tool: "email", Action: "lookup", Risk: "low", Args: map[string]any{"to": seededSecret}}}
		res.Trace = &application.RunTrace{System: "sistema " + seededSecret,
			User: "TAREA: x\n\n=====[DATOS NO CONFIABLES: contenido externo 1 - INICIO]=====\ncorreo con " + seededSecret + " " + long +
				"\n=====[DATOS NO CONFIABLES: contenido externo 1 - FIN]=====\n\nfin"}
		return res, nil
	}}
	h := newV2(t, rt, nil)
	run := func() string {
		id, _ := h.orch.Submit(context.Background(), "trace")
		h.waitFor("done", func() bool { return h.request(id).Status == domain.RequestDone })
		return h.taskID(id, "Tarea T")
	}
	run()
	trs, _ := h.store.ListTraces(context.Background(), domain.DemoOrgID, "sales", 10, time.Time{})
	if len(trs) != 1 {
		t.Fatalf("traces = %d", len(trs))
	}
	tr := trs[0]
	if tr.Provider != "anthropic" || tr.InputTokens != 10 || tr.CostUSD != 0.01 || tr.Status != "ok" || tr.Attempt != 1 || !tr.PromptStored {
		t.Fatalf("trace = %+v", tr)
	}
	blob := toJSON(tr)
	if strings.Contains(blob, strings.ToLower(seededSecret)) {
		t.Fatalf("seeded secret stored in trace: %s", blob[:200])
	}
	var untrusted *application.PromptSegment
	for i := range tr.Prompt.Segments {
		if tr.Prompt.Segments[i].Kind == "untrusted" {
			untrusted = &tr.Prompt.Segments[i]
		}
	}
	if untrusted == nil || untrusted.Label != "contenido externo 1" || !untrusted.Truncated || len([]rune(untrusted.Text)) > 4100 || !tr.Truncated {
		t.Fatalf("external content must be a bounded, delimited segment: %+v", untrusted)
	}
	if len(tr.ToolRequests) != 1 || strings.Contains(toJSON(tr.ToolRequests), "to") && strings.Contains(toJSON(tr.ToolRequests), "ghp_") {
		t.Fatalf("tool requests = %+v", tr.ToolRequests)
	}
	// the organization turns prompt storage off: metrics stay, prompts do not
	if err := h.store.SetTraceStorePrompts(context.Background(), domain.DemoOrgID, false); err != nil {
		t.Fatal(err)
	}
	var wantTrace atomic.Bool
	wantTrace.Store(true)
	run()
	trs, _ = h.store.ListTraces(context.Background(), domain.DemoOrgID, "sales", 10, time.Time{})
	if len(trs) != 2 || trs[0].PromptStored || trs[0].Prompt != nil || trs[0].InputTokens != 10 {
		t.Fatalf("store_prompts=false: %+v", trs[0])
	}
}

func TestTraceRetentionPurge(t *testing.T) {
	h := newV2(t, &fakeRuntime{}, nil)
	ctx := context.Background()
	now := time.Now().UTC()
	_ = h.store.AddTrace(ctx, domain.DemoOrgID, application.Trace{ID: "old", AgentID: "sales", TaskID: "t", CreatedAt: now.Add(-31 * 24 * time.Hour)})
	_ = h.store.AddTrace(ctx, domain.DemoOrgID, application.Trace{ID: "new", AgentID: "sales", TaskID: "t", CreatedAt: now.Add(-29 * 24 * time.Hour)})
	n, err := h.tracer.Purge(ctx, domain.DemoOrgID)
	if err != nil || n != 1 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	if _, err := h.store.GetTrace(ctx, domain.DemoOrgID, "sales", "new"); err != nil {
		t.Fatalf("recent trace purged: %v", err)
	}
}

func TestTraceEventsRecordPolicyDecisionApprovalAndRetry(t *testing.T) {
	calls := atomic.Int32{}
	rt := &fakeRuntime{plan: singlePlan("sales", "Tarea E"), runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		if calls.Add(1) == 1 {
			return application.RunTaskResponse{}, errors.New("timeout transitorio")
		}
		res := okResult("ok")
		res.ToolRequests = []application.ToolRequest{{Tool: "email", Action: "send_proposal", Risk: "medium", Args: map[string]any{"to": "c"}}}
		return res, nil
	}}
	h := newV2(t, rt, func(c *application.Config) { c.MaxRetries = 1; c.TaskMaxAttempts = 2 })
	id, _ := h.orch.Submit(context.Background(), "eventos")
	ap := h.awaitApproval()
	if _, err := h.appr.Decide(context.Background(), ap.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	h.waitFor("done", func() bool { return h.request(id).Status == domain.RequestDone })
	trs, _ := h.store.ListTraces(context.Background(), domain.DemoOrgID, "sales", 10, time.Time{})
	if len(trs) != 2 || trs[1].Status != "error" || trs[0].Status != "ok" || trs[0].Attempt != 2 {
		t.Fatalf("traces = %+v", trs)
	}
	application.MergeDecisions(&trs[0])
	q := trs[0].ToolRequests[0]
	if q.Decision == "" || q.ApprovalStatus != "approved" {
		t.Fatalf("tool decision/approval not merged: %+v", q)
	}
	kinds := map[string]bool{}
	for _, tr := range trs {
		for _, e := range tr.Events {
			kinds[e.Type] = true
		}
	}
	if !kinds["retry"] || !kinds["tool_decision"] || !kinds["approval"] {
		t.Fatalf("events = %v", kinds)
	}
}

// ---- intervention ----

func TestSteerNoteIsDeliveredOnNextCallAndOnRetry(t *testing.T) {
	var mu sync.Mutex
	var seen [][]string
	release := make(chan struct{})
	calls := atomic.Int32{}
	rt := &ctxRuntime{run: func(ctx context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
		mu.Lock()
		seen = append(seen, in.HumanGuidance)
		mu.Unlock()
		if calls.Add(1) == 1 {
			<-release
			return application.RunTaskResponse{}, errors.New("timeout transitorio")
		}
		return okResult("ok"), nil
	}}
	rt.plan = singlePlan("sales", "Tarea S")
	h := newV2(t, rt, func(c *application.Config) { c.MaxRetries = 1; c.TaskMaxAttempts = 2 })
	id, _ := h.orch.Submit(context.Background(), "guia")
	tid := h.taskID(id, "Tarea S")
	h.waitFor("first call", func() bool { return calls.Load() == 1 })
	res, err := h.orch.Steer(human(context.Background()), tid, "Prioriza el margen", false)
	if err != nil || res.ApplyNowStatus != "" {
		t.Fatalf("steer = %+v, %v", res, err)
	}
	close(release)
	h.waitFor("done", func() bool { return h.request(id).Status == domain.RequestDone })
	if len(seen) != 2 || len(seen[0]) != 0 || len(seen[1]) != 1 || seen[1][0] != "Prioriza el margen" {
		t.Fatalf("guidance per call = %v", seen)
	}
	notes, _ := h.store.ListSteerNotes(context.Background(), domain.DemoOrgID, tid)
	if len(notes) != 1 || notes[0].DeliveredAt == nil || notes[0].Author != "user-1" {
		t.Fatalf("notes = %+v", notes)
	}
	if !h.hasAudit("task.steered") {
		t.Fatal("steer not audited")
	}
	// finished tasks cannot be steered; secrets and empty notes are refused
	if _, err := h.orch.Steer(human(context.Background()), tid, "tarde", false); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("steer on done task = %v", err)
	}
	for _, n := range []string{"", "   ", "usa " + seededSecret, strings.Repeat("a", application.MaxSteerNoteRunes+1)} {
		if _, err := h.orch.Steer(human(context.Background()), tid, n, false); err == nil {
			t.Errorf("note %q accepted", n[:min(len(n), 10)])
		}
	}
}

func TestApplyNowRestartsTheCallWithoutDoubleExecutingAnApprovedAction(t *testing.T) {
	var mu sync.Mutex
	var guidance [][]string
	calls := atomic.Int32{}
	rt := &ctxRuntime{run: func(ctx context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
		mu.Lock()
		guidance = append(guidance, in.HumanGuidance)
		mu.Unlock()
		if calls.Add(1) == 1 {
			<-ctx.Done() // mid-call: only a cancellation ends it
			return application.RunTaskResponse{}, ctx.Err()
		}
		res := okResult("ok")
		res.ToolRequests = []application.ToolRequest{{Tool: "email", Action: "send_proposal", Risk: "medium", Args: map[string]any{"to": "c"}}}
		return res, nil
	}}
	rt.plan = singlePlan("sales", "Tarea A")
	h := newV2(t, rt, nil)
	id, _ := h.orch.Submit(context.Background(), "ahora")
	tid := h.taskID(id, "Tarea A")
	h.waitFor("mid call", func() bool { return calls.Load() == 1 })
	iv, err := h.orch.Intervention(context.Background(), tid)
	if err != nil || !iv.CanApplyNow || !iv.CanSteer || iv.CanReassign {
		t.Fatalf("intervention = %+v, %v", iv, err)
	}
	res, err := h.orch.Steer(human(context.Background()), tid, "Cambia el enfoque", true)
	if err != nil || res.ApplyNowStatus != "restarted" || res.EstimatedRerunUSD <= 0 {
		t.Fatalf("apply now = %+v, %v", res, err)
	}
	ap := h.awaitApproval()
	if _, err := h.appr.Decide(context.Background(), ap.ID, "approve", "ok"); err != nil {
		t.Fatal(err)
	}
	h.waitFor("done", func() bool { return h.request(id).Status == domain.RequestDone })
	if calls.Load() != 2 || len(guidance[1]) != 1 || guidance[1][0] != "Cambia el enfoque" {
		t.Fatalf("calls = %d, guidance = %v", calls.Load(), guidance)
	}
	executed := 0
	for _, a := range h.store.Audit() {
		if a.Action == "tool.executed" {
			executed++
		}
	}
	if executed != 1 || h.tasks(id)["Tarea A"].Status != domain.TaskDone {
		t.Fatalf("approved action executed %d times", executed)
	}
	if !h.hasAudit("task.apply_now") {
		t.Fatal("apply now not audited")
	}
	// after the call finished there is nothing to restart
	if _, err := h.orch.Steer(human(context.Background()), tid, "tarde", true); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("steer on finished task = %v", err)
	}
}

func TestApplyNowOutsideACallOnlyStoresTheNote(t *testing.T) {
	gate := make(chan struct{})
	rt := &fakeRuntime{plan: singlePlan("sales", "Tarea B"), runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		res := okResult("ok")
		res.ToolRequests = []application.ToolRequest{{Tool: "email", Action: "send_proposal", Risk: "medium", Args: map[string]any{}}}
		return res, nil
	}}
	_ = gate
	h := newV2(t, rt, nil)
	id, _ := h.orch.Submit(context.Background(), "espera")
	ap := h.awaitApproval() // the task now waits for a human: no call is in flight
	res, err := h.orch.Steer(human(context.Background()), ap.TaskID, "nota", true)
	if err != nil || res.ApplyNowStatus != "not_mid_call" {
		t.Fatalf("steer = %+v, %v", res, err)
	}
	_, _ = h.appr.Decide(context.Background(), ap.ID, "approve", "")
	h.waitFor("done", func() bool { return h.request(id).Status == domain.RequestDone })
}

func TestReassignValidation(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	var ranBy sync.Map
	rt := &fakeRuntime{plan: func(application.PlanRequest) (application.PlanResponse, error) {
		return application.PlanResponse{Tasks: []application.PlannedTask{
			{Key: "a", Title: "Primera", AgentID: "sales"},
			{Key: "b", Title: "Segunda", AgentID: "sales", DependsOn: []string{"a"}},
		}}, nil
	}, runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		ranBy.Store(in.Task.Title, in.Agent.ID)
		if in.Task.Title == "Primera" {
			<-block
		}
		return okResult("ok"), nil
	}}
	h := newV2(t, rt, nil)
	id, _ := h.orch.Submit(context.Background(), "reasignar")
	first, second := h.taskID(id, "Primera"), h.taskID(id, "Segunda")
	h.waitFor("first running", func() bool { return h.tasks(id)["Primera"].Status == domain.TaskRunning })
	ctx := human(context.Background())
	if _, err := h.orch.Reassign(ctx, first, "hr"); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("running task reassigned: %v", err)
	}
	for name, to := range map[string]string{"same agent": "sales", "unknown": "nadie", "auditor": "internal_auditor"} {
		if _, err := h.orch.Reassign(ctx, second, to); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	iv, _ := h.orch.Intervention(ctx, second)
	if !iv.CanReassign || len(iv.Candidates) == 0 {
		t.Fatalf("intervention = %+v", iv)
	}
	for _, c := range iv.Candidates {
		if c.ID == "sales" || c.ID == "internal_auditor" {
			t.Errorf("invalid candidate %s", c.ID)
		}
	}
	got, err := h.orch.Reassign(ctx, second, "hr")
	if err != nil || got.AgentID != "hr" {
		t.Fatalf("reassign = %+v, %v", got, err)
	}
	if !h.hasAudit("task.reassigned") {
		t.Fatal("reassign not audited")
	}
	block <- struct{}{}
	h.waitFor("done", func() bool { return h.request(id).Status == domain.RequestDone })
	if by, _ := ranBy.Load("Segunda"); by != "hr" {
		t.Fatalf("the reassigned task ran as %v", by)
	}
}

func sprint(v any) string { return fmtSprint(v) }
