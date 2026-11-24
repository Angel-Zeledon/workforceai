package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// Temporary helpers over HTTP: the flag, the permissions (the rule of the chat, not just a role), the contract of
// accept / decline / stop / retry, helper_group in GET /requests/{id}, and helpers_enabled / helper_groups for the
// frontend (auth config and the WebSocket hello).

func (s *orgSpy) CreateHelperGroup(ctx context.Context, org string, g domain.HelperGroup) error {
	return s.Store.(application.HelperStore).CreateHelperGroup(ctx, org, g)
}
func (s *orgSpy) GetHelperGroup(ctx context.Context, org, id string) (domain.HelperGroup, error) {
	return s.Store.(application.HelperStore).GetHelperGroup(ctx, org, id)
}
func (s *orgSpy) GetHelperGroupByRequest(ctx context.Context, org, req string) (domain.HelperGroup, error) {
	return s.Store.(application.HelperStore).GetHelperGroupByRequest(ctx, org, req)
}
func (s *orgSpy) UpdateHelperGroup(ctx context.Context, org string, g domain.HelperGroup) error {
	return s.Store.(application.HelperStore).UpdateHelperGroup(ctx, org, g)
}
func (s *orgSpy) AddHelperSpend(ctx context.Context, org, id string, usd float64) error {
	return s.Store.(application.HelperStore).AddHelperSpend(ctx, org, id, usd)
}
func (s *orgSpy) ListHelperGroups(ctx context.Context, org string, st ...string) ([]domain.HelperGroup, error) {
	return s.Store.(application.HelperStore).ListHelperGroups(ctx, org, st...)
}

func helpersOn(o *opts) { o.cfg = helpersCfg }

func helpersCfg(c *application.Config) { c.HelpersEnabled = true }

// seedProposal stores a finished request of agent and a proposal of 6 parts on it.
func seedProposal(t *testing.T, e *env, org, reqID, agent string) domain.HelperGroup {
	t.Helper()
	ctx, now := context.Background(), time.Now().UTC()
	if _, err := e.store.GetRequest(ctx, org, reqID); err != nil {
		if err := e.store.CreateRequest(ctx, org, domain.Request{ID: reqID, Text: "revisa estos contratos", Status: domain.RequestDone, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := e.store.CreateTask(ctx, org, domain.Task{ID: "t-" + reqID, RequestID: reqID, Title: "Revisar", AgentID: agent, Status: domain.TaskDone,
			DependsOn: []string{}, CreatedAt: now, FinishedAt: &now, Output: &domain.StructuredOutput{Summary: "ok"}}); err != nil {
			t.Fatal(err)
		}
	}
	g := domain.HelperGroup{ID: "g-" + reqID, RequestID: reqID, OwnerAgentID: agent, Mode: domain.HelperModePosition, Count: 6, MaxCount: 20, Parallel: 3,
		Estimate: domain.HelperEstimate{MinUSD: 0.01, MaxUSD: 0.2}, CapUSD: 0.2, Status: domain.HelperProposed, CreatedAt: now}
	for i := 1; i <= 6; i++ {
		g.Parts = append(g.Parts, domain.HelperPart{Index: i, Label: fmt.Sprintf("Contratos %d", i), Text: fmt.Sprintf("contrato %d", i)})
	}
	if err := e.spy.CreateHelperGroup(ctx, org, g); err != nil {
		t.Fatal(err)
	}
	return g
}

func (e *env) helperCall(method, path, token string, body any, want int) map[string]any {
	e.t.Helper()
	resp, raw := e.do(method, path, token, body, nil)
	if resp.StatusCode != want {
		e.t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func (e *env) waitRequestDone(token, id string) {
	e.t.Helper()
	for i := 0; i < 400; i++ {
		resp, raw := e.do("GET", api1+"/requests/"+id, token, nil, nil)
		var r struct{ Status string }
		if resp.StatusCode == 200 && json.Unmarshal(raw, &r) == nil && r.Status == string(domain.RequestDone) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatal("the request did not finish")
}

func TestHelpersAreInvisibleWithTheFlagOff(t *testing.T) {
	e := newEnv(t, opts{})
	seedProposal(t, e, domain.DemoOrgID, "r1", "sales")
	for _, c := range []struct{ path, body string }{{"/helpers", `{"count":4,"cap_usd":1}`}, {"/helpers/decline", `{}`}, {"/helpers/stop", `{}`}, {"/helpers/retry", `{"part":1}`}} {
		if got := e.status("POST", api1+"/requests/r1"+c.path, "", json.RawMessage(c.body)); got != 404 {
			t.Fatalf("POST %s with the flag off = %d, want 404", c.path, got)
		}
	}
	got := e.helperCall("GET", api1+"/requests/r1", "", nil, 200)
	if _, has := got["helper_group"]; has {
		t.Fatal("helper_group must not appear with the flag off")
	}
	if cfg := e.helperCall("GET", api1+"/auth/config", "", nil, 200); cfg["helpers_enabled"] != false {
		t.Fatalf("auth config: %v", cfg)
	}
	if got := e.status("POST", api1+"/messages", "", map[string]any{"conversation": "office", "text": "x", "helpers": true}); got != 404 {
		t.Fatalf("helpers:true with the flag off = %d", got)
	}
}

func TestHelpersContractOverHTTP(t *testing.T) {
	e := newEnv(t, opts{cfg: helpersCfg})
	org := domain.DemoOrgID
	seedProposal(t, e, org, "r1", "sales")
	if cfg := e.helperCall("GET", api1+"/auth/config", "", nil, 200); cfg["helpers_enabled"] != true {
		t.Fatalf("auth config: %v", cfg)
	}
	got := e.helperCall("GET", api1+"/requests/r1", "", nil, 200)
	if g, _ := got["helper_group"].(map[string]any); g["status"] != "proposed" || g["count"] != float64(6) {
		t.Fatalf("helper_group = %v", got["helper_group"])
	}

	bad := []map[string]any{{"count": 1, "cap_usd": 1}, {"count": 21, "cap_usd": 1}, {"count": 7, "cap_usd": 1}, {"count": 4, "cap_usd": 0.001},
		{"count": 4, "cap_usd": 1e6}, {"count": 4}}
	for _, b := range bad {
		e.helperCall("POST", api1+"/requests/r1/helpers", "", b, 400)
	}
	e.helperCall("POST", api1+"/requests/nope/helpers", "", map[string]any{"count": 4, "cap_usd": 1}, 404)
	if g, _ := e.spy.GetHelperGroupByRequest(context.Background(), org, "r1"); g.Status != domain.HelperProposed {
		t.Fatal("a refused accept leaves the proposal open")
	}

	ok := e.helperCall("POST", api1+"/requests/r1/helpers", "", map[string]any{"count": 3, "cap_usd": 1}, 200)
	grp, _ := ok["group"].(map[string]any)
	if grp["id"] != "g-r1" || grp["count"] != float64(3) || grp["accepted_by"] == "" || grp["status"] == "proposed" {
		t.Fatalf("accepted group = %v", grp)
	}
	again := e.helperCall("POST", api1+"/requests/r1/helpers", "", map[string]any{"count": 3, "cap_usd": 1}, 200) // idempotent
	if g, _ := again["group"].(map[string]any); g["id"] != "g-r1" {
		t.Fatalf("second accept = %v", again)
	}
	e.waitRequestDone("", "r1")
	e.helperCall("POST", api1+"/requests/r1/helpers/decline", "", map[string]any{}, 409) // already launched
	e.helperCall("POST", api1+"/requests/r1/helpers/stop", "", map[string]any{"part": -1}, 400)
	e.helperCall("POST", api1+"/requests/r1/helpers/stop", "", map[string]any{}, 200) // nothing left to stop: the group as it is
	e.helperCall("POST", api1+"/requests/r1/helpers/retry", "", map[string]any{}, 400)
	e.helperCall("POST", api1+"/requests/r1/helpers/retry", "", map[string]any{"part": 1}, 409) // a finished part is not retried
	e.helperCall("POST", api1+"/requests/r1/helpers/retry", "", map[string]any{"part": 99}, 404)
	final := e.helperCall("GET", api1+"/requests/r1", "", nil, 200)
	if g, _ := final["helper_group"].(map[string]any); g["status"] != "done" {
		t.Fatalf("helper_group at the end = %v", final["helper_group"])
	}
}

func TestDecliningOverHTTP(t *testing.T) {
	e := newEnv(t, opts{cfg: helpersCfg})
	seedProposal(t, e, domain.DemoOrgID, "r2", "sales")
	// an empty body is fine
	if got := e.status("POST", api1+"/requests/r2/helpers/decline", "", nil); got != 200 {
		t.Fatalf("decline = %d", got)
	}
	out := e.helperCall("POST", api1+"/requests/r2/helpers/decline", "", map[string]any{}, 200) // idempotent
	if g, _ := out["group"].(map[string]any); g["status"] != "declined" {
		t.Fatalf("declined group = %v", out)
	}
	e.helperCall("POST", api1+"/requests/r2/helpers", "", map[string]any{"count": 4, "cap_usd": 1}, 409)
	e.helperCall("POST", api1+"/requests/r2/helpers/stop", "", map[string]any{}, 409)
	e.helperCall("POST", api1+"/requests/r2/helpers/retry", "", map[string]any{"part": 1}, 409)
}

func TestHelpersPermissions(t *testing.T) {
	p := newPrivateWork(t, helpersOn)
	e, org := p.e, p.owner.OrgID
	seedProposal(t, e, org, "req-legal", "legal")
	seedProposal(t, e, org, "req-sales", "sales")
	viewer := e.member(p.owner, "viewer-hp@example.com", auth.RoleViewer)
	outsider := e.register("other-hp@example.com", "Other").AccessToken
	accept := map[string]any{"count": 3, "cap_usd": 1}

	// a viewer cannot steer helpers (conversations:write)
	for _, path := range []string{"/helpers", "/helpers/decline", "/helpers/stop", "/helpers/retry"} {
		e.helperCall("POST", api1+"/requests/req-sales"+path, viewer, accept, 403)
	}
	// another company never sees them
	e.helperCall("POST", api1+"/requests/req-sales/helpers", outsider, accept, 404)
	e.helperCall("POST", api1+"/requests/req-sales/helpers/decline", outsider, map[string]any{}, 404)
	// the work of a private department the person is not in does not exist for them, nor do its helpers
	e.helperCall("POST", api1+"/requests/req-legal/helpers", p.beto, accept, 404)
	e.helperCall("POST", api1+"/requests/req-legal/helpers/decline", p.beto, map[string]any{}, 404)
	if got := e.helperCall("GET", api1+"/requests/req-sales", p.beto, nil, 200); got["helper_group"] == nil {
		t.Fatal("beto sees the helpers of the request of a General agent")
	}
	if g, _ := e.spy.GetHelperGroupByRequest(context.Background(), org, "req-legal"); g.Status != domain.HelperProposed {
		t.Fatal("nothing changed by the refused calls")
	}
	// the people who may ask the responsible for work do
	e.helperCall("POST", api1+"/requests/req-legal/helpers", p.ana, accept, 200)
	e.helperCall("POST", api1+"/requests/req-sales/helpers/decline", p.beto, map[string]any{}, 200)
	e.waitRequestDone(p.ana, "req-legal")
}

func TestHelpersTellTheSocketClient(t *testing.T) {
	for _, on := range []bool{false, true} {
		o := opts{}
		if on {
			helpersOn(&o)
		}
		e := newEnv(t, o)
		seedProposal(t, e, domain.DemoOrgID, "r3", "sales")
		c, _, err := dialWS(t, e, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		hello := readFrame(t, c)
		c.Close()
		pl, _ := hello.Payload.(map[string]any)
		if pl["helpers_enabled"] != on {
			t.Fatalf("on=%v: helpers_enabled = %v", on, pl["helpers_enabled"])
		}
		groups, _ := pl["helper_groups"].([]any)
		if on != (len(groups) == 1) {
			t.Fatalf("on=%v: helper_groups = %v", on, pl["helper_groups"])
		}
		if on && groups[0].(map[string]any)["request_id"] != "r3" {
			t.Fatalf("group = %v", groups[0])
		}
	}
}

func TestHelpersWithoutAPositionNeedTheOffice(t *testing.T) {
	e := newEnv(t, opts{cfg: helpersCfg})
	if got := e.status("POST", api1+"/messages", "", map[string]any{"conversation": "agent:sales", "text": "x", "helpers": true}); got != 400 {
		t.Fatalf("helpers in a chat with one agent = %d, want 400", got)
	}
	if got := e.status("POST", api1+"/messages", "", map[string]any{"conversation": "office", "text": "investiga estas 12 ciudades", "helpers": true}); got != 202 {
		t.Fatalf("helpers in the office = %d, want 202", got)
	}
}
