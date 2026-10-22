package trust_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/roles"
	"aiworkforce/backend/internal/trust"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type fixture struct {
	t       *testing.T
	mem     *memory.Store
	svc     *trust.Service
	clk     *clock
	blocked string
	org     string
	ctx     context.Context
	n       int
}

const day = 24 * time.Hour

func newFixture(t *testing.T) *fixture {
	t.Helper()
	mem := memory.New()
	if err := mem.Seed(context.Background(), domain.SeedOrg(50), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	// a green-tier profession that starts at approve_each (ceiling autonomous)
	tpl, _ := roles.Get("data_analyst")
	if err := mem.CreateAgent(context.Background(), domain.DemoOrgID, roles.Instantiate(tpl, "es", "data_analyst", "Dana")); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: domain.DemoOrgID, Store: mem, Pub: events.LocalBus{Hub: events.NewHub(log)}, Log: log}
	f := &fixture{t: t, mem: mem, clk: &clock{t: time.Now().UTC().Add(-40 * day)}, org: domain.DemoOrgID}
	f.svc = trust.New(trust.Config{Store: trust.NewMemStore(), Signals: trust.StoreSignals{Store: mem, Audit: mem}, Agents: mem,
		SetAutonomy: mem.SetAgentAutonomy, Rec: rec, OrgID: domain.DemoOrgID, Now: f.clk.now, Log: log,
		Blocked: func(context.Context, string, string) string { return f.blocked }})
	f.ctx = application.WithActor(application.WithOrg(context.Background(), f.org), "owner-1")
	return f
}

func (f *fixture) decide(agent string, st domain.ApprovalStatus, risk string, n int) {
	f.t.Helper()
	f.clk.add(time.Second) // decisions happen after the level started
	for i := 0; i < n; i++ {
		f.n++
		at := f.clk.now()
		if err := f.mem.CreateApproval(context.Background(), f.org, domain.Approval{ID: fmt.Sprintf("ap-%d", f.n), AgentID: agent, Action: "send", Risk: risk,
			Status: st, CreatedAt: at.Add(-time.Minute), ResolvedAt: &at}); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *fixture) level(agent string) string {
	a, err := f.mem.GetAgent(context.Background(), f.org, agent)
	if err != nil {
		f.t.Fatal(err)
	}
	return a.Autonomy
}

func (f *fixture) eval(agent string) trust.AgentTrust {
	f.t.Helper()
	v, err := f.svc.Evaluate(f.ctx, agent)
	if err != nil {
		f.t.Fatal(err)
	}
	return v
}

func (f *fixture) audits(action string) int {
	page, _ := f.mem.QueryAudit(context.Background(), f.org, domain.AuditQuery{Action: action, Limit: 1000})
	return len(page.Items)
}

// The ladder suggests, a human accepts: autonomy never goes up on its own.
func TestPromotionIsSuggestedNeverApplied(t *testing.T) {
	f := newFixture(t)
	const a = "data_analyst"
	if v := f.eval(a); v.Recommendation != trust.RecProbation || v.Level != trust.LevelApproveEach || v.Ceiling != trust.LevelAutonomous {
		t.Fatalf("new agent: %+v", v)
	}
	f.clk.add(15 * day)
	f.decide(a, domain.ApprovalApproved, "low", 20)
	v := f.eval(a)
	if v.Recommendation != trust.RecPromote || v.Pending == nil || v.Pending.ToLevel != trust.LevelRules {
		t.Fatalf("strong evidence: %+v", v)
	}
	if f.level(a) != trust.LevelApproveEach {
		t.Fatal("a suggestion must not change autonomy")
	}
	// evaluating again keeps one pending proposal and still changes nothing
	f.eval(a)
	if ps, _ := f.svc.Proposals(f.ctx, a, 10); len(ps) != 1 || f.level(a) != trust.LevelApproveEach || f.audits("trust.promotion_suggested") != 1 {
		t.Fatalf("idempotent evaluation: %d proposals", len(ps))
	}

	// Agents, system principals and members cannot accept.
	for _, ctx := range []context.Context{
		application.WithActor(f.ctx, "agent:data_analyst"),
		application.WithActor(f.ctx, "data_analyst"),
		application.WithActor(f.ctx, "system"),
		application.WithActorRole(f.ctx, "member"),
	} {
		if _, err := f.svc.Accept(ctx, v.Pending.ID, ""); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("refused decider: %v", err)
		}
	}
	if f.audits("trust.decision_refused") != 4 || f.level(a) != trust.LevelApproveEach {
		t.Fatal("refusals are audited and change nothing")
	}

	// Kill switch / read-only / pause: the raise waits.
	f.blocked = "kill_switch_active"
	var be trust.BlockedError
	if _, err := f.svc.Accept(f.ctx, v.Pending.ID, ""); !errors.As(err, &be) || be.Code != "kill_switch_active" || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("blocked accept: %v", err)
	}
	f.blocked = ""

	p, err := f.svc.Accept(f.ctx, v.Pending.ID, "earned it")
	if err != nil || p.Status != trust.StatusAccepted || p.ResolvedBy != "owner-1" {
		t.Fatalf("accept: %+v %v", p, err)
	}
	if f.level(a) != trust.LevelRules || f.audits("agent.autonomy_changed") != 1 {
		t.Fatal("accepted promotion raises exactly one level, audited")
	}
	if _, err := f.svc.Accept(f.ctx, p.ID, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("double accept: %v", err)
	}
	// The evidence starts again at the new level.
	v = f.eval(a)
	if v.Counts.Decisions() != 0 || v.Recommendation == trust.RecPromote || v.LastChange == nil || v.LastChange.ID != p.ID {
		t.Fatalf("after promotion: %+v", v)
	}
}

func TestPromotionNeverExceedsCeiling(t *testing.T) {
	f := newFixture(t)
	const a = "sales" // amber tier, starts at rules = its ceiling
	f.eval(a)
	f.clk.add(15 * day)
	f.decide(a, domain.ApprovalApproved, "low", 40)
	v := f.eval(a)
	if v.Recommendation != trust.RecAtCeiling || v.Pending != nil || v.Ceiling != trust.LevelRules {
		t.Fatalf("at ceiling: %+v", v)
	}
	if v.Progress >= 1 && v.NextLevel != "" {
		t.Fatal("no next level at the ceiling")
	}
}

func TestForgedProposalAboveCeilingIsRefused(t *testing.T) {
	f := newFixture(t)
	st := trust.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := trust.New(trust.Config{Store: st, Signals: trust.StoreSignals{Store: f.mem}, Agents: f.mem, SetAutonomy: f.mem.SetAgentAutonomy,
		OrgID: f.org, Now: f.clk.now, Log: log})
	// somebody wrote a proposal straight into the store: sales (ceiling rules) to autonomous
	_ = st.CreateProposal(context.Background(), f.org, trust.Proposal{ID: "forged", AgentID: "sales", Kind: trust.KindPromotion,
		FromLevel: trust.LevelRules, ToLevel: trust.LevelAutonomous, Status: trust.StatusPending, CreatedAt: f.clk.now()})
	f.clk.add(15 * day)
	f.decide("sales", domain.ApprovalApproved, "low", 40)
	if _, err := svc.Accept(f.ctx, "forged", ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("forged proposal: %v", err)
	}
	if f.level("sales") != trust.LevelRules {
		t.Fatal("autonomy above the ceiling")
	}
}

func TestAutomaticDemotionAndRevert(t *testing.T) {
	f := newFixture(t)
	const a = "assistant" // green, rules, ceiling autonomous
	f.eval(a)
	f.clk.add(time.Hour)
	f.decide(a, domain.ApprovalApproved, "low", 4)
	f.decide(a, domain.ApprovalRejected, "high", 1)
	// a system rejection (timeout) is never held against the agent
	at := f.clk.now()
	_ = f.mem.CreateApproval(context.Background(), f.org, domain.Approval{ID: "timeout", AgentID: a, Risk: "high", Status: domain.ApprovalRejected,
		Note: "sin respuesta: tiempo agotado", CreatedAt: at, ResolvedAt: &at})
	v := f.eval(a)
	if f.level(a) != trust.LevelApproveEach || v.LastChange == nil || v.LastChange.Status != trust.StatusApplied || !v.LastChange.Auto || !v.CanRevert {
		t.Fatalf("automatic demotion: level %s view %+v", f.level(a), v.LastChange)
	}
	if v.LastChange.Reasons[0] != trust.TrigHighRisk || v.LastChange.Evidence.Counts.HighRiskRejected != 1 {
		t.Fatalf("evidence: %+v", v.LastChange)
	}
	// one level per incident: the same old rejection does not demote again (the evidence restarted)
	if f.eval(a); f.level(a) != trust.LevelApproveEach {
		t.Fatal("demoted twice for one incident")
	}
	// revert needs a human with the role, and the controls
	if _, err := f.svc.Revert(application.WithActorRole(f.ctx, "viewer"), v.LastChange.ID, ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("viewer revert: %v", err)
	}
	f.blocked = "read_only_mode"
	if _, err := f.svc.Revert(f.ctx, v.LastChange.ID, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("revert in read-only: %v", err)
	}
	f.blocked = ""
	if p, err := f.svc.Revert(f.ctx, v.LastChange.ID, "false alarm"); err != nil || p.Status != trust.StatusReverted {
		t.Fatalf("revert: %+v %v", p, err)
	}
	if f.level(a) != trust.LevelRules {
		t.Fatal("revert restores the previous level")
	}
	if _, err := f.svc.Revert(f.ctx, v.LastChange.ID, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second revert: %v", err)
	}
}

func TestRevertWindowCloses(t *testing.T) {
	f := newFixture(t)
	const a = "assistant"
	f.eval(a)
	f.decide(a, domain.ApprovalRejected, "high", 1)
	v := f.eval(a)
	f.clk.add(8 * day)
	if _, err := f.svc.Revert(f.ctx, v.LastChange.ID, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("late revert: %v", err)
	}
}

func TestPolicyIncidentAndBurst(t *testing.T) {
	f := newFixture(t)
	f.eval("assistant")
	f.eval("analyst")
	f.clk.add(time.Hour)
	for i := 0; i < 3; i++ {
		_ = f.mem.AddAudit(context.Background(), f.org, domain.AuditLog{ID: fmt.Sprintf("d%d", i), TS: f.clk.now(), Actor: "assistant", Action: "tool.denied",
			Details: map[string]any{"reason": "policy:no_payments"}})
	}
	// denials that are not the agent's fault do not count
	_ = f.mem.AddAudit(context.Background(), f.org, domain.AuditLog{ID: "ks", TS: f.clk.now(), Actor: "analyst", Action: "tool.denied", Details: map[string]any{"reason": "kill_switch_active"}})
	if v := f.eval("assistant"); f.level("assistant") != trust.LevelApproveEach || v.LastChange.Reasons[0] != trust.TrigPolicy {
		t.Fatalf("policy incident: %s %+v", f.level("assistant"), v.LastChange)
	}
	f.decide("analyst", domain.ApprovalRejected, "low", 3)
	if v := f.eval("analyst"); f.level("analyst") != trust.LevelApproveEach || v.LastChange.Reasons[0] != trust.TrigBurst {
		t.Fatalf("burst: %s", f.level("analyst"))
	}
}

func TestSuggestedDemotionDeclineAndCooldown(t *testing.T) {
	f := newFixture(t)
	const a = "assistant"
	f.eval(a)
	f.clk.add(15 * day)
	// 7/10 approved over two weeks (no burst)
	f.decide(a, domain.ApprovalRejected, "low", 3)
	f.clk.add(4 * day)
	f.decide(a, domain.ApprovalApproved, "low", 7)
	v := f.eval(a)
	if v.Recommendation != trust.RecDemote || v.Pending == nil || v.Pending.Kind != trust.KindDemotion || f.level(a) != trust.LevelRules {
		t.Fatalf("suggested demotion: %+v level %s", v, f.level(a))
	}
	if _, err := f.svc.Accept(f.ctx, v.Pending.ID, ""); err != nil {
		t.Fatal(err)
	}
	if f.level(a) != trust.LevelApproveEach {
		t.Fatal("accepted demotion lowers the level")
	}

	// a declined promotion is not suggested again for a week
	const b = "data_analyst"
	f.eval(b)
	f.clk.add(15 * day)
	f.decide(b, domain.ApprovalApproved, "low", 20)
	v = f.eval(b)
	if _, err := f.svc.Decline(f.ctx, v.Pending.ID, "not yet"); err != nil {
		t.Fatal(err)
	}
	if v = f.eval(b); v.Recommendation != trust.RecCooldown || v.Pending != nil {
		t.Fatalf("cooldown: %+v", v)
	}
	f.clk.add(8 * day)
	f.decide(b, domain.ApprovalApproved, "low", 20)
	if v = f.eval(b); v.Pending == nil {
		t.Fatalf("after cooldown: %+v", v.Recommendation)
	}
}

func TestLevelChangedElsewhereSupersedesAndResetRestores(t *testing.T) {
	f := newFixture(t)
	const a = "data_analyst"
	f.eval(a)
	f.clk.add(15 * day)
	f.decide(a, domain.ApprovalApproved, "low", 20)
	v := f.eval(a)
	// an onboarding pack moves the agent: the proposal is stale
	_ = f.mem.SetAgentAutonomy(context.Background(), f.org, a, trust.LevelSuggest)
	if _, err := f.svc.Accept(f.ctx, v.Pending.ID, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale accept: %v", err)
	}
	if f.level(a) != trust.LevelSuggest {
		t.Fatal("stale accept changed the level")
	}
	_ = f.mem.SetAgentAutonomy(context.Background(), f.org, a, trust.LevelApproveEach)

	// demote assistant automatically, then reset the demo
	f.eval("assistant")
	f.decide("assistant", domain.ApprovalRejected, "high", 1)
	f.eval("assistant")
	if f.level("assistant") != trust.LevelApproveEach {
		t.Fatal("setup")
	}
	if err := f.svc.Reset(f.ctx); err != nil {
		t.Fatal(err)
	}
	if f.level("assistant") != trust.LevelRules {
		t.Fatal("reset restores the start level")
	}
}

func TestObserverEvaluatesInTheBackground(t *testing.T) {
	f := newFixture(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	obs := &trust.Observer{Inner: events.LocalBus{Hub: events.NewHub(log)}}
	obs.Bind(f.svc)
	f.eval("assistant") // tracking started earlier (hire or first look)
	f.decide("assistant", domain.ApprovalRejected, "high", 1)
	_ = obs.Publish(context.Background(), domain.Event{Type: domain.EvApprovalResolved, OrgID: f.org, AgentID: "assistant"})
	_ = obs.Publish(context.Background(), domain.Event{Type: domain.EvAutonomyChanged, OrgID: f.org, AgentID: "analyst"}) // not a trigger
	deadline := time.Now().Add(5 * time.Second)
	for f.level("assistant") != trust.LevelApproveEach {
		if time.Now().After(deadline) {
			t.Fatal("the observer did not evaluate the agent")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
