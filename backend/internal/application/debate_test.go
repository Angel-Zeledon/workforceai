package application_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// fakeDebater is the runtime capability behind the debates: it counts calls, remembers the last request and when
// it was called.
type fakeDebater struct {
	calls atomic.Int32
	mu    sync.Mutex
	last  application.DebateRequest
	at    time.Time
	resp  application.DebateResponse
	err   error
}

func (f *fakeDebater) Debate(_ context.Context, in application.DebateRequest) (application.DebateResponse, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.last, f.at = in, time.Now()
	f.mu.Unlock()
	return f.resp, f.err
}

// chatty answers with more turns than any cap, a turn from someone who is not in the debate and a secret.
func chatty(participants ...string) *fakeDebater {
	var turns []application.DebateTurnOut
	for round := 0; round < 4; round++ {
		for _, p := range participants {
			kind := domain.DebateTurnPosition
			if round > 0 {
				kind = domain.DebateTurnObjection
			}
			turns = append(turns, application.DebateTurnOut{AgentID: p, Kind: kind, Text: "  turno de " + p + "  "})
		}
	}
	turns = append([]application.DebateTurnOut{{AgentID: "intruder", Kind: domain.DebateTurnPosition, Text: "no debería estar"},
		{AgentID: participants[0], Kind: domain.DebateTurnPosition, Text: "mi clave es password=hunter2hunter2"}}, turns...)
	turns = append(turns, application.DebateTurnOut{AgentID: "assistant", Kind: domain.DebateTurnSynthesis, Text: "Vamos por fases."})
	return &fakeDebater{resp: application.DebateResponse{Options: []string{"Todo ya", "Por fases", "Esperar"}, Turns: turns,
		Synthesis: domain.DebateSynthesis{Choice: "Por fases", Rationale: "Menos riesgo.", Dissent: []string{participants[1] + ": prefiere todo ya"}},
		Usage:     &application.Usage{Model: "sim", InputTokens: 900, OutputTokens: 300, CostUSD: 0.006}, Provider: "simulation", Model: "sim"}}
}

func newDebates(h *harness, rt any) *application.Debates {
	return &application.Debates{Store: h.store, Agents: h.store, Runtime: rt, Budget: h.orch,
		Rec: &application.Recorder{OrgID: domain.DemoOrgID, Store: h.store, Pub: h.pub}, Cfg: application.DefaultConfig()}
}

func TestDebateIsBoundedStoredAndAdvisory(t *testing.T) {
	h := newHarness(t, &fakeRuntime{}, nil)
	rt := chatty("sales", "accounting", "legal")
	svc := newDebates(h, rt)
	svc.MaxTurns = 5
	ctx := application.WithActor(context.Background(), "ana")
	approvalsBefore, _ := h.store.ListApprovals(ctx, domain.DemoOrgID, "")

	d, err := svc.Run(ctx, application.DebateInput{Topic: "¿Lanzamos el plan 2027?", AgentIDs: []string{"sales", "accounting", "legal"}, Locale: "es"})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Turns) != 5 || d.MaxTurns != 5 || rt.last.MaxTurns != 5 {
		t.Fatalf("turns = %d (cap %d, sent %d)", len(d.Turns), d.MaxTurns, rt.last.MaxTurns)
	}
	last := d.Turns[len(d.Turns)-1]
	if last.AgentID != "assistant" || last.Kind != domain.DebateTurnSynthesis || last.Text != "Vamos por fases." {
		t.Fatalf("last turn = %+v", last)
	}
	for i, tr := range d.Turns {
		if tr.Seq != i+1 || tr.AgentID == "intruder" || strings.Contains(tr.Text, "hunter2") || strings.HasPrefix(tr.Text, " ") {
			t.Fatalf("turn %d = %+v", i, tr)
		}
	}
	if !d.Advisory || !d.Untrusted || d.Status != domain.DebateDone || d.Trigger != domain.DebateTriggerUser || d.CreatedBy != "ana" ||
		d.Synthesis.Choice != "Por fases" || len(d.Synthesis.Dissent) != 1 || len(d.Options) != 3 || d.CostUSD != 0.006 {
		t.Fatalf("debate = %+v", d)
	}
	if rt.last.MaxCostUSD <= 0 || len(rt.last.Participants) != 3 || rt.last.Moderator.ID != "assistant" {
		t.Fatalf("request = %+v", rt.last)
	}
	// Stored, listed, and nothing was approved or created on the approvals side.
	got, err := svc.Get(ctx, d.ID)
	if err != nil || len(got.Turns) != 5 || !got.Untrusted {
		t.Fatalf("get = %+v %v", got, err)
	}
	if ls, _ := svc.List(ctx, "", 0); len(ls) != 1 {
		t.Fatalf("list = %d", len(ls))
	}
	approvalsAfter, _ := h.store.ListApprovals(ctx, domain.DemoOrgID, "")
	if len(approvalsAfter) != len(approvalsBefore) {
		t.Fatal("a debate created or changed approvals")
	}
	if h.pub.count(application.EvDebateStarted) != 1 || h.pub.count(application.EvDebateCompleted) != 1 || auditCount(t, h, "debate.completed") != 1 {
		t.Fatal("missing debate events or audit")
	}
	// Charged once, in the ledger, within the spend caps.
	usage, _ := h.store.ListUsage(ctx, domain.DemoOrgID)
	if len(usage) != 1 || usage[0].AgentID != "assistant" {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestDebatePicksRelevantParticipantsAndRefusesBadInput(t *testing.T) {
	h := newHarness(t, &fakeRuntime{}, nil)
	rt := chatty("sales", "accounting")
	svc := newDebates(h, rt)
	ctx := application.WithActor(context.Background(), "ana")

	for name, in := range map[string]application.DebateInput{
		"empty topic":      {Topic: "  "},
		"too many options": {Topic: "x", Options: []string{"a", "b", "c", "d", "e"}},
		"unknown agent":    {Topic: "x", AgentIDs: []string{"sales", "ghost"}},
		"one participant":  {Topic: "x", AgentIDs: []string{"sales"}},
		"five":             {Topic: "x", AgentIDs: []string{"sales", "legal", "hr", "analyst", "operations"}},
		"assistant only":   {Topic: "x", AgentIDs: []string{"assistant", "sales"}},
	} {
		if _, err := svc.Run(ctx, in); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// Agents never start a debate on their own.
	if _, err := svc.Run(application.WithActor(context.Background(), "agent:sales"), application.DebateInput{Topic: "x"}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("agent actor = %v", err)
	}
	if rt.calls.Load() != 0 {
		t.Fatal("the runtime was called for a refused debate")
	}
	// Nobody named: the agents whose profile matches the topic, never the assistant, 2..4 of them.
	if _, err := svc.Run(ctx, application.DebateInput{Topic: "Contrato y cláusulas legales del margen de costos"}); err != nil {
		t.Fatal(err)
	}
	ps := rt.last.Participants
	if len(ps) < 2 || len(ps) > 4 {
		t.Fatalf("participants = %+v", ps)
	}
	for _, p := range ps {
		if p.ID == "assistant" {
			t.Fatal("the assistant debated")
		}
	}
}

// pausing pauses one agent and lets the rest through (the kill switch variant blocks everyone).
type pausing struct {
	stubBudget
	paused string
	kill   bool
}

func (p pausing) AdmitReview(_ context.Context, agent string) application.GuardVerdict {
	if p.kill {
		return application.GuardVerdict{Code: "kill_switch_active"}
	}
	if agent == p.paused {
		return application.GuardVerdict{Code: "agent_paused"}
	}
	return application.GuardVerdict{Allowed: true}
}

func TestDebateDegradesWithoutBlockingAndRespectsCaps(t *testing.T) {
	h := newHarness(t, &fakeRuntime{}, nil)
	ctx := application.WithActor(context.Background(), "ana")
	in := application.DebateInput{Topic: "Precio 2027", AgentIDs: []string{"sales", "accounting", "legal"}}
	code := func(err error) string {
		var de *application.DebateError
		if errors.As(err, &de) {
			return de.Code
		}
		return "?"
	}

	// No runtime capability.
	if _, err := newDebates(h, &fakeRuntime{}).Run(ctx, in); code(err) != application.DebateUnavailable {
		t.Fatalf("no capability = %v", err)
	}
	// Runtime error.
	broken := &fakeDebater{err: errors.New("boom")}
	if _, err := newDebates(h, broken).Run(ctx, in); code(err) != application.DebateUnavailable {
		t.Fatalf("runtime error = %v", err)
	}
	// Kill switch: blocked before any call.
	rt := chatty("sales", "accounting")
	svc := newDebates(h, rt)
	svc.Budget = pausing{kill: true}
	if _, err := svc.Run(ctx, in); code(err) != application.DebateBlocked || rt.calls.Load() != 0 {
		t.Fatalf("kill switch = %v (%d calls)", err, rt.calls.Load())
	}
	// A spending cap (agent or organization).
	svc.Budget = stubBudget{verdict: application.GuardVerdict{Allowed: true}, ex: &application.Exceeded{Scope: domain.ScopeOrg}}
	if _, err := svc.Run(ctx, in); code(err) != application.DebateOverBudget || rt.calls.Load() != 0 {
		t.Fatalf("org cap = %v", err)
	}
	// The per-debate cost cap: the worst case does not fit, nothing is reserved or called.
	svc.Budget = h.orch
	svc.MaxCostUSD = 0.0001
	if _, err := svc.Run(ctx, in); code(err) != application.DebateOverBudget || rt.calls.Load() != 0 {
		t.Fatalf("debate cap = %v", err)
	}
	// A paused agent sits it out; the other two still debate.
	svc.MaxCostUSD = 0
	svc.Budget = pausing{stubBudget: stubBudget{verdict: application.GuardVerdict{Allowed: true}}, paused: "legal"}
	d, err := svc.Run(ctx, in)
	if err != nil || len(d.Participants) != 2 || len(rt.last.Participants) != 2 {
		t.Fatalf("paused = %+v %v", d.Participants, err)
	}
	// Two paused out of three: fewer than two left, blocked.
	svc.Budget = pausing{paused: "legal"}
	if _, err := svc.Run(ctx, application.DebateInput{Topic: "x", AgentIDs: []string{"sales", "legal"}}); code(err) != application.DebateBlocked {
		t.Fatalf("one left = %v", err)
	}
	// Failed attempts are kept (with their code) and announced so the office walks everyone back.
	all, _ := svc.List(ctx, "", 50)
	failed := 0
	for _, d := range all {
		if d.Status == domain.DebateFailed {
			failed++
			if d.FailCode == "" || len(d.Turns) != 0 {
				t.Fatalf("failed row = %+v", d)
			}
		}
	}
	if failed < 5 || h.pub.count(application.EvDebateFailed) < 5 || auditCount(t, h, "debate.failed") < 5 {
		t.Fatalf("failed rows = %d, events = %d", failed, h.pub.count(application.EvDebateFailed))
	}
}

func TestDebateMarker(t *testing.T) {
	for _, c := range []struct {
		text string
		ok   bool
		ids  string
	}{{"[[debate]] precios", true, ""}, {"[[lang:es]] [[DEBATE:sales, legal]] x", true, "sales|legal"}, {"debate sin marca", false, ""}, {"[[debatir]]", false, ""}} {
		ok, ids := application.DebateMarker(c.text)
		if ok != c.ok || strings.Join(ids, "|") != c.ids {
			t.Errorf("%q = %v %v", c.text, ok, ids)
		}
	}
	if got := application.WithDebateMarker("[[debate]] x"); got != "[[debate]] x" {
		t.Errorf("marker added twice: %q", got)
	}
}

// decisionRuntime plans three tasks; flag marks the second as the decision.
func decisionRuntime(flag bool, started *atomic.Int64, seen *sync.Map) *fakeRuntime {
	return &fakeRuntime{
		plan: func(application.PlanRequest) (application.PlanResponse, error) {
			return application.PlanResponse{Objectives: []string{"o"}, Tasks: []application.PlannedTask{
				{Key: "t1", Title: "Analizar", Description: "d", AgentID: "analyst"},
				{Key: "t2", Title: "Elegir proveedor", Description: "A o B", AgentID: "operations", DependsOn: []string{"t1"}, Decision: flag},
				{Key: "t3", Title: "Revisar contrato", Description: "d", AgentID: "legal", DependsOn: []string{"t2"}},
			}}, nil
		},
		runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
			started.CompareAndSwap(0, time.Now().UnixNano())
			for _, r := range in.Recalled {
				if strings.HasPrefix(r.Ref, "debate:") {
					seen.Store(in.Task.Title, r.Snippet)
				}
			}
			return okResult("hecho: " + in.Task.Title), nil
		},
	}
}

func TestPlannerDecisionTriggersOneDebateBeforeTheTasks(t *testing.T) {
	var started atomic.Int64
	var seen sync.Map
	h := newHarness(t, decisionRuntime(true, &started, &seen), nil)
	rt := chatty("operations", "analyst", "legal")
	svc := newDebates(h, rt)
	h.orch.SetDebates(svc)
	id, err := h.orch.Submit(context.Background(), "Necesitamos elegir proveedor de logística")
	if err != nil {
		t.Fatal(err)
	}
	h.waitFor("request done", func() bool { return h.request(id).Status == domain.RequestDone })
	ds, _ := svc.List(context.Background(), id, 10)
	if len(ds) != 1 || rt.calls.Load() != 1 {
		t.Fatalf("debates = %d, calls = %d", len(ds), rt.calls.Load())
	}
	d := ds[0]
	if d.Trigger != domain.DebateTriggerPlanner || d.Topic != "Elegir proveedor" || d.TaskID == "" || d.Participants[0] != "operations" {
		t.Fatalf("debate = %+v", d)
	}
	rt.mu.Lock()
	debatedAt := rt.at
	rt.mu.Unlock()
	if started.Load() == 0 || debatedAt.UnixNano() > started.Load() {
		t.Fatal("the debate did not happen before the tasks started")
	}
	// Only the deciding task gets the debate, as untrusted recalled data labelled advisory.
	note, ok := seen.Load("Elegir proveedor")
	if !ok || !strings.Contains(note.(string), "Por fases") {
		t.Fatalf("decision task recall = %v", note)
	}
	if _, other := seen.Load("Analizar"); other {
		t.Fatal("a task that does not decide got the debate")
	}
}

func TestNoDebateWithoutFlagOrMarkerAndMarkerForcesOne(t *testing.T) {
	var started atomic.Int64
	var seen sync.Map
	h := newHarness(t, decisionRuntime(false, &started, &seen), nil)
	rt := chatty("analyst", "operations")
	svc := newDebates(h, rt)
	h.orch.SetDebates(svc)
	id, _ := h.orch.Submit(context.Background(), "Elegir proveedor")
	h.waitFor("request done", func() bool { return h.request(id).Status == domain.RequestDone })
	if rt.calls.Load() != 0 {
		t.Fatal("debated a plan without a flagged decision")
	}
	// The person asks for it: [[debate]] (the "/debatir" command, POST /requests {"debate": true}).
	id2, _ := h.orch.Submit(application.WithActor(context.Background(), "ana"), application.WithDebateMarker("Elegir proveedor"))
	h.waitFor("request done", func() bool { return h.request(id2).Status == domain.RequestDone })
	ds, _ := svc.List(context.Background(), id2, 10)
	if len(ds) != 1 || ds[0].Trigger != domain.DebateTriggerUser || ds[0].CreatedBy != "ana" {
		t.Fatalf("marked request debates = %+v", ds)
	}
	// A debate that cannot happen never fails the request.
	svc.Runtime = &fakeDebater{err: errors.New("down")}
	id3, _ := h.orch.Submit(context.Background(), application.WithDebateMarker("Elegir proveedor"))
	h.waitFor("request done", func() bool { return h.request(id3).Status == domain.RequestDone })
}
