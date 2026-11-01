package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/notify"
	"aiworkforce/backend/internal/roles"
)

// planEnv is an API with plans on: every organization starts on Free.
func planEnv(t *testing.T, selfServe bool) *env {
	t.Helper()
	cfg := application.DefaultConfig()
	cfg.IdleDelay = 0
	mem := memory.New()
	if err := mem.Seed(context.Background(), domain.SeedOrg(cfg.BudgetUSD), roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := events.NewHub(log)
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: mem, Pub: events.LocalBus{Hub: hub}, Log: log}
	appr := application.NewApprovals(cfg, mem, rec)
	q := &application.Queries{Store: mem, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	orch := application.NewOrchestrator(ctx, cfg, mem, fakeRuntime{}, memory.NewLocker(), rec, appr, q, log)
	plans := &notify.Plans{Store: notify.NewMemPlans(), DefaultFreeCapUSD: 1}
	oc := &application.OrgConfig{Cfg: cfg, Store: mem, Core: mem, Orch: orch, Rec: rec, Log: log, PlanLimits: plans.Limits}
	d := api.Deps{Cfg: cfg, Queries: q, Orch: orch, Approvals: appr, Store: mem, Runtime: fakeRuntime{}, Hub: hub, Log: log,
		OrgConfig: oc, Rec: rec, Notify: &notify.Service{Plans: plans}, PlanSelfServe: selfServe}
	ts := httptest.NewServer(api.NewRouter(d))
	t.Cleanup(ts.Close)
	return &env{t: t, srv: ts, store: mem, hub: hub}
}

type planBody struct {
	Plan   string `json:"plan"`
	Limits struct {
		MaxSchedules int `json:"max_schedules"`
	} `json:"limits"`
	Used struct {
		Agents    int `json:"agents"`
		Schedules int `json:"schedules"`
	} `json:"used"`
	Usage struct {
		CapUSD float64 `json:"cap_usd"`
		Hard   bool    `json:"hard"`
	} `json:"usage"`
	Catalog   []map[string]any `json:"catalog"`
	SelfServe bool             `json:"self_serve"`
}

func TestPlanCatalogLimitsAndSelfServeUpgrade(t *testing.T) {
	e := planEnv(t, true)
	resp, raw := e.do("GET", "/api/v1/plans", "", nil, nil)
	if resp.StatusCode != 200 || len(decodeInto[struct {
		Plans []map[string]any `json:"plans"`
	}](t, raw).Plans) != 3 {
		t.Fatalf("catalog %d: %s", resp.StatusCode, raw)
	}
	resp, raw = e.do("GET", "/api/v1/plan", "", nil, nil)
	p := decodeInto[planBody](t, raw)
	if resp.StatusCode != 200 || p.Plan != "free" || p.Limits.MaxSchedules != 2 || p.Used.Agents != len(roles.SeedAgents()) ||
		p.Used.Schedules != 0 || !p.Usage.Hard || p.Usage.CapUSD != 1 || len(p.Catalog) != 3 || !p.SelfServe {
		t.Fatalf("plan %d: %s", resp.StatusCode, raw)
	}
	sched := map[string]any{"template_key": "daily_briefing", "hour": 9}
	for i := 0; i < 2; i++ {
		if resp, raw := e.do("POST", "/api/v1/schedules", "", sched, nil); resp.StatusCode != 201 && resp.StatusCode != 200 {
			t.Fatalf("schedule %d: %d %s", i, resp.StatusCode, raw)
		}
	}
	resp, raw = e.do("POST", "/api/v1/schedules", "", sched, nil)
	lim := decodeInto[map[string]any](t, raw)
	if resp.StatusCode != 409 || lim["code"] != "plan_limit" || lim["limit_kind"] != "schedules" || lim["plan"] != "free" {
		t.Fatalf("third schedule on free: %d %s", resp.StatusCode, raw)
	}
	if resp, raw := e.do("PUT", "/api/v1/plan", "", map[string]any{"plan": "ultra"}, nil); resp.StatusCode != 400 {
		t.Fatalf("unknown plan: %d %s", resp.StatusCode, raw)
	}
	resp, raw = e.do("PUT", "/api/v1/plan", "", map[string]any{"plan": "pro"}, nil)
	p = decodeInto[planBody](t, raw)
	if resp.StatusCode != 200 || p.Plan != "pro" || p.Usage.CapUSD != 12 || p.Used.Schedules != 2 {
		t.Fatalf("upgrade %d: %s", resp.StatusCode, raw)
	}
	if resp, raw := e.do("POST", "/api/v1/schedules", "", sched, nil); resp.StatusCode != 201 && resp.StatusCode != 200 {
		t.Fatalf("schedule on pro: %d %s", resp.StatusCode, raw)
	}
}

func TestPlanChangeIsOffWithoutSelfServe(t *testing.T) {
	e := planEnv(t, false)
	if resp, raw := e.do("PUT", "/api/v1/plan", "", map[string]any{"plan": "max"}, nil); resp.StatusCode != 403 {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	_, raw := e.do("GET", "/api/v1/plan", "", nil, nil)
	if p := decodeInto[planBody](t, raw); p.Plan != "free" || p.SelfServe {
		t.Fatalf("%s", raw)
	}
}
