package notify

import (
	"context"
	"errors"
	"testing"

	"aiworkforce/backend/internal/policy"
)

func TestChangePlanSetsTheCatalogAllowanceAndLimits(t *testing.T) {
	ctx := context.Background()
	p := &Plans{Store: NewMemPlans(), DefaultFreeCapUSD: 1}
	if l := p.Limits(ctx, "o1"); l.ID != policy.PlanFree || l.MaxActiveProjects != 1 {
		t.Fatalf("default: %+v", l)
	}
	got, err := p.Change(ctx, "o1", policy.PlanPro, "owner")
	if err != nil {
		t.Fatal(err)
	}
	pro, _ := policy.TierOf(policy.PlanPro)
	if got.Plan != policy.PlanPro || got.CapUSD != pro.AIAllowanceUSD || !got.Hard() {
		t.Fatalf("%+v", got)
	}
	if l := p.Limits(ctx, "o1"); l.MaxActiveProjects != pro.MaxActiveProjects {
		t.Fatalf("limits after the change: %+v", l)
	}
	// the budget hook sees the new allowance at once (the cache was dropped)
	if c := p.OrgCap(ctx, "o1"); c != pro.AIAllowanceUSD {
		t.Fatalf("org cap %v", c)
	}
	// back to free: the deployment's free cap wins over the catalog's
	if got, _ := p.Change(ctx, "o1", policy.PlanFree, "owner"); got.CapUSD != 1 {
		t.Fatalf("%+v", got)
	}
	if _, err := p.Change(ctx, "o1", "enterprise", "owner"); !errors.Is(err, ErrUnknownPlan) {
		t.Fatalf("%v", err)
	}
	if _, err := p.Change(ctx, "o1", policy.PlanPaid, "owner"); !errors.Is(err, ErrUnknownPlan) {
		t.Fatalf("the legacy plan is not for sale: %v", err)
	}
}

func TestMaxPlanStatusIsCappedAndLegacyPaidIsNot(t *testing.T) {
	ctx := context.Background()
	p := &Plans{Store: NewMemPlans()}
	if l := p.Limits(ctx, "x"); l.ID != policy.PlanPaid || l.MaxAgents != 0 {
		t.Fatalf("without plans nothing is limited: %+v", l)
	}
	if c := p.OrgCap(ctx, "x"); c != 0 {
		t.Fatalf("legacy paid has no cap: %v", c)
	}
	_, _ = p.Change(ctx, "m", policy.PlanMax, "owner")
	st := p.Status(ctx, "m", 0, 45, 0)
	if !st.Hard || st.State != policy.CapExhausted || !st.Paused {
		t.Fatalf("max at its allowance must pause: %+v", st)
	}
	var nilPlans *Plans
	if l := nilPlans.Limits(ctx, "m"); l.ID != policy.PlanPaid {
		t.Fatalf("%+v", l)
	}
}
