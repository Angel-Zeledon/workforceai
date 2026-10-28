package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/roles"
)

// coreStub overrides the parts of the store the scanner reads (usage ledger, lifetime cost, approvals).
type coreStub struct {
	application.Store
	usage     []domain.UsageEntry
	cost      float64
	approvals []domain.Approval
}

func (c *coreStub) ListUsage(context.Context, string) ([]domain.UsageEntry, error) {
	return c.usage, nil
}
func (c *coreStub) OrgCost(context.Context, string) (float64, error) { return c.cost, nil }
func (c *coreStub) ListApprovals(context.Context, string, string) ([]domain.Approval, error) {
	return c.approvals, nil
}

type scanEnv struct {
	svc  *application.InitiativeService
	sc   *application.InitiativeScanner
	core *coreStub
	spy  *submitSpy
	now  *time.Time
}

// newScan: seeded agents (sales, accounting, analyst, assistant, ...), every source empty, a quiet clock on the 20th.
func newScan(t *testing.T) *scanEnv {
	t.Helper()
	mem := memory.New()
	if err := mem.Seed(context.Background(), domain.SeedOrg(50), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	spy := &submitSpy{}
	svc := &application.InitiativeService{Store: mem, Submit: spy, Cfg: application.DefaultConfig(), Now: func() time.Time { return now }}
	core := &coreStub{Store: mem}
	sc := &application.InitiativeScanner{Svc: svc, Core: core, Now: func() time.Time { return now }}
	return &scanEnv{svc: svc, sc: sc, core: core, spy: spy, now: &now}
}

func (e *scanEnv) scan(t *testing.T) []application.ScanResult {
	t.Helper()
	res, err := e.sc.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestClassifyMetric(t *testing.T) {
	for label, want := range map[string]string{
		"Margen bruto %": application.MetricMargin, "Gross margin": application.MetricMargin, "Margem": application.MetricMargin,
		"Ventas netas": application.MetricSales, "Revenue": application.MetricSales, "Facturación": application.MetricSales, "Receita": application.MetricSales,
		"Caja final": application.MetricCash, "Cash": application.MetricCash, "Tesorería": application.MetricCash,
		"Costo de ventas": "", "Cost of sales": "", "Gastos": "", "Mes": "", "": "", "Marginal": "",
	} {
		if got := application.ClassifyMetric(label); got != want {
			t.Errorf("ClassifyMetric(%q) = %q, want %q", label, got, want)
		}
	}
}

func TestSignalSettingsDefaultsValidationAndKeep(t *testing.T) {
	e := newScan(t)
	ctx := context.Background()
	st, err := e.svc.Settings(ctx)
	if err != nil || st.Signals == nil || *st.Signals != application.DefaultSignalSettings() {
		t.Fatalf("defaults = %+v %v", st, err)
	}
	bad := application.DefaultSignalSettings()
	bad.QualityDrop.DropPoints = 0
	if _, err := e.svc.PutSettings(ctx, application.InitiativeSettings{Enabled: true, DailyLimit: 3, MonthlyLimit: 30, Signals: &bad}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid threshold accepted: %v", err)
	}
	custom := application.DefaultSignalSettings()
	custom.MetricChange.Enabled, custom.MetricChange.MarginPoints = false, 5
	if _, err := e.svc.PutSettings(ctx, application.InitiativeSettings{Enabled: true, DailyLimit: 3, MonthlyLimit: 30, Signals: &custom}); err != nil {
		t.Fatal(err)
	}
	// A write without signals (older client) keeps the stored ones.
	if _, err := e.svc.PutSettings(ctx, application.InitiativeSettings{Enabled: true, DailyLimit: 4, MonthlyLimit: 30}); err != nil {
		t.Fatal(err)
	}
	st, _ = e.svc.Settings(ctx)
	if st.DailyLimit != 4 || st.Signals.MetricChange.Enabled || st.Signals.MetricChange.MarginPoints != 5 {
		t.Fatalf("signals not kept: %+v", st.Signals)
	}
}

func TestScanMetricChangeRaisesToTheRightRoleOnceAndAcceptingSubmitsARequest(t *testing.T) {
	e := newScan(t)
	e.sc.Series = func(context.Context) ([]application.MetricSeries, error) {
		return []application.MetricSeries{
			// margin 31 % -> 24 %: 7 points (threshold 3) -> accounting
			{ArtifactID: "art-1", ArtifactTitle: "Margen Acme", Column: "Margen", Metric: application.MetricMargin, Ref: "s1!B5", Period: "Septiembre", Previous: 0.31, Current: 0.24},
			// sales +4 % (threshold 10 %) -> nothing
			{ArtifactID: "art-2", ArtifactTitle: "Ventas", Column: "Ventas", Metric: application.MetricSales, Ref: "s1!C9", Previous: 1000, Current: 1040},
			// cash -25 % -> accounting (no finance_treasury agent among the seeded ones)
			{ArtifactID: "art-3", ArtifactTitle: "Flujo", Column: "Caja", Metric: application.MetricCash, Ref: "r7:cash", Previous: 2000, Current: 1500},
			// sales from 0 cannot be compared
			{ArtifactID: "art-4", ArtifactTitle: "Nuevo", Column: "Sales", Metric: application.MetricSales, Ref: "r1:sales", Previous: 0, Current: 50},
		}, nil
	}
	res := e.scan(t)
	if len(res) != 2 {
		t.Fatalf("results = %+v", res)
	}
	byKey := map[string]application.ScanResult{}
	for _, r := range res {
		byKey[r.SignalKey] = r
		if !r.Raised || r.Signal != application.SignalMetricChange || r.AgentID != "accounting" {
			t.Errorf("result = %+v", r)
		}
	}
	if _, ok := byKey["art-1:s1!b5"]; !ok {
		t.Fatalf("margin key missing: %+v", res)
	}
	list, _ := e.svc.List(context.Background(), application.InitiativePending, 0)
	var margin application.Initiative
	for _, it := range list {
		if it.SignalKey == "art-1:s1!b5" {
			margin = it
		}
	}
	if margin.Params["change"] != "7.0" || margin.Params["direction"] != "down" || margin.Params["unit"] != "points" ||
		margin.Params["previous"] != "31.0%" || margin.Params["current"] != "24.0%" || !strings.Contains(margin.Title, "bajó 7.0 puntos") {
		t.Fatalf("margin suggestion = %+v", margin)
	}
	if !strings.Contains(margin.Prompt, "No modifiques") {
		t.Errorf("the request must ask for an analysis only: %q", margin.Prompt)
	}
	// Second scan: same keys inside the window -> duplicates, nothing new.
	for _, r := range e.scan(t) {
		if r.Raised || r.Reason != "duplicate" {
			t.Errorf("rescan = %+v", r)
		}
	}
	// Accepting is the normal request path; nothing ran before.
	if len(e.spy.texts) != 0 {
		t.Fatalf("something was submitted before acceptance: %v", e.spy.texts)
	}
	if _, err := e.svc.Accept(context.Background(), margin.ID); err != nil || len(e.spy.texts) != 1 || e.spy.texts[0] != margin.Prompt {
		t.Fatalf("accept = %v %v", err, e.spy.texts)
	}
}

func TestScanSpendPace(t *testing.T) {
	e := newScan(t)
	cap := 30.0
	e.sc.OrgCap = func(context.Context) float64 { return cap }
	// 19.5 days into a 31-day month: US$ 15 spent -> projected 23.85. Before the month: US$ 10 lifetime.
	e.core.usage = []domain.UsageEntry{
		{CostUSD: 10, TS: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)},
		{CostUSD: 15, TS: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)},
	}
	e.core.cost = 25
	// Available this month: 30 - 10 = 20; projected 23.85 = 119 % -> raised to accounting.
	res := e.scan(t)
	if len(res) != 1 || !res[0].Raised || res[0].Signal != application.SignalSpendPace || res[0].AgentID != "accounting" || res[0].SignalKey != "org:2026-10" {
		t.Fatalf("spend pace = %+v", res)
	}
	list, _ := e.svc.List(context.Background(), application.InitiativePending, 0)
	if p := list[0].Params; p["spent"] != "15.00" || p["projected"] != "23.85" || p["available"] != "20.00" || p["pace"] != "119" {
		t.Fatalf("params = %+v", p)
	}

	// Below the pace, no cap, or too early in the month: nothing.
	e2 := newScan(t)
	e2.sc.OrgCap = func(context.Context) float64 { return 100 }
	e2.core.usage, e2.core.cost = e.core.usage, 25
	if res := e2.scan(t); len(res) != 0 {
		t.Errorf("under pace: %+v", res)
	}
	e2.sc.OrgCap = func(context.Context) float64 { return 0 }
	if res := e2.scan(t); len(res) != 0 {
		t.Errorf("no cap: %+v", res)
	}
	e3 := newScan(t)
	*e3.now = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	e3.sc.OrgCap = func(context.Context) float64 { return 1 }
	e3.core.usage = []domain.UsageEntry{{CostUSD: 0.9, TS: time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)}}
	e3.core.cost = 0.9
	if res := e3.scan(t); len(res) != 0 {
		t.Errorf("before min_days: %+v", res)
	}
}

func TestScanApprovalAgingAndQualityDrop(t *testing.T) {
	e := newScan(t)
	now := *e.now
	e.core.approvals = []domain.Approval{
		{ID: "ap-new", AgentID: "sales", Title: "Reciente", Status: domain.ApprovalPending, CreatedAt: now.Add(-2 * time.Hour)},
		{ID: "ap-old", AgentID: "legal", Title: "Contrato Acme", Risk: "high", Status: domain.ApprovalPending, CreatedAt: now.Add(-50 * time.Hour)},
		{ID: "ap-mid", AgentID: "sales", Title: "Propuesta", Risk: "medium", Status: domain.ApprovalPending, CreatedAt: now.Add(-30 * time.Hour)},
	}
	e.sc.Quality = func(context.Context) ([]application.QualityChange, error) {
		return []application.QualityChange{{AgentID: "sales", Score: 70, Previous: 84}, {AgentID: "legal", Score: 88, Previous: 92}}, nil
	}
	res := e.scan(t)
	if len(res) != 2 {
		t.Fatalf("results = %+v", res)
	}
	q, a := res[0], res[1]
	if q.Signal != application.SignalQualityDrop || q.AgentID != "sales" || !q.Raised || q.SignalKey != "sales" {
		t.Errorf("quality = %+v", q)
	}
	if a.Signal != application.SignalApprovalAging || a.AgentID != "assistant" || !a.Raised || a.SignalKey != "ap-old" {
		t.Errorf("approvals = %+v", a)
	}
	list, _ := e.svc.List(context.Background(), application.InitiativePending, 0)
	for _, it := range list {
		if it.Signal == application.SignalApprovalAging {
			if it.Params["count"] != "2" || it.Params["oldest"] != "Contrato Acme" || !strings.Contains(it.Prompt, "No apruebes ni rechaces") ||
				!strings.Contains(it.Prompt, "«Contrato Acme» (high, 50 h)") || strings.Contains(it.Prompt, "Reciente") {
				t.Errorf("approval suggestion = %+v", it)
			}
		}
	}
	// min_count above what is waiting -> nothing.
	sig := application.DefaultSignalSettings()
	sig.ApprovalAging.MinCount, sig.QualityDrop.Enabled = 4, false
	if _, err := e.svc.PutSettings(context.Background(), application.InitiativeSettings{Enabled: true, DailyLimit: 10, MonthlyLimit: 30, Signals: &sig}); err != nil {
		t.Fatal(err)
	}
	*e.now = now.Add(48 * time.Hour) // outside every dedupe window of the approvals
	if res := e.scan(t); len(res) != 0 {
		t.Errorf("min_count / disabled quality: %+v", res)
	}
}

func TestScanRespectsBudgetControlsAndTheMainSwitch(t *testing.T) {
	e := newScan(t)
	e.sc.Quality = func(context.Context) ([]application.QualityChange, error) {
		return []application.QualityChange{{AgentID: "analyst", Score: 50, Previous: 80}, {AgentID: "sales", Score: 50, Previous: 80}}, nil
	}
	if _, err := e.svc.PutSettings(context.Background(), application.InitiativeSettings{Enabled: true, DailyLimit: 1, MonthlyLimit: 30}); err != nil {
		t.Fatal(err)
	}
	// The paused agent (controls) is skipped without spending budget; the next one is raised; the budget stops a third.
	e.sc.Allowed = func(_ context.Context, _, agentID string) bool { return agentID != "analyst" }
	res := e.scan(t)
	if len(res) != 2 || res[0].Reason != "controls" || res[0].Raised || !res[1].Raised {
		t.Fatalf("controls = %+v", res)
	}
	e.sc.Allowed = nil
	if res := e.scan(t); res[0].Reason != "daily_limit" {
		t.Fatalf("daily budget = %+v", res)
	}
	// Initiative off: nothing is even evaluated.
	if _, err := e.svc.PutSettings(context.Background(), application.InitiativeSettings{Enabled: false, DailyLimit: 5, MonthlyLimit: 30}); err != nil {
		t.Fatal(err)
	}
	if res := e.scan(t); len(res) != 0 {
		t.Fatalf("disabled = %+v", res)
	}
}

func TestScanLocaleAndUnknownAgent(t *testing.T) {
	e := newScan(t)
	e.sc.Locale = func(context.Context) string { return "en-US" }
	e.sc.Quality = func(context.Context) ([]application.QualityChange, error) {
		return []application.QualityChange{{AgentID: "ghost", Score: 10, Previous: 90}, {AgentID: "legal", Score: 60, Previous: 80}}, nil
	}
	res := e.scan(t)
	if len(res) != 2 || res[0].Reason != "no_agent" || !res[1].Raised {
		t.Fatalf("results = %+v", res)
	}
	list, _ := e.svc.List(context.Background(), application.InitiativePending, 0)
	if len(list) != 1 || !strings.HasSuffix(list[0].Title, "quality score dropped 20 points") || !strings.Contains(list[0].Prompt, "do not change settings") {
		t.Fatalf("english text = %+v", list)
	}
}
