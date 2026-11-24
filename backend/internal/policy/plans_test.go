package policy

import "testing"

func TestCatalogIsOrderedAndEveryPlanIsCapped(t *testing.T) {
	if len(Catalog) != 3 || Catalog[0].ID != PlanFree || Catalog[1].ID != PlanPro || Catalog[2].ID != PlanMax {
		t.Fatalf("catalog: %+v", Catalog)
	}
	if Catalog[0].PriceMonthlyUSD != 0 {
		t.Fatalf("free must be free: %+v", Catalog[0])
	}
	for i, p := range Catalog {
		if !HardCapped(p.ID) || p.AIAllowanceUSD <= 0 {
			t.Fatalf("%s must have a hard AI allowance: %+v", p.ID, p)
		}
		// at full use the AI may not eat more than half of the price
		if p.PriceMonthlyUSD > 0 && p.AIAllowanceUSD > p.PriceMonthlyUSD/2 {
			t.Fatalf("%s: allowance %.2f is too close to the price %.2f", p.ID, p.AIAllowanceUSD, p.PriceMonthlyUSD)
		}
		if p.PriceYearlyUSD != p.PriceMonthlyUSD*10 {
			t.Fatalf("%s: a year is ten months (two free): %+v", p.ID, p)
		}
		if i == 0 {
			continue
		}
		q := Catalog[i-1]
		if p.PriceMonthlyUSD <= q.PriceMonthlyUSD || p.AIAllowanceUSD <= q.AIAllowanceUSD || p.MaxAgents < q.MaxAgents ||
			p.MaxActiveProjects < q.MaxActiveProjects || p.MaxSchedules < q.MaxSchedules || p.MaxSeats < q.MaxSeats || p.StorageBytes <= q.StorageBytes {
			t.Fatalf("%s must give at least what %s gives, for more money", p.ID, q.ID)
		}
		if Rank(p.ID) <= Rank(q.ID) {
			t.Fatalf("rank of %s", p.ID)
		}
	}
	// An industry pack (up to 10 agents) on top of the 7 seed agents must fit even on Free.
	if Catalog[0].MaxAgents < 17 {
		t.Fatalf("free agents %d: an industry pack would not fit", Catalog[0].MaxAgents)
	}
}

func TestLegacyPaidPlanHasNoPlanLimits(t *testing.T) {
	if HardCapped(PlanPaid) || IsCatalogPlan(PlanPaid) || IsCatalogPlan("enterprise") {
		t.Fatal("paid is the legacy plan without a cap")
	}
	tier, ok := TierOf(PlanPaid)
	if ok || tier.MaxAgents != 0 || tier.MaxSeats != 0 || tier.StorageBytes != 0 {
		t.Fatalf("%+v", tier)
	}
	if AtLimit(1000, 0) || !AtLimit(2, 2) || AtLimit(1, 2) {
		t.Fatal("AtLimit")
	}
}

func TestHelperLimitsByPlan(t *testing.T) {
	cases := []struct {
		plan             string
		perOrg           int
		wantPar, wantMax int
	}{
		{PlanFree, 8, 3, 6},
		{PlanPro, 8, 6, 20},
		{PlanMax, 8, 8, 50},
		{PlanMax, 4, 4, 50}, // never more than MAX_PARALLEL_PER_ORG
		{PlanPro, 2, 2, 20},
		{PlanFree, 0, 3, 6},  // no per-organization cap configured
		{PlanPaid, 8, 8, 50}, // legacy: system maximums, still bounded by the organization
		{"unknown", 5, 5, 50},
	}
	for _, c := range cases {
		tier, _ := TierOf(c.plan)
		p, m := HelperLimits(tier, c.perOrg)
		if p != c.wantPar || m != c.wantMax {
			t.Errorf("%s/%d: got %d/%d, want %d/%d", c.plan, c.perOrg, p, m, c.wantPar, c.wantMax)
		}
	}
}
