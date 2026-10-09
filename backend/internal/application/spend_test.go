package application_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// The cached org spend must equal the ledger under concurrent recording.
func TestOrgSpentCounterMatchesStoreUnderConcurrency(t *testing.T) {
	h := newHarness(t, &fakeRuntime{plan: proposalPlan()}, nil)
	ctx := context.Background()
	_ = h.store.CreateRequest(ctx, domain.DemoOrgID, domain.Request{ID: "r1", Text: "x", Status: domain.RequestRunning, CreatedAt: time.Now()})
	b := h.orch.Budget()
	if _, err := b.OrgSpent(ctx, domain.DemoOrgID); err != nil { // seeds the counter
		t.Fatal(err)
	}
	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := b.Record(ctx, "r1", domain.UsageEntry{AgentID: "sales", Kind: domain.UsageRunTask, Model: "m"}, 0.01); err != nil {
				t.Error(err)
			}
		}()
		go func() { // readers racing with the writers (and with re-seeding)
			defer wg.Done()
			if _, err := b.OrgSpent(ctx, domain.DemoOrgID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	want, _ := h.store.OrgCost(ctx, domain.DemoOrgID)
	got, err := b.OrgSpent(ctx, domain.DemoOrgID)
	if err != nil || !near(got, want) || !near(got, n*0.01) {
		t.Fatalf("cached spend = %v (err %v), store = %v, want %v", got, err, want, n*0.01)
	}
}

// Caps stay exact with the counter: spent + reserved + estimate <= cap.
func TestOrgCapEnforcedExactlyWithSpendCounter(t *testing.T) {
	h := newHarness(t, &fakeRuntime{plan: proposalPlan()}, func(c *application.Config) { c.BudgetUSD = 1 })
	ctx := context.Background()
	_ = h.store.CreateRequest(ctx, domain.DemoOrgID, domain.Request{ID: "r1", Text: "x", Status: domain.RequestRunning, CreatedAt: time.Now()})
	b := h.orch.Budget()
	if _, err := b.Record(ctx, "r1", domain.UsageEntry{AgentID: "sales", Kind: domain.UsageRunTask, Model: "m"}, 0.6); err != nil {
		t.Fatal(err)
	}
	if _, ex, _ := b.Reserve(ctx, "r1", "sales", 0.5); ex == nil || ex.Scope != domain.ScopeOrg {
		t.Fatalf("0.6 spent + 0.5 must exceed the org cap of 1: %+v", ex)
	}
	r, ex, err := b.Reserve(ctx, "r1", "sales", 0.4)
	if ex != nil || err != nil {
		t.Fatalf("exact fit must pass: ex=%v err=%v", ex, err)
	}
	// Recording while holding the reservation, then releasing, leaves 1.0 spent.
	if _, err := b.Record(ctx, "r1", domain.UsageEntry{AgentID: "sales", Kind: domain.UsageRunTask, Model: "m"}, 0.4); err != nil {
		t.Fatal(err)
	}
	r.Release()
	if _, ex, _ := b.Reserve(ctx, "r1", "sales", 0.01); ex == nil {
		t.Fatal("the org budget is fully spent: any further call must be blocked")
	}
}

// The counter is a cache: it must not query the store on every read (the point
// of G11). A cost written behind its back is only seen at the next re-sync.
func TestOrgSpentIsServedFromTheCounter(t *testing.T) {
	h := newHarness(t, &fakeRuntime{plan: proposalPlan()}, nil)
	ctx := context.Background()
	_ = h.store.CreateRequest(ctx, domain.DemoOrgID, domain.Request{ID: "r1", Text: "x", Status: domain.RequestRunning, CreatedAt: time.Now()})
	b := h.orch.Budget()
	if v, _ := b.OrgSpent(ctx, domain.DemoOrgID); v != 0 {
		t.Fatalf("spent = %v", v)
	}
	if err := h.store.AddCost(ctx, domain.DemoOrgID, "r1", "", 2); err != nil {
		t.Fatal(err)
	}
	if v, _ := b.OrgSpent(ctx, domain.DemoOrgID); v != 0 {
		t.Fatalf("a fresh counter must not re-query the store, got %v", v)
	}
}
