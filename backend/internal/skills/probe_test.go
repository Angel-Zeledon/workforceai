package skills

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// fakeProbeRuntime answers RunTask from a script and remembers every request it got.
type fakeProbeRuntime struct {
	mu    sync.Mutex
	reqs  []application.RunTaskRequest
	reply func(round int, in application.RunTaskRequest) (application.RunTaskResponse, error)
}

func (f *fakeProbeRuntime) RunTask(_ context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, in)
	n := len(f.reqs) - 1
	f.mu.Unlock()
	return f.reply(n, in)
}

func answer(text string, cost float64) application.RunTaskResponse {
	return application.RunTaskResponse{Output: domain.StructuredOutput{Summary: text},
		Usage: application.Usage{Model: "sim", InputTokens: 100, OutputTokens: 40, CostUSD: cost}}
}

// loadsThenAnswers asks for skill.load of name on the first call and answers once the text arrived.
func loadsThenAnswers(name string) func(int, application.RunTaskRequest) (application.RunTaskResponse, error) {
	return func(_ int, in application.RunTaskRequest) (application.RunTaskResponse, error) {
		if len(in.LoadedSkills) == 0 {
			r := answer("voy a cargar la habilidad", 0.001)
			r.ToolRequests = []application.ToolRequest{{Tool: "skill", Action: "load", Args: map[string]any{"name": name}, Risk: "low"}}
			return r, nil
		}
		return answer("respuesta siguiendo: "+in.LoadedSkills[0].Text, 0.002), nil
	}
}

// fakeProbeBudget counts what a run does to the budget.
type fakeProbeBudget struct {
	mu        sync.Mutex
	reserved  int
	recorded  []float64
	released  int
	exceeded  *application.Exceeded
	admit     application.GuardVerdict
	estimate  float64
	lastAgent string
}

func newBudget() *fakeProbeBudget {
	return &fakeProbeBudget{admit: application.GuardVerdict{Allowed: true}, estimate: 0.05}
}

func (b *fakeProbeBudget) AdmitReview(context.Context, string) application.GuardVerdict {
	return b.admit
}
func (b *fakeProbeBudget) EstimateAgentCall(any) float64 { return b.estimate }
func (b *fakeProbeBudget) ReserveAgentCall(_ context.Context, agent string, _ any) (*application.Reservation, *application.Exceeded) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.exceeded != nil {
		return nil, b.exceeded
	}
	b.reserved++
	b.lastAgent = agent
	return nil, nil
}
func (b *fakeProbeBudget) RecordAgentCall(_ context.Context, _ string, u application.Usage, _ *application.Reservation) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recorded = append(b.recorded, u.CostUSD)
}
func (b *fakeProbeBudget) NoteProviderNoBalance(context.Context, string, error) bool { return false }

func (e *env) tester(rt TestRuntime, b TestBudget) *Tester {
	return NewTester(TesterConfig{Svc: e.svc, Store: NewMemTestStore(), Runtime: rt, Budget: b,
		Agent: func(_ context.Context, _, id string) (domain.Agent, error) {
			switch id {
			case "sales", "accounting", "hr", "a-elena", "a-valeria":
				return domain.Agent{ID: id, Name: id, Role: "sales", Persona: "p"}, nil
			}
			return domain.Agent{}, domain.ErrNotFound
		},
		Agents: func(context.Context, string) ([]domain.Agent, error) {
			return []domain.Agent{{ID: "sales"}, {ID: "accounting"}, {ID: "hr"}}, nil
		}})
}

func (e *env) draftFor(t *testing.T, name, agent string) Detail {
	t.Helper()
	d, err := e.svc.Create(e.ctx, member, in(name))
	if err != nil {
		t.Fatal(err)
	}
	if agent != "" {
		if err := e.svc.Assign(e.ctx, member, agent, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func saveCase(t *testing.T, tt *Tester, e *env, skill string) TestCase {
	t.Helper()
	c, err := tt.SaveCase(e.ctx, member, skill, "", CaseInput{Prompt: "Prepara el informe semanal de ventas", Expect: "Menciona totales"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A draft is tested without being activated: the version under test is the one the agent loads, the run says which
// version it was, and the skill stays a draft with no active version.
func TestProbeDraftIsTestedWithoutActivating(t *testing.T) {
	e := newEnv(t, false)
	d := e.draftFor(t, "weekly-report", "sales")
	rt := &fakeProbeRuntime{reply: loadsThenAnswers("weekly-report")}
	bud := newBudget()
	tt := e.tester(rt, bud)
	c := saveCase(t, tt, e, d.ID)

	runs, err := tt.Run(e.ctx, member, d.ID, RunInput{CaseID: c.ID, AgentID: "sales"})
	if err != nil || len(runs) != 1 {
		t.Fatalf("%v %v", runs, err)
	}
	r := runs[0]
	if r.Status != RunDone || r.Version != 1 || !r.UsedSkill || r.Active {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.Output, "Do the thing") {
		t.Fatalf("the draft's text must have reached the agent: %q", r.Output)
	}
	if r.CostUSD < 0.0029 || r.CostUSD > 0.0031 || r.InputTokens != 200 {
		t.Fatalf("real cost and tokens are the sum of the calls: %+v", r)
	}
	sk, err := e.svc.Get(e.ctx, admin, d.ID, 0)
	if err != nil || sk.Status != StatusDraft || sk.ActiveVersion != 0 {
		t.Fatalf("testing must not activate: %+v %v", sk.Skill, err)
	}
	// the draft never became servable: a real task would not load it
	if _, err := e.svc.Load(e.ctx, org, LoadCall{AgentID: "sales", Name: "weekly-report"}); err == nil {
		t.Fatal("a draft must still not be served to agents")
	}
	// first call carried only name + description of the skill under test, second one the text
	if len(rt.reqs) != 2 || len(rt.reqs[0].LoadedSkills) != 0 || len(rt.reqs[1].LoadedSkills) != 1 || len(rt.reqs[0].Skills) != 1 {
		t.Fatalf("calls: %+v", rt.reqs)
	}
	if h, err := tt.Runs(e.ctx, admin, d.ID, c.ID, 0); err != nil || len(h) != 1 || h[0].ID != r.ID {
		t.Fatalf("history: %v %v", h, err)
	}
	ov, err := tt.Overview(e.ctx, admin, d.ID)
	if err != nil || len(ov.LastByVersion) != 1 || ov.LastByVersion[0].Version != 1 {
		t.Fatalf("overview: %+v %v", ov, err)
	}
}

// No effects by construction: the agent is told it has no tools, and whatever tool it asks for is dropped and
// recorded by name only. Another skill is not served either.
func TestProbeRejectsEveryToolThatIsNotTheSkillLoad(t *testing.T) {
	e := newEnv(t, false)
	d := e.draftFor(t, "weekly-report", "sales")
	rt := &fakeProbeRuntime{reply: func(_ int, in application.RunTaskRequest) (application.RunTaskResponse, error) {
		r := answer("listo", 0.001)
		r.ToolRequests = []application.ToolRequest{
			{Tool: "email", Action: "send", Args: map[string]any{"to": "cliente@example.com", "body": "secreto"}, Risk: "high"},
			{Tool: "fs", Action: "write", Args: map[string]any{"path": "/Shared/x.md"}, Risk: "medium"},
			{Tool: "skill", Action: "load", Args: map[string]any{"name": "other-skill"}, Risk: "low"},
			{Tool: "code", Action: "run", Args: map[string]any{"cmd": "rm -rf /"}, Risk: "high"},
		}
		return r, nil
	}}
	tt := e.tester(rt, newBudget())
	c := saveCase(t, tt, e, d.ID)
	runs, err := tt.Run(e.ctx, member, d.ID, RunInput{CaseID: c.ID, AgentID: "sales"})
	if err != nil {
		t.Fatal(err)
	}
	r := runs[0]
	want := []string{"email.send", "fs.write", "skill.load", "code.run"}
	if strings.Join(r.Blocked, ",") != strings.Join(want, ",") {
		t.Fatalf("blocked = %v", r.Blocked)
	}
	if r.UsedSkill || len(rt.reqs) != 1 {
		t.Fatalf("nothing was served and there was no second call: %+v / %d calls", r, len(rt.reqs))
	}
	if len(rt.reqs[0].Agent.Tools) != 0 {
		t.Fatalf("the agent must be told it has no tools: %v", rt.reqs[0].Agent.Tools)
	}
	if strings.Contains(r.Output+strings.Join(r.Blocked, " "), "cliente@example.com") {
		t.Fatal("arguments of a refused tool must not be kept")
	}
	// a skill.load in the LAST round is not served either
	rt2 := &fakeProbeRuntime{reply: func(_ int, in application.RunTaskRequest) (application.RunTaskResponse, error) {
		r := answer("otra vez", 0)
		r.ToolRequests = []application.ToolRequest{{Tool: "skill", Action: "load", Args: map[string]any{"name": "weekly-report"}}}
		return r, nil
	}}
	tt2 := e.tester(rt2, newBudget())
	c2 := saveCase(t, tt2, e, d.ID)
	runs, _ = tt2.Run(e.ctx, member, d.ID, RunInput{CaseID: c2.ID, AgentID: "sales"})
	if len(rt2.reqs) != MaxTestRounds || len(runs[0].Blocked) != 1 {
		t.Fatalf("the run is bounded: %d calls, %+v", len(rt2.reqs), runs[0].Blocked)
	}
}

// The budget: the estimate is known before, every call is reserved and recorded in the ledger with the agent, and a
// cap that does not allow the call makes a failed run without calling the model.
func TestProbeCountsAgainstTheBudget(t *testing.T) {
	e := newEnv(t, false)
	d := e.draftFor(t, "weekly-report", "sales")
	rt := &fakeProbeRuntime{reply: loadsThenAnswers("weekly-report")}
	bud := newBudget()
	tt := e.tester(rt, bud)
	c := saveCase(t, tt, e, d.ID)

	est, err := tt.Estimate(e.ctx, member, d.ID, RunInput{CaseID: c.ID, AgentID: "sales"})
	if err != nil || est.Runs != 1 || est.EstimateUSD < 0.0999 || est.EstimateUSD > 0.1001 {
		t.Fatalf("estimate = %+v %v (two worst-case calls of 0.05)", est, err)
	}
	if len(rt.reqs) != 0 {
		t.Fatal("estimating must not call the model")
	}
	runs, _ := tt.Run(e.ctx, member, d.ID, RunInput{CaseID: c.ID, AgentID: "sales"})
	if bud.reserved != 2 || len(bud.recorded) != 2 || bud.lastAgent != "sales" {
		t.Fatalf("reserved %d recorded %v", bud.reserved, bud.recorded)
	}
	if runs[0].EstimateUSD < 0.0999 || runs[0].CostUSD <= 0 {
		t.Fatalf("a run keeps the estimate and the real cost: %+v", runs[0])
	}

	bud.exceeded = &application.Exceeded{Scope: domain.ScopeOrg}
	n := len(rt.reqs)
	runs, err = tt.Run(e.ctx, member, d.ID, RunInput{CaseID: c.ID, AgentID: "sales"})
	if err != nil || runs[0].Status != RunFailed || runs[0].FailCode != "budget_exceeded:org" || len(rt.reqs) != n {
		t.Fatalf("over the cap: %+v calls %d->%d", runs[0], n, len(rt.reqs))
	}
	bud.exceeded = nil
	bud.admit = application.GuardVerdict{Code: "kill_switch_active"}
	runs, _ = tt.Run(e.ctx, member, d.ID, RunInput{CaseID: c.ID, AgentID: "sales"})
	if runs[0].Status != RunFailed || runs[0].FailCode != "blocked:kill_switch_active" || len(rt.reqs) != n {
		t.Fatalf("kill switch: %+v", runs[0])
	}
	// a failing runtime is a failed run and still releases the reservation
	bud.admit = application.GuardVerdict{Allowed: true}
	rt.reply = func(int, application.RunTaskRequest) (application.RunTaskResponse, error) {
		return application.RunTaskResponse{}, errors.New("boom")
	}
	before := len(bud.recorded)
	runs, _ = tt.Run(e.ctx, member, d.ID, RunInput{CaseID: c.ID, AgentID: "sales"})
	if runs[0].Status != RunFailed || runs[0].FailCode != "runtime_unavailable" || len(bud.recorded) != before+1 {
		t.Fatalf("runtime error: %+v", runs[0])
	}
}

// Side by side: the version under review and the active one answer the same case; they share a pair id.
func TestProbeComparesWithTheActiveVersion(t *testing.T) {
	e := newEnv(t, false)
	d := e.draftFor(t, "weekly-report", "sales")
	if _, err := e.svc.Submit(e.ctx, member, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Review(e.ctx, admin, d.ID, true, 1, ""); err != nil {
		t.Fatal(err)
	}
	up := in("weekly-report")
	up.Instructions = "# weekly-report\n\n1. Do the NEW thing.\n"
	if _, _, err := e.svc.Update(e.ctx, member, d.ID, up); err != nil {
		t.Fatal(err)
	}
	sk, _ := e.svc.Get(e.ctx, admin, d.ID, 0)
	if sk.ActiveVersion != 1 || sk.CurrentVersion != 2 {
		t.Fatalf("setup: %+v", sk.Skill)
	}
	rt := &fakeProbeRuntime{reply: loadsThenAnswers("weekly-report")}
	tt := e.tester(rt, newBudget())
	c := saveCase(t, tt, e, d.ID)
	est, _ := tt.Estimate(e.ctx, member, d.ID, RunInput{CaseID: c.ID, AgentID: "sales", Compare: true})
	if est.Runs != 2 || len(est.Versions) != 2 || est.Versions[0] != 2 || est.Versions[1] != 1 {
		t.Fatalf("estimate of a comparison: %+v", est)
	}
	runs, err := tt.Run(e.ctx, member, d.ID, RunInput{CaseID: c.ID, AgentID: "sales", Compare: true})
	if err != nil || len(runs) != 2 {
		t.Fatalf("%v %v", runs, err)
	}
	if runs[0].Version != 2 || runs[0].Active || runs[1].Version != 1 || !runs[1].Active || runs[0].PairID == "" || runs[0].PairID != runs[1].PairID {
		t.Fatalf("pair: %+v %+v", runs[0], runs[1])
	}
	if !strings.Contains(runs[0].Output, "NEW thing") || strings.Contains(runs[1].Output, "NEW thing") {
		t.Fatalf("each run must follow its own version:\n%s\n---\n%s", runs[0].Output, runs[1].Output)
	}
	// without an active version, or when the tested one IS the active one, there is nothing to compare with
	runs, _ = tt.Run(e.ctx, member, d.ID, RunInput{CaseID: c.ID, AgentID: "sales", Version: 1, Compare: true})
	if len(runs) != 1 || runs[0].PairID != "" {
		t.Fatalf("v1 is the active one: %+v", runs)
	}
}

func TestProbeLimitsAndInputs(t *testing.T) {
	e := newEnv(t, false)
	d := e.draftFor(t, "weekly-report", "sales")
	tt := e.tester(&fakeProbeRuntime{reply: loadsThenAnswers("weekly-report")}, newBudget())
	for i := 0; i < MaxTestCases; i++ {
		saveCase(t, tt, e, d.ID)
	}
	if _, err := tt.SaveCase(e.ctx, member, d.ID, "", CaseInput{Prompt: "uno más"}); CodeOf(err) != "too_many_cases" {
		t.Fatalf("cap = %v", err)
	}
	for code, in := range map[string]CaseInput{
		"case_prompt_required": {Prompt: "   "},
		"case_prompt_too_long": {Prompt: strings.Repeat("a", MaxCasePrompt+1)},
		"case_expect_too_long": {Prompt: "x", Expect: strings.Repeat("a", MaxCaseExpect+1)},
		"case_title_too_long":  {Prompt: "x", Title: strings.Repeat("a", MaxCaseTitle+1)},
	} {
		cs, _ := tt.Cases(e.ctx, member, d.ID)
		if _, err := tt.SaveCase(e.ctx, member, d.ID, cs[0].ID, in); CodeOf(err) != code {
			t.Errorf("%s: %v", code, err)
		}
	}
	cs, _ := tt.Cases(e.ctx, member, d.ID)
	if err := tt.DeleteCase(e.ctx, member, d.ID, cs[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tt.SaveCase(e.ctx, member, d.ID, "", CaseInput{Prompt: "ahora sí"}); err != nil {
		t.Fatalf("a slot was freed: %v", err)
	}
	// a secret in a case does not stay in it
	c, _ := tt.SaveCase(e.ctx, member, d.ID, cs[1].ID, CaseInput{Prompt: "usa la clave sk-proj-abcdefghijklmnopqrstuvwxyz0123456789 para entrar"})
	if strings.Contains(c.Prompt, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("secret kept: %q", c.Prompt)
	}
	// an agent that cannot use the skill, an unknown case, an unknown version
	if _, err := tt.Run(e.ctx, member, d.ID, RunInput{CaseID: cs[1].ID, AgentID: "hr"}); CodeOf(err) != "agent_not_eligible" {
		t.Fatalf("not assigned = %v", err)
	}
	if _, err := tt.Run(e.ctx, member, d.ID, RunInput{CaseID: "nope", AgentID: "sales"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("case = %v", err)
	}
	if _, err := tt.Run(e.ctx, member, d.ID, RunInput{CaseID: cs[1].ID, AgentID: "sales", Version: 9}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("version = %v", err)
	}
	cand, err := tt.Candidates(e.ctx, member, d.ID)
	if err != nil || len(cand) != 1 || cand[0].ID != "sales" {
		t.Fatalf("candidates = %v %v", cand, err)
	}
}

// An agent never starts a test, and one organization never sees another's skills, cases or runs.
func TestProbeHumansOnlyAndOrganizationIsolation(t *testing.T) {
	e := newEnv(t, false)
	d := e.draftFor(t, "weekly-report", "sales")
	rt := &fakeProbeRuntime{reply: loadsThenAnswers("weekly-report")}
	tt := e.tester(rt, newBudget())
	c := saveCase(t, tt, e, d.ID)
	for _, a := range []Actor{{ID: "sales", System: true}, {System: true}, {}} {
		if _, err := tt.Run(e.ctx, a, d.ID, RunInput{CaseID: c.ID, AgentID: "sales"}); CodeOf(err) != "humans_only" {
			t.Fatalf("actor %+v: %v", a, err)
		}
		if _, err := tt.Estimate(e.ctx, a, d.ID, RunInput{CaseID: c.ID, AgentID: "sales"}); CodeOf(err) != "humans_only" {
			t.Fatalf("estimate %+v: %v", a, err)
		}
	}
	if _, err := tt.SaveCase(e.ctx, Actor{System: true}, d.ID, "", CaseInput{Prompt: "x"}); CodeOf(err) != "humans_only" {
		t.Fatalf("save = %v", err)
	}
	if len(rt.reqs) != 0 {
		t.Fatal("no model call for a refused actor")
	}

	other := application.WithOrg(context.Background(), "org2")
	if _, err := tt.Run(other, admin, d.ID, RunInput{CaseID: c.ID, AgentID: "sales"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("run from another org = %v", err)
	}
	if _, err := tt.Cases(other, admin, d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cases from another org = %v", err)
	}
	if _, err := tt.Runs(other, admin, d.ID, "", 0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("runs from another org = %v", err)
	}
	if _, err := tt.Overview(other, admin, d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("overview from another org = %v", err)
	}
	// the store itself is scoped: the same case id in another org is not there
	if _, err := tt.cfg.Store.GetCase(other, "org2", d.ID, c.ID); err == nil {
		t.Fatal("store leaked a case across organizations")
	}
}

// A skill of a private department is sealed: whoever cannot see the department cannot list, save, estimate or run
// its cases, not even an administrator who is outside it.
func TestProbeOfPrivateDepartmentIsSealed(t *testing.T) {
	oe := newOfficeEnv(t)
	e := oe.env
	d, err := e.svc.Create(e.ctx, ana, officeIn("contract-check", "d-legal"))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Assign(e.ctx, ana, "a-elena", d.ID); err != nil {
		t.Fatal(err)
	}
	rt := &fakeProbeRuntime{reply: loadsThenAnswers("contract-check")}
	tt := e.tester(rt, newBudget())
	tt.cfg.Agents = func(context.Context, string) ([]domain.Agent, error) {
		return []domain.Agent{{ID: "a-elena"}, {ID: "a-valeria"}}, nil
	}
	c, err := tt.SaveCase(e.ctx, ana, d.ID, "", CaseInput{Prompt: "Revisa este contrato"})
	if err != nil {
		t.Fatal(err)
	}
	if runs, err := tt.Run(e.ctx, ana, d.ID, RunInput{CaseID: c.ID, AgentID: "a-elena"}); err != nil || runs[0].Status != RunDone {
		t.Fatalf("the department's lead can test: %v %v", runs, err)
	}
	// a plain member of the department can read it but only the lead or an administrator writes cases
	if _, err := tt.Cases(e.ctx, luis, d.ID); err != nil {
		t.Fatalf("member reads: %v", err)
	}
	if _, err := tt.SaveCase(e.ctx, luis, d.ID, "", CaseInput{Prompt: "x"}); CodeOf(err) != "office_forbidden" {
		t.Fatalf("member writes = %v", err)
	}
	calls := len(rt.reqs)
	for who, a := range map[string]Actor{"marta": marta, "owner outside": boss} {
		if _, err := tt.Cases(e.ctx, a, d.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s cases = %v", who, err)
		}
		if _, err := tt.Overview(e.ctx, a, d.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s overview = %v", who, err)
		}
		if _, err := tt.Runs(e.ctx, a, d.ID, "", 0); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s runs = %v", who, err)
		}
		if _, err := tt.Candidates(e.ctx, a, d.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s candidates = %v", who, err)
		}
		if _, err := tt.Estimate(e.ctx, a, d.ID, RunInput{CaseID: c.ID, AgentID: "a-elena"}); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s estimate = %v", who, err)
		}
		if _, err := tt.Run(e.ctx, a, d.ID, RunInput{CaseID: c.ID, AgentID: "a-elena"}); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s run = %v", who, err)
		}
		if _, err := tt.SaveCase(e.ctx, a, d.ID, "", CaseInput{Prompt: "x"}); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s save = %v", who, err)
		}
	}
	if len(rt.reqs) != calls {
		t.Fatal("a sealed skill must never reach the model for an outsider")
	}
}
