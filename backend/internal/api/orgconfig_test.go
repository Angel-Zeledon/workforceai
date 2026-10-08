package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/roles"
)

// cfgEnv is a minimal API environment with OrgConfig mounted.
func cfgEnv(t *testing.T, withAuth bool) *env {
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
	oc := &application.OrgConfig{Cfg: cfg, Store: mem, Core: mem, Orch: orch, Rec: rec, Log: log}
	orch.SetStyle(oc)
	d := api.Deps{Cfg: cfg, Queries: q, Orch: orch, Approvals: appr, Store: mem, Runtime: fakeRuntime{}, Hub: hub, Log: log,
		OrgConfig: oc, AuthEnabled: withAuth}
	var svc *auth.Service
	if withAuth {
		var err error
		svc, err = auth.NewService(auth.Config{Store: auth.NewMemoryStore(), Secret: []byte("0123456789abcdef0123456789abcdef-test"),
			PasswordParams: auth.PasswordParams{Memory: 8, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
		if err != nil {
			t.Fatal(err)
		}
		d.Auth = svc
		d.AuthRoutes = auth.NewHandler(svc, auth.HandlerConfig{}).Routes()
	}
	ts := httptest.NewServer(api.NewRouter(d))
	t.Cleanup(ts.Close)
	return &env{t: t, srv: ts, svc: svc, store: mem, hub: hub}
}

func decodeInto[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

func TestWorkflowTemplateGallery(t *testing.T) {
	e := cfgEnv(t, false)
	resp, raw := e.do("GET", "/api/v1/workflow-templates", "", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	type step struct {
		Key       string   `json:"key"`
		Title     string   `json:"title"`
		AgentID   string   `json:"agent_id"`
		DependsOn []string `json:"depends_on"`
		Stage     int      `json:"stage"`
	}
	type tpl struct {
		Key           string `json:"key"`
		Name          string `json:"name"`
		NameKey       string `json:"name_key"`
		ParallelSteps int    `json:"parallel_steps"`
		Steps         []step `json:"steps"`
	}
	es := decodeInto[[]tpl](t, raw)
	byKey := map[string]tpl{}
	for _, x := range es {
		byKey[x.Key] = x
	}
	for _, k := range []string{"new_client", "collections", "proposal", "month_close"} {
		if byKey[k].Name == "" || byKey[k].NameKey == "" || len(byKey[k].Steps) == 0 {
			t.Fatalf("template %s incomplete: %+v", k, byKey[k])
		}
	}
	if byKey["month_close"].Name != "Cierre de mes" || byKey["month_close"].ParallelSteps < 2 {
		t.Fatalf("month_close = %+v", byKey["month_close"])
	}
	_, raw = e.do("GET", "/api/v1/workflow-templates?locale=en", "", nil, nil)
	en := decodeInto[[]tpl](t, raw)
	for _, x := range en {
		if x.Key == "month_close" && x.Name != "Month-end close" {
			t.Fatalf("en name = %q", x.Name)
		}
	}
	if st := e.status("GET", "/api/v1/workflow-templates/ghost", "", nil); st != 404 {
		t.Fatalf("unknown template status = %d", st)
	}
}

func TestInstantiateTemplateOverHTTP(t *testing.T) {
	e := cfgEnv(t, false)
	resp, raw := e.do("POST", "/api/v1/workflow-templates/new_client/instantiate", "", map[string]any{"params": map[string]string{"client": "Grupo Alfa"}}, nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	id := decodeInto[map[string]string](t, raw)["request_id"]
	if id == "" {
		t.Fatal("no request id")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, raw := e.do("GET", "/api/v1/requests/"+id, "", nil, nil)
		var d struct {
			Status domain.RequestStatus `json:"status"`
			Tasks  []domain.Task        `json:"tasks"`
		}
		_ = json.Unmarshal(raw, &d)
		if d.Status == domain.RequestDone {
			if len(d.Tasks) != 5 {
				t.Fatalf("tasks = %d", len(d.Tasks))
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("request did not finish: %s", raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st := e.status("POST", "/api/v1/workflow-templates/new_client/instantiate", "", map[string]any{"params": map[string]string{}}); st != 400 {
		t.Fatalf("missing param status = %d", st)
	}
	if st := e.status("POST", "/api/v1/workflow-templates/nope/instantiate", "", map[string]any{}); st != 404 {
		t.Fatalf("unknown template status = %d", st)
	}
}

func TestOnboardingFlowOverHTTP(t *testing.T) {
	e := cfgEnv(t, false)
	_, raw := e.do("GET", "/api/v1/onboarding", "", nil, nil)
	st := decodeInto[map[string]any](t, raw)
	if st["onboarding_completed"] != false || st["locale"] != "es" || st["tone"] != "neutral" {
		t.Fatalf("fresh org status = %v", st)
	}
	if tones, _ := st["available_tones"].([]any); len(tones) != 6 {
		t.Fatalf("available_tones = %v", st["available_tones"])
	}
	_, raw = e.do("GET", "/api/v1/onboarding/packs", "", nil, nil)
	if packs := decodeInto[[]map[string]any](t, raw); len(packs) < 3 {
		t.Fatalf("packs = %d", len(packs))
	}
	body := map[string]any{"pack_key": "agency", "tone": "co", "briefing": map[string]any{"enabled": true, "hour": 8, "minute": 0, "timezone": "America/Bogota"}}
	resp, raw := e.do("POST", "/api/v1/onboarding", "", body, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("onboard status %d: %s", resp.StatusCode, raw)
	}
	res := decodeInto[map[string]any](t, raw)
	if res["schedule"] == nil || res["memory_seeded"].(float64) < 1 {
		t.Fatalf("onboard result = %v", res)
	}
	if code := e.status("POST", "/api/v1/onboarding", "", body); code != http.StatusConflict {
		t.Fatalf("second onboarding status = %d", code)
	}
	if code := e.status("POST", "/api/v1/onboarding", "", map[string]any{"pack_key": "ghost", "force": true}); code != 404 {
		t.Fatalf("unknown pack status = %d", code)
	}
	_, raw = e.do("GET", "/api/v1/schedules", "", nil, nil)
	if sc := decodeInto[[]map[string]any](t, raw); len(sc) != 1 || sc[0]["template_key"] != "daily_briefing" {
		t.Fatalf("schedules = %s", raw)
	}
	// Agent autonomy from the pack is visible through the regular agents API.
	_, raw = e.do("GET", "/api/v1/agents/analyst", "", nil, nil)
	if a := decodeInto[domain.Agent](t, raw); a.Autonomy != "autonomous" {
		t.Fatalf("analyst autonomy = %q", a.Autonomy)
	}
}

func TestToneEndpointsValidate(t *testing.T) {
	e := cfgEnv(t, false)
	if code := e.status("PUT", "/api/v1/org/settings", "", map[string]any{"tone": "pirate"}); code != 400 {
		t.Fatalf("invalid tone status = %d", code)
	}
	if code := e.status("PUT", "/api/v1/org/settings", "", map[string]any{"locale": "fr"}); code != 400 {
		t.Fatalf("invalid locale status = %d", code)
	}
	resp, raw := e.do("PUT", "/api/v1/org/settings", "", map[string]any{"tone": "ar", "locale": "en"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if st := decodeInto[map[string]any](t, raw); st["tone"] != "ar" || st["locale"] != "en" || st["configured"] != true {
		t.Fatalf("settings = %v", st)
	}
	resp, raw = e.do("PUT", "/api/v1/agents/sales/tone", "", map[string]any{"tone": "mx"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("agent tone status %d: %s", resp.StatusCode, raw)
	}
	if st := decodeInto[map[string]any](t, raw); st["agent_tones"].(map[string]any)["sales"] != "mx" {
		t.Fatalf("agent_tones = %v", st["agent_tones"])
	}
	if code := e.status("PUT", "/api/v1/agents/ghost/tone", "", map[string]any{"tone": "mx"}); code != 404 {
		t.Fatalf("unknown agent status = %d", code)
	}
}

func TestSchedulesOverHTTP(t *testing.T) {
	e := cfgEnv(t, false)
	resp, raw := e.do("POST", "/api/v1/schedules", "", map[string]any{"hour": 8, "minute": 0, "timezone": "America/Santiago", "weekdays": []int{1, 2, 3, 4, 5}}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %s", resp.StatusCode, raw)
	}
	id := decodeInto[map[string]any](t, raw)["id"].(string)
	if code := e.status("POST", "/api/v1/schedules", "", map[string]any{"hour": 99, "timezone": "UTC"}); code != 400 {
		t.Fatalf("invalid hour status = %d", code)
	}
	if code := e.status("PUT", "/api/v1/schedules/"+id, "", map[string]any{"hour": 7, "minute": 30, "timezone": "UTC", "enabled": false}); code != 200 {
		t.Fatalf("update status = %d", code)
	}
	if code := e.status("DELETE", "/api/v1/schedules/"+id, "", nil); code != http.StatusNoContent {
		t.Fatalf("delete status = %d", code)
	}
	if code := e.status("DELETE", "/api/v1/schedules/"+id, "", nil); code != 404 {
		t.Fatalf("second delete status = %d", code)
	}
}

func TestTaxTemplatesAreExamples(t *testing.T) {
	e := cfgEnv(t, false)
	_, raw := e.do("GET", "/api/v1/tax-templates?country=MX&locale=en", "", nil, nil)
	got := decodeInto[[]map[string]any](t, raw)
	if len(got) != 1 || got[0]["example"] != true || got[0]["country"] != "MX" {
		t.Fatalf("tax templates = %s", raw)
	}
	if d, _ := got[0]["disclaimer"].(string); d == "" {
		t.Fatal("missing disclaimer")
	}
}

func TestOrgConfigRoutesRequireAuthAndPermissions(t *testing.T) {
	e := cfgEnv(t, true)
	for _, p := range []string{"/api/v1/workflow-templates", "/api/v1/onboarding", "/api/v1/schedules", "/api/v1/org/settings"} {
		if code := e.status("GET", p, "", nil); code != http.StatusUnauthorized {
			t.Fatalf("GET %s without token = %d", p, code)
		}
	}
	owner := e.register("owner@example.com", "acme")
	if code := e.status("GET", "/api/v1/workflow-templates", owner.AccessToken, nil); code != 200 {
		t.Fatalf("owner gallery status = %d", code)
	}
	if code := e.status("POST", "/api/v1/onboarding", owner.AccessToken, map[string]any{"pack_key": "general"}); code != 200 {
		t.Fatalf("owner onboarding status = %d", code)
	}
}
