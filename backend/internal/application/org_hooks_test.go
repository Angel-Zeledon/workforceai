package application_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// ---- department budget (budget_dept.go) ----

func TestDepartmentLimitBlocksTheDepartmentAndStrictestWins(t *testing.T) {
	h := newHarness(t, &fakeRuntime{plan: proposalPlan()}, nil)
	ctx := context.Background()
	dept := "Ventas"
	if err := h.store.SetAgentDepartment(ctx, domain.DemoOrgID, "sales", &dept); err != nil {
		t.Fatal(err)
	}
	b := h.orch.Budget()
	b.DepartmentLimitsFor = func(context.Context, string) map[string]float64 { return map[string]float64{"ventas": 0.10} } // case-insensitive tag match
	r1, ex, err := b.Reserve(ctx, "r1", "sales", 0.06)
	if err != nil || ex != nil {
		t.Fatalf("first reservation fits: ex=%v err=%v", ex, err)
	}
	_, ex, _ = b.Reserve(ctx, "r1", "sales", 0.06)
	if ex == nil || ex.Scope != domain.ScopeDepartment || !ex.Resumable() || !near(ex.CapUSD, 0.10) || !near(ex.ReservedUSD, 0.06) {
		t.Fatalf("department limit must block: %+v", ex)
	}
	if r, ex, _ := b.Reserve(ctx, "r1", "legal", 0.06); ex != nil { // other department: untouched
		t.Fatalf("agent without department is not limited: %v", ex)
	} else {
		r.Release()
	}
	r1.Release()
	// An agent cap stricter than the department limit wins.
	if err := h.store.SetBudgetCap(ctx, domain.DemoOrgID, domain.ScopeAgent, "sales", 0.03); err != nil {
		t.Fatal(err)
	}
	if _, ex, _ := b.Reserve(ctx, "r1", "sales", 0.06); ex == nil || ex.Scope != domain.ScopeAgent {
		t.Fatalf("strictest limit must be reported: %+v", ex)
	}
	st, err := b.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range st.Departments {
		if d.Department == "ventas" && near(d.LimitUSD, 0.10) && d.Agents == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("status must list the department: %+v", st.Departments)
	}
}

// ---- delegation (delegation_hook.go) ----

type denyChecker struct{ calls atomic.Int32 }

func (c *denyChecker) CheckDelegation(_ context.Context, _ string, from application.DelegationActor, to string) application.DelegationVerdict {
	c.calls.Add(1)
	if to == "legal" {
		return application.DelegationVerdict{Allowed: false, RuleID: "r-deny", Reason: "denied_by_rule"}
	}
	return application.DelegationVerdict{Allowed: true, Reason: "no_rule"}
}

func TestDelegationDenyRefusesPresetPlanAndAudits(t *testing.T) {
	h := newHarness(t, &fakeRuntime{plan: proposalPlan()}, nil)
	c := &denyChecker{}
	h.orch.SetDelegation(c)
	ctx := application.WithActor(context.Background(), "u1")
	_, err := h.orch.SubmitPlan(ctx, "x", application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "A", AgentID: "legal"}}})
	var de *application.DelegationError
	if !errors.As(err, &de) || !errors.Is(err, domain.ErrForbidden) || de.Verdict.RuleID != "r-deny" {
		t.Fatalf("want a DelegationError wrapping ErrForbidden, got %v", err)
	}
	if !h.hasAudit("delegation.denied") {
		t.Fatal("a denial must be audited")
	}
	h.orch.SetDelegation(nil)
	if _, err := h.orch.SubmitPlan(ctx, "x", application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "A", AgentID: "legal"}}}); err != nil {
		t.Fatalf("without a checker nothing is enforced: %v", err)
	}
}

// ---- holds (hold.go) ----

func TestMemoryHoldsRoundTrip(t *testing.T) {
	s := &application.MemoryHolds{}
	ctx := context.Background()
	at := time.Now().Add(time.Hour)
	_ = s.PutHold(ctx, "o", application.Hold{TaskID: "t2", WakeAt: at.Add(time.Minute)})
	_ = s.PutHold(ctx, "o", application.Hold{TaskID: "t1", WakeAt: at})
	hs, _ := s.ListHolds(ctx, "o")
	if len(hs) != 2 || hs[0].TaskID != "t1" {
		t.Fatalf("holds must be listed by wake time: %+v", hs)
	}
	_ = s.DeleteHold(ctx, "o", "t1")
	if hs, _ = s.ListHolds(ctx, "o"); len(hs) != 1 {
		t.Fatalf("delete: %+v", hs)
	}
	if other, _ := s.ListHolds(ctx, "x"); len(other) != 0 {
		t.Fatal("holds are per organization")
	}
}

type untilGate struct {
	until time.Time
	open  atomic.Bool
}

func (g *untilGate) GateTask(context.Context, string, domain.Task) application.GateDecision {
	if g.open.Load() {
		return application.GateDecision{Action: application.GateRun}
	}
	return application.GateDecision{Action: application.GateHold, Reason: "closed", Until: g.until}
}
func (g *untilGate) GateResolved(context.Context, string, domain.Task, string, bool) {}

func TestHeldTaskIsRecordedAndWakeHeldReleasesIt(t *testing.T) {
	h := newHarness(t, &fakeRuntime{plan: proposalPlan(), runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		return okResult("hecho"), nil
	}}, nil)
	store := &application.MemoryHolds{}
	h.orch.SetHoldStore(store)
	g := &untilGate{until: time.Now().Add(time.Hour)}
	h.orch.SetTaskGate(g)
	ctx := context.Background()
	id, err := h.orch.SubmitPlan(ctx, "x", application.PlanResponse{Tasks: []application.PlannedTask{{Key: "a", Title: "A", AgentID: "sales"}}})
	if err != nil {
		t.Fatal(err)
	}
	h.waitFor("hold recorded", func() bool { return len(h.orch.Holds(ctx)) == 1 })
	if got := h.orch.Holds(ctx)[0]; got.RequestID != id || got.Reason != "closed" {
		t.Fatalf("unexpected hold: %+v", got)
	}
	g.open.Store(true)
	h.orch.WakeHeld() // an hour-long sleep is cut short
	h.waitFor("request done and hold removed", func() bool {
		return h.request(id).Status == domain.RequestDone && len(h.orch.Holds(ctx)) == 0
	})
}
