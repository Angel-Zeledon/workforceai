package agenthistory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
)

// world is a fake configuration of agents: the source reads it, the executor writes it.
type world struct {
	mu       sync.Mutex
	cfg      map[string]*Config // org|agent
	skills   map[string]bool    // usable skills
	calls    []string
	forbid   map[string]bool // ops refused with ErrForbidden
	audits   []domain.AuditLog
	lowered  []string
	ceiling  float64
	noAgents bool
}

func newWorld() *world {
	return &world{cfg: map[string]*Config{}, skills: map[string]bool{}, forbid: map[string]bool{}}
}

func (w *world) put(org, id string, c Config) {
	w.mu.Lock()
	defer w.mu.Unlock()
	cc := c
	w.cfg[key(org, id)] = &cc
}

func (w *world) edit(org, id string, fn func(c *Config)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fn(w.cfg[key(org, id)])
}

func (w *world) source(_ context.Context, org, id string, c *Config) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	x, ok := w.cfg[key(org, id)]
	if !ok {
		return fmt.Errorf("%w: agent %s", domain.ErrNotFound, id)
	}
	*c = *x
	c.Tags = slices.Clone(x.Tags)
	c.Skills = slices.Clone(x.Skills)
	c.Tools = slices.Clone(x.Tools)
	if x.Permissions != nil {
		p := *x.Permissions
		p.Exceptions = slices.Clone(x.Permissions.Exceptions)
		p.FloorLifted = slices.Clone(x.Permissions.FloorLifted)
		c.Permissions = &p
	}
	return nil
}

func (w *world) svc() *Service {
	return &Service{Store: NewMemStore(), Sources: []Source{w.source},
		Agents: func(_ context.Context, org string) ([]string, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			out := []string{}
			for k := range w.cfg {
				if o, id, _ := cut(k); o == org {
					out = append(out, id)
				}
			}
			slices.Sort(out)
			return out, nil
		},
		Audit: func(_ context.Context, _ string, a domain.AuditLog) { w.audits = append(w.audits, a) }}
}

func cut(k string) (string, string, bool) {
	for i := range k {
		if k[i] == '|' {
			return k[:i], k[i+1:], true
		}
	}
	return k, "", false
}

// executor for org "o".
type exec struct{ w *world }

func (e exec) do(op, id string, fn func(c *Config)) error {
	e.w.mu.Lock()
	e.w.calls = append(e.w.calls, op)
	refused := e.w.forbid[op]
	e.w.mu.Unlock()
	if refused {
		return fmt.Errorf("%w: not yours", domain.ErrForbidden)
	}
	e.w.edit("o", id, fn)
	return nil
}

func (e exec) SetProfile(_ context.Context, id string, c Config) error {
	return e.do(OpSetProfile, id, func(x *Config) { x.Instructions, x.Creativity, x.Tags, x.Tone = c.Instructions, c.Creativity, c.Tags, c.Tone })
}
func (e exec) LowerAutonomy(_ context.Context, id, to string) error {
	e.w.lowered = append(e.w.lowered, to)
	return e.do(OpLowerAutonomy, id, func(x *Config) { x.Autonomy = to })
}
func (e exec) AdoptPermissions(_ context.Context, id string) error {
	return e.do(OpAdoptPermissions, id, func(x *Config) { x.Permissions = &Permissions{} })
}
func (e exec) SetException(_ context.Context, id string, ex Exception) error {
	return e.do(OpSetException, id, func(x *Config) {
		x.Permissions.Exceptions = slices.DeleteFunc(x.Permissions.Exceptions, func(y Exception) bool { return y.Category == ex.Category })
		x.Permissions.Exceptions = append(x.Permissions.Exceptions, ex)
	})
}
func (e exec) RemoveException(_ context.Context, id, cat string) error {
	return e.do(OpRemoveException, id, func(x *Config) {
		x.Permissions.Exceptions = slices.DeleteFunc(x.Permissions.Exceptions, func(y Exception) bool { return y.Category == cat })
	})
}
func (e exec) RestoreFloor(_ context.Context, id, cat string) error {
	return e.do(OpRestoreFloor, id, func(x *Config) {
		x.Permissions.FloorLifted = slices.DeleteFunc(x.Permissions.FloorLifted, func(y string) bool { return y == cat })
	})
}
func (e exec) AssignSkill(_ context.Context, id, sk string) error {
	return e.do(OpAssignSkill, id, func(x *Config) { x.Skills = append(x.Skills, SkillRef{ID: sk, Name: sk}) })
}
func (e exec) UnassignSkill(_ context.Context, id, sk string) error {
	return e.do(OpUnassignSkill, id, func(x *Config) {
		x.Skills = slices.DeleteFunc(x.Skills, func(y SkillRef) bool { return y.ID == sk })
	})
}
func (e exec) SetBudget(_ context.Context, id string, usd float64) error {
	return e.do(OpSetBudget, id, func(x *Config) { x.BudgetUSD = usd })
}
func (e exec) SetJob(_ context.Context, id string, j Job, tools []string) error {
	return e.do(OpSetJob, id, func(x *Config) { jj := j; x.Job, x.Tools = &jj, tools })
}

func base() Config {
	return Config{Name: "Valeria", Title: "Ventas", Role: "sales", Instructions: "Sé breve.", Creativity: "medium", Tone: "",
		Autonomy: "rules", Permissions: &Permissions{}, Tools: []string{"email", "crm"},
		Job: &Job{Responsibilities: []string{"Vender"}}}
}

var ctx = context.Background()

func person(name string) Cause { return Cause{Actor: "u-" + name, ActorName: name, Source: "PUT /agents/{id}/profile"} }

func TestRecordWritesOnlyWhenSomethingChanged(t *testing.T) {
	w := newWorld()
	w.put("o", "a", base())
	s := w.svc()
	v, wrote, err := s.Record(ctx, "o", "a", Cause{Actor: "system", Source: "baseline"})
	if err != nil || !wrote || v.Version != 1 || len(v.Fields) != 0 || v.ActorKind != ActorSystem {
		t.Fatalf("first record: v=%+v wrote=%v err=%v", v, wrote, err)
	}
	if _, wrote, _ := s.Record(ctx, "o", "a", person("Carla")); wrote {
		t.Fatal("nothing changed but a version was written")
	}
	w.edit("o", "a", func(c *Config) { c.Instructions = "Sé muy breve."; c.Tags = []string{"b", "a"} })
	v, wrote, _ = s.Record(ctx, "o", "a", person("Carla"))
	if !wrote || v.Version != 2 || !slices.Equal(v.Fields, []string{"instructions", "tags"}) || v.ActorName != "Carla" || v.ActorKind != ActorPerson {
		t.Fatalf("second: %+v", v)
	}
	// same tags in another order are not a change
	w.edit("o", "a", func(c *Config) { c.Tags = []string{"a", "b"} })
	if _, wrote, _ := s.Record(ctx, "o", "a", person("Carla")); wrote {
		t.Fatal("reordering a list is not a change")
	}
}

func TestDetailDiffsAgainstThePreviousVersion(t *testing.T) {
	w := newWorld()
	w.put("o", "a", base())
	s := w.svc()
	s.Record(ctx, "o", "a", Cause{Source: "baseline"})
	w.edit("o", "a", func(c *Config) { c.Instructions = "Nuevo" })
	s.Record(ctx, "o", "a", person("Carla"))
	d, err := s.Detail(ctx, "o", "a", 2)
	if err != nil || d.Previous == nil || *d.Previous != 1 || len(d.Changes) != 1 || d.Changes[0].Field != "instructions" ||
		string(d.Changes[0].From) != `"Sé breve."` || string(d.Changes[0].To) != `"Nuevo"` {
		t.Fatalf("detail: %+v err=%v", d, err)
	}
	if _, err := s.Detail(ctx, "o", "a", 9); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing version: %v", err)
	}
}

func TestSecretsKeepOnlyAFingerprint(t *testing.T) {
	var a, b Config
	a.SetSecret("api_key", "sk-very-secret-1")
	b.SetSecret("api_key", "sk-very-secret-2")
	ch := Diff(a, b)
	if len(ch) != 1 || !ch[0].Secret || ch[0].From != nil || ch[0].To != nil || ch[0].Field != "secret:api_key" {
		t.Fatalf("secret diff: %+v", ch)
	}
	for _, v := range a.Secrets {
		if v == "sk-very-secret-1" || len(v) != 16 {
			t.Fatalf("the secret itself was kept: %q", v)
		}
	}
	p := PlanRestore(a, b, PlanEnv{})
	if len(p.Apply) != 0 || len(p.Skip) != 1 || p.Skip[0].Reason != ReasonSecret {
		t.Fatalf("a secret is never restored: %+v", p)
	}
}

func TestRetentionKeepsTheNewest(t *testing.T) {
	w := newWorld()
	w.put("o", "a", base())
	s := w.svc()
	s.Keep = 5
	for i := 0; i < 12; i++ {
		w.edit("o", "a", func(c *Config) { c.Instructions = fmt.Sprint("v", i) })
		if _, _, err := s.Record(ctx, "o", "a", person("Carla")); err != nil {
			t.Fatal(err)
		}
	}
	l, _ := s.List(ctx, "o", "a", 0)
	if len(l) != 5 || l[0].Version != 12 || l[4].Version != 8 {
		t.Fatalf("retention: %d versions, newest %d", len(l), l[0].Version)
	}
	if _, err := s.Store.Get(ctx, "o", "a", 7); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("dropped version still served")
	}
}

func TestListStartsWithABaselineAndIsolatesOrgs(t *testing.T) {
	w := newWorld()
	w.put("o", "a", base())
	other := base()
	other.Instructions = "secreto de otra empresa"
	w.put("x", "a", other)
	s := w.svc()
	l, err := s.List(ctx, "o", "a", 0)
	if err != nil || len(l) != 1 || l[0].Source != "baseline" {
		t.Fatalf("baseline: %+v err=%v", l, err)
	}
	if lx, _ := s.List(ctx, "x", "a", 0); len(lx) != 1 || lx[0].Config.Instructions != "secreto de otra empresa" {
		t.Fatalf("other org: %+v", lx)
	}
	if l, _ := s.List(ctx, "o", "a", 0); l[0].Config.Instructions == "secreto de otra empresa" {
		t.Fatal("history leaked between organizations")
	}
	if _, err := s.List(ctx, "o", "ghost", 0); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown agent: %v", err)
	}
}

func setup(t *testing.T, before Config, after func(c *Config)) (*world, *Service) {
	t.Helper()
	w := newWorld()
	w.put("o", "a", before)
	s := w.svc()
	if _, _, err := s.Record(ctx, "o", "a", Cause{Source: "baseline"}); err != nil {
		t.Fatal(err)
	}
	w.edit("o", "a", after)
	if _, _, err := s.Record(ctx, "o", "a", person("Carla")); err != nil {
		t.Fatal(err)
	}
	return w, s
}

func restore1(t *testing.T, w *world, s *Service) RestoreResult {
	t.Helper()
	r, err := s.Restore(ctx, "o", "a", 1, person("Ana"), exec{w}, PlanEnv{SkillUsable: func(id string) bool { return w.skills[id] }, BudgetCeiling: w.ceiling})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func skipped(r RestoreResult, field, reason string) bool {
	return slices.ContainsFunc(r.Skipped, func(s Step) bool { return s.Field == field && s.Reason == reason })
}

func TestRestoreCreatesANewVersion(t *testing.T) {
	w, s := setup(t, base(), func(c *Config) { c.Instructions = "Ayer cambié esto"; c.Tone = "formal" })
	r := restore1(t, w, s)
	if r.Version == nil || r.Version.Version != 3 || r.Version.RestoredFrom == nil || *r.Version.RestoredFrom != 1 || r.Version.Source != "restore" {
		t.Fatalf("restore result: %+v", r)
	}
	if got := r.Version.Config; got.Instructions != "Sé breve." || got.Tone != "" {
		t.Fatalf("not restored: %+v", got)
	}
	if v2, _ := s.Store.Get(ctx, "o", "a", 2); v2.Config.Instructions != "Ayer cambié esto" {
		t.Fatal("history was rewritten")
	}
	if len(w.audits) != 1 || w.audits[0].Action != "agent.config_restored" || w.audits[0].Details.(map[string]any)["restored_from"] != 1 {
		t.Fatalf("audit: %+v", w.audits)
	}
	if n := slices.Index(w.calls, OpSetProfile); n < 0 || slices.Index(w.calls[n+1:], OpSetProfile) >= 0 {
		t.Fatalf("the profile is restored in one call: %v", w.calls)
	}
	// restoring the current state again changes nothing
	r2, err := s.Restore(ctx, "o", "a", 3, person("Ana"), exec{w}, PlanEnv{})
	if err != nil || !r2.Unchanged || r2.Version != nil {
		t.Fatalf("no-op restore: %+v %v", r2, err)
	}
}

func TestRestoreNeverRaisesAutonomy(t *testing.T) {
	before := base()
	before.Autonomy = "autonomous"
	w, s := setup(t, before, func(c *Config) { c.Autonomy = "approve_each" }) // the system lowered it after an incident
	r := restore1(t, w, s)
	if !skipped(r, "autonomy", ReasonAutonomyEarned) || slices.Contains(w.calls, OpLowerAutonomy) {
		t.Fatalf("autonomy raised by a restore: %+v calls=%v", r, w.calls)
	}
	if r.Version != nil && r.Version.Config.Autonomy != "approve_each" {
		t.Fatal("autonomy changed")
	}
}

func TestRestoreLowersAutonomyThroughTheLadder(t *testing.T) {
	before := base()
	before.Autonomy = "approve_each"
	w, s := setup(t, before, func(c *Config) { c.Autonomy = "rules" })
	r := restore1(t, w, s)
	if !slices.ContainsFunc(r.Applied, func(st Step) bool { return st.Op == OpLowerAutonomy && st.Autonomy == "approve_each" }) ||
		!slices.Equal(w.lowered, []string{"approve_each"}) {
		t.Fatalf("lowering: %+v lowered=%v", r, w.lowered)
	}
}

func TestRestoreNeverRelaxesApprovals(t *testing.T) {
	before := base()
	before.Permissions = &Permissions{Exceptions: []Exception{{Category: policy.KindMessages, Mode: policy.ModeNever}}, FloorLifted: []string{policy.KindPayments}}
	w, s := setup(t, before, func(c *Config) { c.Permissions = &Permissions{} }) // someone tightened it since
	r := restore1(t, w, s)
	if !slices.ContainsFunc(r.Skipped, func(st Step) bool { return st.Category == policy.KindMessages && st.Reason == ReasonPermissionsFreer }) ||
		!slices.ContainsFunc(r.Skipped, func(st Step) bool { return st.Category == policy.KindPayments && st.Reason == ReasonPermissionsFreer }) {
		t.Fatalf("freer permissions not skipped: %+v", r.Skipped)
	}
	if slices.Contains(w.calls, OpSetException) || slices.Contains(w.calls, OpRemoveException) {
		t.Fatalf("permissions touched: %v", w.calls)
	}
}

func TestRestoreTightensApprovals(t *testing.T) {
	amount := 100.0
	before := base()
	before.Permissions = &Permissions{Exceptions: []Exception{{Category: policy.KindSpend, Mode: policy.ModeAskIf, If: &policy.AskIf{AmountOver: &amount}}}}
	w, s := setup(t, before, func(c *Config) {
		big := 500.0
		c.Permissions = &Permissions{Exceptions: []Exception{
			{Category: policy.KindMessages, Mode: policy.ModeNever},
			{Category: policy.KindSpend, Mode: policy.ModeAskIf, If: &policy.AskIf{AmountOver: &big}}},
			FloorLifted: []string{policy.KindDelete}}
	})
	r := restore1(t, w, s)
	got := r.Version.Config.Permissions
	if len(got.FloorLifted) != 0 || exceptionOf(got, policy.KindMessages) != nil ||
		exceptionOf(got, policy.KindSpend) == nil || *exceptionOf(got, policy.KindSpend).If.AmountOver != 100 {
		t.Fatalf("stricter permissions not restored: %+v (skipped %+v)", got, r.Skipped)
	}
}

func TestRestoreAdoptsFactoryRulesButNeverDropsThem(t *testing.T) {
	before := base()
	before.Permissions = &Permissions{Exceptions: []Exception{{Category: policy.KindPublish, Mode: policy.ModeAsk}}}
	w, s := setup(t, before, func(c *Config) { c.Permissions = nil })
	r := restore1(t, w, s)
	if !slices.Contains(w.calls, OpAdoptPermissions) || r.Version.Config.Permissions == nil {
		t.Fatalf("factory rules not adopted: %+v calls=%v", r, w.calls)
	}
	// and the other way: the old version had no choices; the current entry stays
	before = base()
	before.Permissions = nil
	w, s = setup(t, before, func(c *Config) { c.Permissions = &Permissions{} })
	r = restore1(t, w, s)
	if r.Version != nil && r.Version.Config.Permissions == nil {
		t.Fatal("factory rules switched off by a restore")
	}
}

func TestRestoreSkipsSkillsThatNoLongerExist(t *testing.T) {
	before := base()
	before.Skills = []SkillRef{{ID: "s1", Name: "Cotizar"}, {ID: "s2", Name: "Borrada"}}
	w, s := setup(t, before, func(c *Config) { c.Skills = []SkillRef{{ID: "s3", Name: "Nueva"}} })
	w.skills["s1"] = true // s2 was deleted or disabled
	r := restore1(t, w, s)
	if !slices.ContainsFunc(r.Skipped, func(st Step) bool { return st.Reason == ReasonSkillUnavailable && st.Skill.ID == "s2" && st.Skill.Name == "Borrada" }) {
		t.Fatalf("missing skill not reported: %+v", r.Skipped)
	}
	ids := []string{}
	for _, sk := range r.Version.Config.Skills {
		ids = append(ids, sk.ID)
	}
	if !slices.Equal(ids, []string{"s1"}) {
		t.Fatalf("skills after restore: %v", ids)
	}
}

func TestRestoreKeepsTheBudgetUnderTheCeiling(t *testing.T) {
	before := base()
	before.BudgetUSD = 500
	w, s := setup(t, before, func(c *Config) { c.BudgetUSD = 50 })
	w.ceiling = 200 // the department's limit went down since
	r := restore1(t, w, s)
	if !skipped(r, "budget_usd", ReasonBudgetAboveCeiling) || slices.Contains(w.calls, OpSetBudget) {
		t.Fatalf("budget above the ceiling restored: %+v", r)
	}
	w.ceiling = 1000
	r = restore1(t, w, s)
	if r.Version == nil || r.Version.Config.BudgetUSD != 500 {
		t.Fatalf("budget within the ceiling not restored: %+v", r)
	}
}

func TestRestoreOnlyNarrowsTools(t *testing.T) {
	before := base()
	before.Tools = []string{"email", "crm", "payments"}
	w, s := setup(t, before, func(c *Config) { c.Tools = []string{"email"}; c.Job.Responsibilities = []string{"Otra"} })
	r := restore1(t, w, s)
	if !skipped(r, "tools", ReasonToolsFreer) || !slices.Equal(r.Version.Config.Tools, []string{"email"}) ||
		r.Version.Config.Job.Responsibilities[0] != "Vender" {
		t.Fatalf("tools: %+v cfg=%+v", r.Skipped, r.Version.Config)
	}
}

func TestRestoreReportsWhatThePersonMayNotChange(t *testing.T) {
	w, s := setup(t, base(), func(c *Config) { c.BudgetUSD = 80; c.Instructions = "x" })
	w.forbid[OpSetBudget] = true
	r := restore1(t, w, s)
	if !skipped(r, "budget_usd", ReasonNotAllowed) || r.Version.Config.Instructions != "Sé breve." {
		t.Fatalf("partial restore: %+v", r)
	}
}

func TestControlsAndStructureAreShownNotRestored(t *testing.T) {
	w, s := setup(t, base(), func(c *Config) { c.Paused = true; c.Department = "Legal"; c.Access = &Access{Owner: "person:u1"} })
	r := restore1(t, w, s)
	if !skipped(r, "paused", ReasonControl) || !skipped(r, "department", ReasonStructural) || !skipped(r, "access", ReasonAccess) || !r.Unchanged {
		t.Fatalf("controls: %+v", r)
	}
}

func TestAgentsNeverRestore(t *testing.T) {
	w, s := setup(t, base(), func(c *Config) { c.Instructions = "x" })
	for _, actor := range []string{"agent:a", "a", "system", "orchestrator", ""} {
		if _, err := s.Restore(ctx, "o", "a", 1, Cause{Actor: actor}, exec{w}, PlanEnv{}); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("actor %q restored: %v", actor, err)
		}
	}
	if len(w.calls) != 0 {
		t.Fatalf("calls: %v", w.calls)
	}
}

type nopPub struct{}

func (nopPub) Publish(context.Context, domain.Event) error { return nil }

func TestObserverRecordsTheAutomaticDemotionAsTheSystem(t *testing.T) {
	w := newWorld()
	w.put("o", "a", base())
	s := w.svc()
	s.Record(ctx, "o", "a", Cause{Source: "baseline"})
	o := &Observer{Inner: nopPub{}}
	o.Bind(s)
	w.edit("o", "a", func(c *Config) { c.Autonomy = "approve_each" })
	// the ladder runs in the background of a person's approval decision: the actor is still in the context
	c := application.WithActor(ctx, "u-carla")
	_ = o.Publish(c, domain.Event{Type: domain.EvAutonomyChanged, OrgID: "o", AgentID: "a",
		Payload: map[string]any{"source": "auto_demotion", "from": "rules", "to": "approve_each"}})
	s.Flush()
	v, _, _ := s.Store.Last(ctx, "o", "a")
	if v.Version != 2 || v.Actor != ActorSystem || v.ActorKind != ActorSystem || v.Source != "trust.auto_demotion" || !slices.Equal(v.Fields, []string{"autonomy"}) {
		t.Fatalf("demotion version: %+v", v)
	}
}

func TestObserverLeavesHookedRequestsToTheirCaller(t *testing.T) {
	w := newWorld()
	w.put("o", "a", base())
	s := w.svc()
	o := &Observer{Inner: nopPub{}}
	o.Bind(s)
	hc, done := WithHook(application.WithActor(ctx, "u-carla"))
	if !Hooked(hc) || Hooked(ctx) {
		t.Fatal("hooked")
	}
	_ = o.Publish(hc, domain.Event{Type: domain.EvAutonomyChanged, OrgID: "o", AgentID: "a", Payload: map[string]any{"source": "auto_demotion"}})
	// a later event of the same agent in the request does not hide that the system acted on its own
	_ = o.Publish(hc, domain.Event{Type: "agent.profile_changed", OrgID: "o", AgentID: "a"})
	_ = o.Publish(hc, domain.Event{Type: domain.EvAgentCreated, OrgID: "o", AgentID: "b"})
	s.Flush()
	if _, had, _ := s.Store.Last(ctx, "o", "a"); had {
		t.Fatal("the observer recorded inside a hooked request")
	}
	pending := done()
	if c := pending["a"]; c.Source != "trust.auto_demotion" || c.Actor != ActorSystem {
		t.Fatalf("system cause not handed over: %+v", pending)
	}
	if c := pending["b"]; c.Source != domain.EvAgentCreated || c.Actor != "u-carla" {
		t.Fatalf("hire not handed over: %+v", pending)
	}
	if Hooked(hc) {
		t.Fatal("still hooked after done")
	}
	_ = o.Publish(hc, domain.Event{Type: domain.EvAgentCreated, OrgID: "o", AgentID: "a"})
	s.Flush()
	if _, had, _ := s.Store.Last(ctx, "o", "a"); !had {
		t.Fatal("work that outlives the request is not recorded")
	}
}

func TestRecordAllVisitsEveryAgentOfTheOrg(t *testing.T) {
	w := newWorld()
	w.put("o", "a", base())
	w.put("o", "b", base())
	w.put("x", "c", base())
	s := w.svc()
	s.RecordAll(ctx, "o", Cause{Actor: "u-1", Source: "POST /onboarding"})
	for _, id := range []string{"a", "b"} {
		if _, had, _ := s.Store.Last(ctx, "o", id); !had {
			t.Fatalf("%s not recorded", id)
		}
	}
	if _, had, _ := s.Store.Last(ctx, "x", "c"); had {
		t.Fatal("another organization was recorded")
	}
}

func TestConcurrentRecordsDoNotCollide(t *testing.T) {
	w := newWorld()
	w.put("o", "a", base())
	s := w.svc()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w.edit("o", "a", func(c *Config) { c.Instructions = fmt.Sprint(i) })
			if _, _, err := s.Record(ctx, "o", "a", person("Carla")); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	l, _ := s.Store.List(ctx, "o", "a", 0)
	for i := 1; i < len(l); i++ {
		if l[i-1].Version != l[i].Version+1 {
			t.Fatalf("gap or duplicate: %d then %d", l[i-1].Version, l[i].Version)
		}
	}
}
