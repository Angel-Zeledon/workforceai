package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/connections/gmail"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/gateway"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/sanitize"
	"aiworkforce/backend/internal/vault"
)

const canary = "CANARY_SECRET_9f2c_abcdefghijklmnop"

type connEnv struct {
	*env
	conns *connections.Service
	ctl   *controls.Service
	gw    *gateway.Gateway
	gm    *gmail.Provider
	org   string
	logs  *bytes.Buffer
}

// newConnEnv mounts the connections/controls routes over in-memory stores.
// withKEK=false mimics a deployment without CONNECTIONS_KEK.
func newConnEnv(t *testing.T, withAuth, withKEK bool) *connEnv {
	t.Helper()
	cfg := application.DefaultConfig()
	cfg.IdleDelay = 0
	mem := memory.New()
	if err := mem.Seed(context.Background(), domain.SeedOrg(cfg.BudgetUSD), domain.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	hub := events.NewHub(log)
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: mem, Pub: events.LocalBus{Hub: hub}, Log: log}
	appr := application.NewApprovals(cfg, mem, rec)
	q := &application.Queries{Store: mem, Cfg: cfg}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	orch := application.NewOrchestrator(ctx, cfg, mem, fakeRuntime{}, memory.NewLocker(), rec, appr, q, log)

	var kw vault.KeyWrapper
	if withKEK {
		w, _ := vault.NewEnvKeyWrapper(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
		kw = w
	}
	sus := sanitize.NewSuspects()
	v := vault.New(vault.NewMemRepo(), kw)
	v.Suspects = sus
	gm, _ := gmail.New(gmail.Config{})
	astore := auth.NewMemoryStore()
	audit := func(c context.Context, org, actor, action, entity, id string, d map[string]any) {
		rec.Audit(application.WithOrg(c, org), domain.AuditLog{Actor: actor, Action: action, Entity: entity, EntityID: id, Details: d})
	}
	emit := func(c context.Context, org, typ string, p map[string]any) {
		rec.Emit(application.WithOrg(c, org), application.Action{Type: typ, Entity: "connection", Payload: p, SkipAudit: true})
	}
	cs, err := connections.NewService(connections.Config{Store: connections.NewMemStore(), Vault: v, Suspects: sus,
		Providers: map[string]connections.Provider{"google_gmail": gm}, Audit: audit, Emit: emit,
		AdminCount: func(c context.Context, org string) int {
			ms, _ := astore.ListMembers(c, org)
			n := 0
			for _, m := range ms {
				if m.Role == auth.RoleOwner || m.Role == auth.RoleAdmin {
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			return n
		}})
	if err != nil {
		t.Fatal(err)
	}
	ctl := controls.New(controls.NewMemStore(), controls.AuditFunc(audit), controls.EmitFunc(emit))
	gw := gateway.New(cs, ctl, sus, log)
	gw.Audit, gw.Emit = audit, emit
	cs.OnRevoke = func(c context.Context, org, id, reason string) { gw.CancelHoldsFor(c, org, id, reason) }
	ctl.Hooks = controls.Hooks{OnKillSwitch: gw.OnKillSwitch, OnRelease: gw.OnRelease}
	orch.SetConnections(gateway.Guard{G: gw}, gw, gw)

	d := api.Deps{Cfg: cfg, Queries: q, Orch: orch, Approvals: appr, Store: mem, Runtime: fakeRuntime{}, Hub: hub, Log: log,
		Conns: cs, Controls: ctl, Gateway: gw, AuthEnabled: withAuth}
	var svc *auth.Service
	if withAuth {
		svc, err = auth.NewService(auth.Config{Store: astore, Secret: []byte("0123456789abcdef0123456789abcdef-test"),
			PasswordParams: auth.PasswordParams{Memory: 8, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}})
		if err != nil {
			t.Fatal(err)
		}
		d.Auth = svc
		d.AuthRoutes = auth.NewHandler(svc, auth.HandlerConfig{}).Routes()
	}
	ts := httptest.NewServer(api.NewRouter(d))
	t.Cleanup(ts.Close)
	org := cfg.OrgID
	return &connEnv{env: &env{t: t, srv: ts, svc: svc, store: mem, hub: hub, appr: appr}, conns: cs, ctl: ctl, gw: gw, gm: gm, org: org, logs: logs}
}

func (e *connEnv) createSim(token, label string, caps ...string) (int, map[string]any) {
	e.t.Helper()
	resp, raw := e.do("POST", "/api/v1/connections", token, map[string]any{"provider": "google_gmail", "label": label, "capabilities": caps, "mode": "simulated"}, nil)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m
}

func TestConnectionsAPINeverReturnsSecrets(t *testing.T) {
	e := newConnEnv(t, false, true)
	var seen []string
	collect := func(method, path string, body any) {
		resp, raw := e.do(method, path, "", body, nil)
		if resp.StatusCode >= 500 {
			t.Fatalf("%s %s = %d %s", method, path, resp.StatusCode, raw)
		}
		seen = append(seen, string(raw))
	}
	resp, raw := e.do("POST", "/api/v1/connections", "", map[string]any{"provider": "custom_api", "kind": "api_key", "label": "ERP", "secret": canary}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %s", resp.StatusCode, raw)
	}
	seen = append(seen, string(raw))
	c := decodeInto[map[string]any](t, raw)
	id := c["id"].(string)
	cred := c["credential"].(map[string]any)
	if cred["hint"] != canary[len(canary)-4:] || cred["version"].(float64) != 1 {
		t.Fatalf("credential metadata: %v", cred)
	}
	collect("PUT", "/api/v1/connections/"+id+"/credential", map[string]any{"secret": "CANARY_SECRET_ROTATED_abcdefghijk"})
	for _, p := range []string{"/api/v1/connections", "/api/v1/connections/" + id, "/api/v1/connections/" + id + "/grants",
		"/api/v1/connections/" + id + "/usage", "/api/v1/connections/" + id + "/usage/summary", "/api/v1/connections/" + id + "/limits",
		"/api/v1/connection-providers", "/api/v1/org/controls", "/api/v1/approvals", "/api/v1/activity", "/api/v1/agents/assistant/connections"} {
		collect("GET", p, nil)
	}
	collect("POST", "/api/v1/connections/"+id+"/test", nil)
	collect("PATCH", "/api/v1/connections/"+id, map[string]any{"label": "ERP 2"})
	// A secret in the wrong place must not be echoed back either.
	collect("POST", "/api/v1/connections", map[string]any{"provider": "custom_api", "kind": "api_key", "label": "bad", "secret": "CANARY_SECRET_bad format!!"})
	all := strings.Join(seen, "\n") + e.logs.String()
	if strings.Contains(all, "CANARY_SECRET") {
		t.Fatalf("a secret leaked through the API or the logs:\n%s", all)
	}
	// ...nor through the event stream / audit trail of the store.
	acts, _ := e.store.ListActivity(context.Background(), e.org, "", 1000)
	b, _ := json.Marshal(acts)
	if strings.Contains(string(b), "CANARY_SECRET") {
		t.Fatal("secret in the activity feed")
	}
}

func TestLiveConnectionWithoutKEKFailsClosedButSimulationWorks(t *testing.T) {
	e := newConnEnv(t, false, false)
	resp, raw := e.do("POST", "/api/v1/connections", "", map[string]any{"provider": "google_gmail", "label": "live", "capabilities": []string{"mail.read"}}, nil)
	if resp.StatusCode != 503 || !strings.Contains(string(raw), "kek_missing") {
		t.Fatalf("live without KEK = %d %s", resp.StatusCode, raw)
	}
	resp, raw = e.do("POST", "/api/v1/connections", "", map[string]any{"provider": "custom_api", "kind": "api_key", "label": "k", "secret": "abcdefghijklmnop"}, nil)
	if resp.StatusCode != 503 {
		t.Fatalf("api key without KEK = %d %s", resp.StatusCode, raw)
	}
	if code, c := e.createSim("", "sim", "mail.read"); code != 201 || c["status"] != "active" || c["mode"] != "simulated" {
		t.Fatalf("simulated: %d %v", code, c)
	}
	_, raw = e.do("GET", "/api/v1/connection-providers", "", nil, nil)
	if strings.Contains(string(raw), `"live_available":true`) {
		t.Fatalf("providers claim live availability without KEK: %s", raw)
	}
}

func TestMixedReadWriteRejectedOverAPI(t *testing.T) {
	e := newConnEnv(t, false, true)
	code, c := e.createSim("", "mixed", "mail.read", "mail.send")
	if code != 400 || c["code"] != "read_write_must_be_separate" {
		t.Fatalf("%d %v", code, c)
	}
}

func TestRBACOfConnectionsAndControls(t *testing.T) {
	e := newConnEnv(t, true, true)
	owner := e.register("owner@example.com", "O")
	admin := e.member(owner, "admin@example.com", auth.RoleAdmin)
	member := e.member(owner, "member@example.com", auth.RoleMember)
	viewer := e.member(owner, "viewer@example.com", auth.RoleViewer)

	if c := e.status("GET", "/api/v1/connections", "", nil); c != 401 {
		t.Fatalf("unauthenticated = %d", c)
	}
	if c := e.status("GET", "/api/v1/connections", viewer, nil); c != 403 {
		t.Fatalf("viewer list = %d", c)
	}
	if c := e.status("GET", "/api/v1/connections", member, nil); c != 200 {
		t.Fatalf("member list = %d", c)
	}
	if code, _ := e.createSim(member, "x", "mail.read"); code != 403 {
		t.Fatalf("member create = %d", code)
	}
	code, c := e.createSim(admin, "Gmail lectura", "mail.read")
	if code != 201 {
		t.Fatalf("admin create = %d", code)
	}
	id := c["id"].(string)
	if c := e.status("POST", "/api/v1/connections/"+id+"/suspend", member, nil); c != 403 {
		t.Fatalf("member suspend = %d", c)
	}
	if c := e.status("GET", "/api/v1/connections/"+id+"/usage", member, nil); c != 403 {
		t.Fatalf("member usage = %d", c)
	}

	// Kill switch: admin activates, only the owner lifts it.
	if c := e.status("POST", "/api/v1/org/controls/kill-switch", member, map[string]string{"level": "freeze"}); c != 403 {
		t.Fatalf("member kill switch = %d", c)
	}
	if c := e.status("POST", "/api/v1/org/controls/kill-switch", admin, map[string]string{"level": "freeze", "reason": "test"}); c != 200 {
		t.Fatalf("admin kill switch = %d", c)
	}
	if c := e.status("POST", "/api/v1/org/controls/release", admin, map[string]string{"reason": "oops"}); c != 403 {
		t.Fatalf("admin release = %d: decision 4: only the owner lifts the kill switch", c)
	}
	_, raw := e.do("GET", "/api/v1/org/controls", member, nil, nil)
	if !strings.Contains(string(raw), `"kill_switch_level":"freeze"`) {
		t.Fatalf("controls: %s", raw)
	}
	if c := e.status("POST", "/api/v1/org/controls/release", owner.AccessToken, map[string]string{}); c != 400 {
		t.Fatalf("release without a reason = %d", c)
	}
	if c := e.status("POST", "/api/v1/org/controls/release", owner.AccessToken, map[string]string{"reason": "all clear"}); c != 200 {
		t.Fatalf("owner release = %d", c)
	}
	// read-only: admin turns it on, only the owner turns it off.
	if c := e.status("PUT", "/api/v1/org/controls", admin, map[string]string{"mode": "read_only"}); c != 200 {
		t.Fatalf("admin read-only on = %d", c)
	}
	if c := e.status("PUT", "/api/v1/org/controls", admin, map[string]string{"mode": "normal"}); c != 403 {
		t.Fatalf("admin read-only off = %d", c)
	}
	if c := e.status("PUT", "/api/v1/org/controls", owner.AccessToken, map[string]string{"mode": "normal"}); c != 200 {
		t.Fatalf("owner read-only off = %d", c)
	}
	// plan review can only be disabled by the owner
	if c := e.status("PUT", "/api/v1/org/controls", admin, map[string]any{"settings": map[string]string{"plan_review": "never"}}); c != 403 {
		t.Fatalf("admin disables plan review = %d", c)
	}
	// per-tool kill switch: admin disables, only the owner re-enables
	if c := e.status("PUT", "/api/v1/org/controls/tools/email.send", admin, map[string]any{"disabled": true}); c != 200 {
		t.Fatalf("tool off = %d", c)
	}
	if c := e.status("PUT", "/api/v1/org/controls/tools/email.send", admin, map[string]any{"disabled": false, "reason": "x"}); c != 403 {
		t.Fatalf("admin tool on = %d", c)
	}
	if c := e.status("PUT", "/api/v1/org/controls/tools/email.send", owner.AccessToken, map[string]any{"disabled": false, "reason": "back"}); c != 200 {
		t.Fatalf("owner tool on = %d", c)
	}
	// agent pause
	if c := e.status("POST", "/api/v1/agents/sales/control", admin, map[string]string{"action": "pause", "drain": "immediate", "reason": "x"}); c != 200 {
		t.Fatalf("pause = %d", c)
	}
	if c := e.status("POST", "/api/v1/agents/nobody/control", admin, map[string]string{"action": "pause"}); c != 404 {
		t.Fatalf("unknown agent = %d", c)
	}
	if c := e.status("POST", "/api/v1/agents/sales/control", member, map[string]string{"action": "resume"}); c != 403 {
		t.Fatalf("member resume = %d", c)
	}
}

func TestWriteGrantNeedsConfirmationAndSecondAdmin(t *testing.T) {
	e := newConnEnv(t, true, true)
	owner := e.register("owner@example.com", "O")
	admin := e.member(owner, "admin@example.com", auth.RoleAdmin)
	_, c := e.createSim(owner.AccessToken, "Gmail escritura", "mail.draft", "mail.send")
	id := c["id"].(string)
	path := "/api/v1/connections/" + id + "/grants/assistant"

	// no confirmation -> rejected
	resp, raw := e.do("PUT", path, owner.AccessToken, map[string]any{"capabilities": []string{"mail.send"}}, nil)
	if resp.StatusCode != 400 || !strings.Contains(string(raw), "confirmation_required") {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	// agents must exist
	if c := e.status("PUT", "/api/v1/connections/"+id+"/grants/ghost", owner.AccessToken, map[string]any{"capabilities": []string{"mail.draft"}, "confirm_name": "Gmail escritura"}); c != 404 {
		t.Fatalf("unknown agent = %d", c)
	}
	// forbidden capabilities
	if c := e.status("PUT", path, owner.AccessToken, map[string]any{"capabilities": []string{"approve"}}); c != 400 {
		t.Fatalf("approve capability = %d", c)
	}
	// two admins (owner + admin) -> second human required
	resp, raw = e.do("PUT", path, owner.AccessToken, map[string]any{"capabilities": []string{"mail.send"}, "confirm_name": "Gmail escritura"}, nil)
	if resp.StatusCode != http.StatusAccepted || !strings.Contains(string(raw), "pending_second_approval") {
		t.Fatalf("write grant with two admins = %d %s", resp.StatusCode, raw)
	}
	if c := e.status("POST", path+"/approve", owner.AccessToken, nil); c != 409 {
		t.Fatalf("self approval = %d", c)
	}
	resp, raw = e.do("POST", path+"/approve", admin, nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"status":"active"`) {
		t.Fatalf("second approval = %d %s", resp.StatusCode, raw)
	}
	// the default profile is strict
	if !strings.Contains(string(raw), `"redaction_profile":"strict"`) {
		t.Fatalf("default redaction profile: %s", raw)
	}
	// none_admin_only: an admin cannot, the owner can
	if c := e.status("PUT", "/api/v1/connections/"+id+"/grants/sales", admin, map[string]any{"capabilities": []string{"mail.draft"}, "confirm_name": "Gmail escritura", "redaction_profile": "none_admin_only"}); c != 400 {
		t.Fatalf("admin none_admin_only = %d", c)
	}
}

func TestOAuthCallbackIsPublicAndRejectsForgedState(t *testing.T) {
	e := newConnEnv(t, true, true)
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/connections/oauth/callback?code=abc&state=forged", nil)
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "error=invalid_state") {
		t.Fatalf("callback = %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if b, _ := io.ReadAll(resp.Body); strings.Contains(string(b), "forged") {
		t.Fatal("state echoed")
	}
	// OAuth start without an OAuth app configured is a clean 503, never a crash.
	owner := e.register("o@example.com", "O")
	resp2, raw := e.do("POST", "/api/v1/connections", owner.AccessToken, map[string]any{"provider": "google_gmail", "label": "live", "capabilities": []string{"mail.read"}}, nil)
	if resp2.StatusCode != 201 {
		t.Fatalf("%d %s", resp2.StatusCode, raw)
	}
	id := decodeInto[map[string]any](t, raw)["id"].(string)
	if c := e.status("POST", "/api/v1/connections/"+id+"/oauth/start", owner.AccessToken, nil); c != 503 {
		t.Fatalf("oauth start without app = %d", c)
	}
}

func TestKillSwitchStopsConnectionUseAndToolSwitchWorks(t *testing.T) {
	e := newConnEnv(t, false, true)
	_, c := e.createSim("", "Gmail lectura", "mail.read")
	id := c["id"].(string)
	if code := e.status("PUT", "/api/v1/connections/"+id+"/grants/assistant", "", map[string]any{"capabilities": []string{"mail.read"}}); code != 200 {
		t.Fatalf("grant = %d", code)
	}
	call := application.GatewayCall{Org: e.org, AgentID: "assistant", TaskID: "t1", Tool: "email", Action: "search", Autonomy: "rules"}
	if out := e.gw.Execute(context.Background(), call); out.Decision != "allowed" {
		t.Fatalf("%+v", out)
	}
	e.do("PUT", "/api/v1/org/controls/tools/email", "", map[string]any{"disabled": true}, nil)
	if out := e.gw.Execute(context.Background(), call); out.DenyReason != controls.CodeToolDisabled {
		t.Fatalf("per-tool kill switch: %+v", out)
	}
	e.do("PUT", "/api/v1/org/controls/tools/email", "", map[string]any{"disabled": false, "reason": "ok"}, nil)
	e.do("POST", "/api/v1/org/controls/kill-switch", "", map[string]any{"level": "lockdown", "reason": "drill"}, nil)
	_, raw := e.do("GET", "/api/v1/connections/"+id, "", nil, nil)
	if !strings.Contains(string(raw), `"status":"suspended"`) {
		t.Fatalf("lockdown must suspend: %s", raw)
	}
	e.do("POST", "/api/v1/org/controls/release", "", map[string]any{"reason": "done", "resume_connections": "all"}, nil)
	_, raw = e.do("GET", "/api/v1/connections/"+id, "", nil, nil)
	if !strings.Contains(string(raw), `"status":"active"`) {
		t.Fatalf("resume all: %s", raw)
	}
	// usage list paginates and exposes only metadata
	resp, raw := e.do("GET", "/api/v1/connections/"+id+"/usage?limit=1", "", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"next_cursor"`) {
		t.Fatalf("usage = %d %s", resp.StatusCode, raw)
	}
	// revoke needs the exact name; history survives
	if c := e.status("POST", "/api/v1/connections/"+id+"/revoke", "", map[string]string{"confirm_name": "wrong"}); c != 400 {
		t.Fatalf("revoke with wrong name = %d", c)
	}
	if c := e.status("POST", "/api/v1/connections/"+id+"/revoke", "", map[string]string{"confirm_name": "Gmail lectura"}); c != 200 {
		t.Fatalf("revoke = %d", c)
	}
	_, raw = e.do("GET", "/api/v1/connections/"+id+"/usage", "", nil, nil)
	if !strings.Contains(string(raw), `"items":[{`) {
		t.Fatalf("usage history lost after revoke: %s", raw)
	}
}

func TestPendingApprovalExposesConnectionContext(t *testing.T) {
	e := newConnEnv(t, false, true)
	_, c := e.createSim("", "Gmail escritura", "mail.send")
	id := c["id"].(string)
	e.do("PUT", "/api/v1/connections/"+id+"/grants/assistant", "", map[string]any{"capabilities": []string{"mail.send"}, "confirm_name": "Gmail escritura"}, nil)
	call := application.GatewayCall{Org: e.org, AgentID: "assistant", TaskID: "t1", Tool: "email", Action: "send", Autonomy: "autonomous",
		Args: map[string]any{"to": "laura@acme.com", "subject": "Hola", "body": "x"}}
	out := e.gw.Execute(context.Background(), call)
	if out.Pending == nil {
		t.Fatalf("%+v", out)
	}
	ap, _, err := e.appr.Request(context.Background(), domain.Approval{TaskID: "t1", AgentID: "assistant", Action: "send", Risk: "high", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	e.gw.RegisterApproval(ap.ID, *out.Pending)
	_, raw := e.do("GET", "/api/v1/approvals?status=pending", "", nil, nil)
	var list []map[string]any
	_ = json.Unmarshal(raw, &list)
	if len(list) != 1 {
		t.Fatalf("approvals: %s", raw)
	}
	ctx, _ := list[0]["context"].(map[string]any)
	if ctx == nil || ctx["hold_seconds"].(float64) != 60 || ctx["reversibility"] != "none" || ctx["account"] == "" {
		t.Fatalf("approval card context: %v", list[0])
	}
	if strings.Contains(string(raw), "laura@acme.com") == false {
		t.Fatalf("recipient must be visible on the card: %s", raw)
	}
}

func TestOutboxAndControlCenterEndpoints(t *testing.T) {
	e := newConnEnv(t, false, true)
	_, c := e.createSim("", "Gmail escritura", "mail.send")
	id := c["id"].(string)
	if code := e.status("PUT", "/api/v1/connections/"+id+"/grants/assistant", "", map[string]any{"capabilities": []string{"mail.send"}, "confirm_name": "Gmail escritura"}); code != 200 {
		t.Fatalf("grant = %d", code)
	}
	out := e.gw.Execute(context.Background(), application.GatewayCall{Org: e.org, AgentID: "assistant", TaskID: "t1", Tool: "email", Action: "send", Autonomy: "autonomous",
		Args: map[string]any{"to": "laura@acme.com", "subject": "Hola", "body": "Texto"}})
	if out.Pending == nil {
		t.Fatalf("%+v", out)
	}
	resp, raw := e.do("GET", "/api/v1/outbox?status=pending_approval", "", nil, nil)
	var list struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(raw, &list)
	if resp.StatusCode != 200 || len(list.Items) != 1 {
		t.Fatalf("outbox = %d %s", resp.StatusCode, raw)
	}
	it := list.Items[0]
	for _, k := range []string{"id", "agent_id", "connection_id", "account_label", "to", "subject", "body", "status", "block_reason", "origin_external",
		"new_recipient", "reversibility", "hold_seconds", "hold_until", "version", "created_at", "updated_at"} {
		if _, ok := it[k]; !ok {
			t.Fatalf("outbox item lacks %q: %v", k, it)
		}
	}
	itemID := it["id"].(string)
	resp, raw = e.do("PATCH", "/api/v1/outbox/"+itemID, "", map[string]any{"subject": "Hola 2"}, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"version":2`) {
		t.Fatalf("edit = %d %s", resp.StatusCode, raw)
	}
	if code := e.status("POST", "/api/v1/outbox/"+itemID+"/approve", "", map[string]any{"version": 1}); code != 409 {
		t.Fatalf("stale approve = %d", code)
	}
	resp, raw = e.do("PATCH", "/api/v1/outbox/"+itemID, "", map[string]any{"body": "my key AKIAIOSFODNN7EXAMPLE"}, nil)
	if resp.StatusCode != 400 || !strings.Contains(string(raw), "secret_detected") {
		t.Fatalf("secret edit = %d %s", resp.StatusCode, raw)
	}
	resp, raw = e.do("POST", "/api/v1/outbox/"+itemID+"/approve", "", map[string]any{"version": 2}, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"status":"held"`) || !strings.Contains(string(raw), `"hold_until":"`) {
		t.Fatalf("approve = %d %s", resp.StatusCode, raw)
	}
	resp, raw = e.do("POST", "/api/v1/tool-calls/"+itemID+"/cancel-hold", "", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"status":"cancelled"`) {
		t.Fatalf("cancel-hold = %d %s", resp.StatusCode, raw)
	}

	// agent controls + per-tool switch + plan reviews + spend status shapes
	e.do("POST", "/api/v1/agents/sales/control", "", map[string]any{"action": "pause", "drain": "graceful", "reason": "x"}, nil)
	_, raw = e.do("GET", "/api/v1/agent-controls", "", nil, nil)
	if !strings.Contains(string(raw), `"agent_id":"sales"`) || !strings.Contains(string(raw), `"control":"paused"`) || !strings.Contains(string(raw), `"reason":"x"`) {
		t.Fatalf("agent-controls: %s", raw)
	}
	if code := e.status("POST", "/api/v1/org/controls/tools", "", map[string]any{"tool": "email.send", "disabled": true}); code != 200 {
		t.Fatalf("tool switch = %d", code)
	}
	_, raw = e.do("GET", "/api/v1/org/controls", "", nil, nil)
	for _, want := range []string{`"disabled_tools":["email.send"]`, `"admin_count":1`, `"viewer":{`, `"can_release":true`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("controls lacks %s: %s", want, raw)
		}
	}
	if _, raw = e.do("GET", "/api/v1/plan-reviews", "", nil, nil); !strings.Contains(string(raw), `"items":[]`) {
		t.Fatalf("plan-reviews: %s", raw)
	}
	if resp, raw = e.do("GET", "/api/v1/spend-limits/status", "", nil, nil); resp.StatusCode != 200 || !strings.Contains(string(raw), `"items"`) {
		t.Fatalf("spend status: %d %s", resp.StatusCode, raw)
	}
	// grants: {approve:true} alias of the second-human approval, 409 when nothing is pending
	if code := e.status("POST", "/api/v1/connections/"+id+"/grants/assistant", "", map[string]any{"approve": true}); code != 409 {
		t.Fatalf("approve alias with nothing pending = %d", code)
	}
	if code := e.status("POST", "/api/v1/connections/"+id+"/grants/assistant", "", map[string]any{}); code != 400 {
		t.Fatalf("approve alias without approve:true = %d", code)
	}
}

func TestProviderCatalogShape(t *testing.T) {
	e := newConnEnv(t, false, true)
	_, raw := e.do("GET", "/api/v1/connection-providers", "", nil, nil)
	var provs []struct {
		ID           string   `json:"id"`
		Auth         []string `json:"auth"`
		Available    bool     `json:"available"`
		Split        bool     `json:"split_read_write"`
		Live         bool     `json:"live_available"`
		Capabilities []struct {
			ID, Profile, Risk string
			SideEffects       bool `json:"side_effects"`
			AlwaysApproval    bool `json:"always_approval"`
			HoldSeconds       int  `json:"hold_seconds_default"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &provs); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	var found bool
	for _, p := range provs {
		if p.ID != "google_gmail" {
			continue
		}
		found = true
		if !p.Available || !p.Split || !p.Live || len(p.Auth) != 1 || p.Auth[0] != "oauth2_byo_app" || len(p.Capabilities) != 3 {
			t.Fatalf("gmail provider: %+v", p)
		}
		for _, c := range p.Capabilities {
			if c.ID == "mail.send" && (!c.AlwaysApproval || c.Profile != "write" || c.Risk != "high") {
				t.Fatalf("mail.send: %+v", c)
			}
			if c.ID == "mail.read" && (c.Profile != "read" || c.SideEffects) {
				t.Fatalf("mail.read: %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("google_gmail missing from the catalog")
	}
}
