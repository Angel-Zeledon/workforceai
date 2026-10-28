package api_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/roles"
)

// B1: the trust ladder over HTTP. Viewers read, admins/owners decide, members cannot; promotions are suggestions
// until accepted; the kill switch blocks a raise; every response carries the methodology.
func TestTrustLadderOverHTTP(t *testing.T) {
	var mu sync.Mutex
	clock := time.Now().UTC().Add(-30 * 24 * time.Hour)
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	advance := func(d time.Duration) { mu.Lock(); clock = clock.Add(d); mu.Unlock() }
	blocked := ""
	e := newEnv(t, opts{auth: true, trust: true, trustClock: now, trustBlocked: &blocked})
	owner := e.register("owner-tr@example.com", "OwnerTR")
	admin := e.member(owner, "admin-tr@example.com", auth.RoleAdmin)
	member := e.member(owner, "member-tr@example.com", auth.RoleMember)
	viewer := e.member(owner, "viewer-tr@example.com", auth.RoleViewer)
	ctx := context.Background()
	tpl, _ := roles.Get("data_analyst")
	if err := e.store.CreateAgent(ctx, owner.OrgID, roles.Instantiate(tpl, "es", "data_analyst", "Dana")); err != nil {
		t.Fatal(err)
	}
	const a = api1 + "/agents/data_analyst/trust"

	got := e.json("GET", a, viewer, nil, 200)
	m := got["methodology"].(map[string]any)
	if m["promotion_needs_human"] != true || m["auto_demotion"] != true || len(m["promotion"].([]any)) != 6 {
		t.Fatalf("methodology: %v", m)
	}
	ag := got["agent"].(map[string]any)
	if ag["level"] != "approve_each" || ag["ceiling"] != "autonomous" || ag["recommendation"] != "probation" || ag["pending"] != nil {
		t.Fatalf("new agent: %v", ag)
	}
	if s := e.status("GET", api1+"/agents/ghost/trust", viewer, nil); s != 404 {
		t.Fatalf("unknown agent = %d", s)
	}

	// 20 approvals after the probation: a promotion is suggested, nothing changes yet.
	advance(15 * 24 * time.Hour)
	for i := 0; i < 20; i++ {
		at := now()
		if err := e.store.CreateApproval(ctx, owner.OrgID, domain.Approval{ID: fmt.Sprintf("tr-ap-%d", i), AgentID: "data_analyst", Risk: "low",
			Status: domain.ApprovalApproved, CreatedAt: at, ResolvedAt: &at}); err != nil {
			t.Fatal(err)
		}
	}
	if s := e.status("POST", a+"/evaluate", viewer, nil); s != 403 {
		t.Fatalf("viewer evaluate = %d", s)
	}
	ev := e.json("POST", a+"/evaluate", admin, nil, 200)["agent"].(map[string]any)
	pending, _ := ev["pending"].(map[string]any)
	if ev["recommendation"] != "promote" || pending == nil || pending["to_level"] != "rules" || ev["level"] != "approve_each" {
		t.Fatalf("suggested promotion: %v", ev)
	}
	pid := pending["id"].(string)
	accept := api1 + "/trust/proposals/" + pid + "/accept"
	if s := e.status("POST", accept, member, nil); s != 403 {
		t.Fatalf("member accept = %d", s)
	}
	if s := e.status("POST", accept, viewer, nil); s != 403 {
		t.Fatalf("viewer accept = %d", s)
	}
	blocked = "kill_switch_active"
	if out := e.json("POST", accept, admin, map[string]any{"note": "x"}, 409); out["code"] != "kill_switch_active" {
		t.Fatalf("blocked accept: %v", out)
	}
	blocked = ""
	p := e.json("POST", accept, admin, map[string]any{"note": "earned"}, 200)
	if p["status"] != "accepted" || p["to_level"] != "rules" {
		t.Fatalf("accept: %v", p)
	}
	ag = e.json("GET", a, viewer, nil, 200)["agent"].(map[string]any)
	if ag["level"] != "rules" || ag["last_change"] == nil {
		t.Fatalf("after accept: %v", ag)
	}
	if s := e.status("POST", accept, admin, nil); s != 409 {
		t.Fatalf("double accept = %d", s)
	}
	hist := e.list(a+"/proposals", viewer)
	if len(hist) != 1 || hist[0]["status"] != "accepted" {
		t.Fatalf("history: %v", hist)
	}

	// A rejected high-risk action demotes at once; the owner can revert it.
	advance(time.Hour)
	at := now()
	if err := e.store.CreateApproval(ctx, owner.OrgID, domain.Approval{ID: "tr-high", AgentID: "data_analyst", Risk: "high",
		Status: domain.ApprovalRejected, Note: "no", CreatedAt: at, ResolvedAt: &at}); err != nil {
		t.Fatal(err)
	}
	ev = e.json("POST", a+"/evaluate", admin, nil, 200)["agent"].(map[string]any)
	lc := ev["last_change"].(map[string]any)
	if ev["level"] != "approve_each" || lc["status"] != "applied" || lc["auto"] != true || ev["can_revert"] != true {
		t.Fatalf("automatic demotion: %v", ev)
	}
	e.json("POST", api1+"/trust/proposals/"+lc["id"].(string)+"/revert", owner.AccessToken, map[string]any{"note": "false alarm"}, 200)
	if lvl := e.json("GET", a, viewer, nil, 200)["agent"].(map[string]any)["level"]; lvl != "rules" {
		t.Fatalf("revert: %v", lvl)
	}

	// Audited.
	page := e.json("GET", api1+"/audit?action=agent.autonomy_changed", owner.AccessToken, nil, 200)
	if items := page["items"].([]any); len(items) != 3 {
		t.Fatalf("autonomy changes audited: %d", len(items))
	}
	// Overview lists every agent.
	if ov := e.json("GET", api1+"/trust/agents", viewer, nil, 200); len(ov["agents"].([]any)) < 2 {
		t.Fatalf("overview: %v", ov)
	}
}
