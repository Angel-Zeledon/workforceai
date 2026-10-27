package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/integrations"
	"aiworkforce/backend/internal/roles"
)

// v1 (VENDOR_INTEGRATIONS off): vendor connectors are absent from the catalog and cannot be created; a connection
// created before the switch is kept (listed, flagged) but cannot be tested, granted or used; the generic API source
// keeps working.
func TestVendorConnectorsOffHideAndRefuse(t *testing.T) {
	e := newConnEnv(t, false, true)
	code, c := e.createSim("", "before the switch", "mail.read")
	if code != 201 {
		t.Fatalf("create while on = %d %v", code, c)
	}
	id := c["id"].(string)

	off := integrations.Policy{}
	e.conns.Config.ProviderEnabled = off.Enabled

	_, raw := e.do("GET", "/api/v1/connection-providers", "", nil, nil)
	var cat struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &cat); err != nil || len(cat.Items) == 0 {
		var arr []struct {
			ID string `json:"id"`
		}
		if err2 := json.Unmarshal(raw, &arr); err2 != nil {
			t.Fatalf("catalog: %s", raw)
		}
		cat.Items = arr
	}
	if len(cat.Items) != 1 || cat.Items[0].ID != "custom_api" {
		t.Fatalf("catalog must only offer the generic API source: %s", raw)
	}

	if code, c := e.createSim("", "after", "mail.read"); code != 403 || c["code"] != integrations.CodeDisabled {
		t.Fatalf("create vendor while off = %d %v", code, c)
	}
	resp, raw := e.do("GET", "/api/v1/connections/"+id, "", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"provider_disabled":true`) {
		t.Fatalf("existing connection must be kept and flagged: %d %s", resp.StatusCode, raw)
	}
	_, raw = e.do("GET", "/api/v1/connections", "", nil, nil)
	if !strings.Contains(string(raw), id) {
		t.Fatalf("existing connection must still be listed: %s", raw)
	}
	if resp, raw := e.do("POST", "/api/v1/connections/"+id+"/test", "", nil, nil); resp.StatusCode != 403 || !strings.Contains(string(raw), integrations.CodeDisabled) {
		t.Fatalf("test while off = %d %s", resp.StatusCode, raw)
	}
	if resp, raw := e.do("PUT", "/api/v1/connections/"+id+"/grants/sales", "", map[string]any{"capabilities": []string{"mail.read"}}, nil); resp.StatusCode != 403 {
		t.Fatalf("grant while off = %d %s", resp.StatusCode, raw)
	}
	cn, err := e.conns.Get(context.Background(), e.org, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.conns.Executor(cn); err == nil {
		t.Fatal("a disabled provider must not produce an executor")
	}
	// The generic API source is never gated.
	if resp, raw := e.do("POST", "/api/v1/connections", "", map[string]any{"provider": "custom_api", "kind": "api_key", "label": "ERP", "secret": "abcdefghijklmnop"}, nil); resp.StatusCode != 201 {
		t.Fatalf("custom_api while off = %d %s", resp.StatusCode, raw)
	}
	// Admins can still clean up: revoke works on a disabled provider.
	if resp, raw := e.do("POST", "/api/v1/connections/"+id+"/revoke", "", map[string]any{"confirm_name": "before the switch"}, nil); resp.StatusCode != 200 {
		t.Fatalf("revoke while off = %d %s", resp.StatusCode, raw)
	}
}

// v1: the Slack channel routes are not mounted (request URLs 404) and the settings say "disabled"; GET /org/settings
// reports the switch so the UI hides the vendor cards.
func TestSlackChannelOffAndSettingsReportState(t *testing.T) {
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
	off := integrations.Policy{}
	d := api.Deps{Cfg: cfg, Queries: q, Orch: orch, Approvals: appr, Store: mem, Runtime: fakeRuntime{}, Hub: hub, Log: log,
		OrgConfig: oc, VendorIntegrations: &off}
	ts := httptest.NewServer(api.NewRouter(d))
	t.Cleanup(ts.Close)
	e := &env{t: t, srv: ts, store: mem, hub: hub}

	for _, p := range []string{"/api/v1/channels/slack/" + cfg.OrgID + "/interactions", "/api/v1/channels/slack/" + cfg.OrgID + "/commands"} {
		if c := e.status("POST", p, "", map[string]any{}); c != 404 {
			t.Fatalf("POST %s = %d, want 404", p, c)
		}
	}
	resp, raw := e.do("GET", "/api/v1/channels/slack/settings", "", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"available":false`) || !strings.Contains(string(raw), integrations.CodeDisabled) {
		t.Fatalf("settings while off = %d %s", resp.StatusCode, raw)
	}
	for _, r := range [][2]string{{"PUT", "/api/v1/channels/slack/settings"}, {"POST", "/api/v1/channels/slack/test"}, {"POST", "/api/v1/channels/slack/link-code"}} {
		if resp, raw := e.do(r[0], r[1], "", map[string]any{}, nil); resp.StatusCode != 403 || !strings.Contains(string(raw), integrations.CodeDisabled) {
			t.Fatalf("%s %s = %d %s", r[0], r[1], resp.StatusCode, raw)
		}
	}
	_, raw = e.do("GET", "/api/v1/org/settings", "", nil, nil)
	var st struct {
		Vendor integrations.State `json:"vendor_integrations"`
	}
	if err := json.Unmarshal(raw, &st); err != nil || st.Vendor.Enabled || len(st.Vendor.Providers) != 0 {
		t.Fatalf("org settings must report vendor integrations off: %s", raw)
	}
}
