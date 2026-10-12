package api_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// C1: the coin wallet, the shop and the cosmetic state over HTTP.
func TestCoinsAndShopOverHTTP(t *testing.T) {
	e := newEnv(t, opts{auth: true, rewards: true})
	owner := e.register("owner-coins@example.com", "OwnerCoins")
	member := e.member(owner, "member-coins@example.com", auth.RoleMember)
	viewer := e.member(owner, "viewer-coins@example.com", auth.RoleViewer)
	rival := e.register("rival-coins@example.com", "RivalCoins")
	ctx := application.WithOrg(context.Background(), owner.OrgID)
	ctxRival := application.WithOrg(context.Background(), rival.OrgID)

	earn := func(ctx context.Context, org string, n int) {
		for i := 0; i < n; i++ {
			e.coins.OnTaskCompleted(ctx, org, domain.Task{ID: fmt.Sprintf("%s-%d", org, i), Title: "work", AgentID: "analyst", Status: domain.TaskDone})
		}
	}
	earn(ctx, owner.OrgID, 12) // 120 coins

	// Reading is open to every role; the wallet is the organization's own.
	w := e.json("GET", api1+"/rewards/wallet", viewer, nil, 200)
	if wl := w["wallet"].(map[string]any); wl["balance"].(float64) != 120 || w["earned_by_agent"].(map[string]any)["analyst"].(float64) != 120 {
		t.Fatalf("wallet: %v", w)
	}
	if w := e.json("GET", api1+"/rewards/wallet", rival.AccessToken, nil, 200); w["wallet"].(map[string]any)["balance"].(float64) != 0 {
		t.Fatalf("rival sees our coins: %v", w)
	}
	cat := e.json("GET", api1+"/shop/catalog", viewer, nil, 200)
	if len(cat["items"].([]any)) < 40 || len(cat["character_slots"].([]any)) == 0 || len(cat["office_slots"].([]any)) == 0 {
		t.Fatalf("catalog: %v", cat)
	}
	if r := e.json("GET", api1+"/rewards/rules", viewer, nil, 200); r["enabled"] != true {
		t.Fatalf("rules: %v", r)
	}
	led := e.list(api1+"/rewards/ledger?limit=5", viewer)
	if len(led) != 5 || led[0]["kind"] != "earn" || led[0]["agent_id"] != "analyst" {
		t.Fatalf("ledger: %v", led)
	}

	// Viewers cannot buy, equip or place; there is no way to credit coins.
	buy := map[string]string{"item_id": "hat_cap"}
	if s := e.status("POST", api1+"/shop/purchases", viewer, buy); s != 403 {
		t.Fatalf("viewer buy = %d", s)
	}
	if s := e.status("PUT", api1+"/agents/analyst/equipment/hat", viewer, buy); s != 403 {
		t.Fatalf("viewer equip = %d", s)
	}
	for _, p := range []string{"/rewards/wallet", "/rewards/grant", "/rewards/topup", "/shop/coins", "/shop/credit"} {
		for _, m := range []string{"POST", "PUT", "PATCH"} {
			if s := e.status(m, api1+p, owner.AccessToken, map[string]any{"amount": 1000}); s != 404 && s != 405 {
				t.Fatalf("%s %s = %d: a route can credit coins", m, p, s)
			}
		}
	}

	// Members buy. Idempotency-Key replays without a second charge.
	hdr := map[string]string{"Idempotency-Key": "buy-hat-1"}
	r, b := e.do("POST", api1+"/shop/purchases", member, buy, hdr)
	if r.StatusCode != 201 || !strings.Contains(string(b), `"balance":70`) {
		t.Fatalf("buy = %d %s", r.StatusCode, b)
	}
	r, b = e.do("POST", api1+"/shop/purchases", member, buy, hdr)
	if r.StatusCode != 200 || !strings.Contains(string(b), `"replay":true`) || !strings.Contains(string(b), `"balance":70`) {
		t.Fatalf("replay = %d %s", r.StatusCode, b)
	}
	r, b = e.do("POST", api1+"/shop/purchases", member, buy, map[string]string{"Idempotency-Key": "other"})
	if r.StatusCode != 409 || !strings.Contains(string(b), "already_owned") {
		t.Fatalf("second copy = %d %s", r.StatusCode, b)
	}
	r, b = e.do("POST", api1+"/shop/purchases", member, map[string]string{"item_id": "outfit_gold"}, nil)
	if r.StatusCode != 409 || !strings.Contains(string(b), "insufficient_coins") {
		t.Fatalf("too expensive = %d %s", r.StatusCode, b)
	}
	if s := e.status("POST", api1+"/shop/purchases", member, map[string]string{"item_id": "nope"}); s != 404 {
		t.Fatalf("unknown item = %d", s)
	}

	// Equip / unequip an agent; the slot must match the item.
	if s := e.status("PUT", api1+"/agents/analyst/equipment/face", member, buy); s != 400 {
		t.Fatalf("wrong slot = %d", s)
	}
	if s := e.status("PUT", api1+"/agents/ghost/equipment/hat", member, buy); s != 404 {
		t.Fatalf("unknown agent = %d", s)
	}
	if s := e.status("PUT", api1+"/agents/analyst/equipment/hat", rival.AccessToken, buy); s != 409 { // not owned by the rival
		t.Fatalf("rival equip = %d", s)
	}
	e.json("PUT", api1+"/agents/analyst/equipment/hat", member, buy, 200)
	st := e.json("GET", api1+"/shop/state", viewer, nil, 200)
	if eq := st["equipment"].([]any); len(eq) != 1 || eq[0].(map[string]any)["item_id"] != "hat_cap" || len(st["inventory"].([]any)) != 1 {
		t.Fatalf("state: %v", st)
	}
	if s := e.status("DELETE", api1+"/agents/analyst/equipment/hat", member, nil); s != 204 {
		t.Fatalf("unequip = %d", s)
	}
	if s := e.status("DELETE", api1+"/agents/analyst/equipment/zzz", member, nil); s != 400 {
		t.Fatalf("unequip bad slot = %d", s)
	}

	// Office items.
	e.json("POST", api1+"/shop/purchases", member, map[string]string{"item_id": "plant_cactus"}, 201)
	e.json("PUT", api1+"/office/items/plant_1", member, map[string]string{"item_id": "plant_cactus"}, 200)
	if s := e.status("PUT", api1+"/office/items/lamp_1", member, map[string]string{"item_id": "plant_cactus"}); s != 400 {
		t.Fatalf("wrong office slot = %d", s)
	}
	if st := e.json("GET", api1+"/shop/state", viewer, nil, 200); len(st["office"].([]any)) != 1 {
		t.Fatalf("office: %v", st)
	}
	if s := e.status("DELETE", api1+"/office/items/plant_1", member, nil); s != 204 {
		t.Fatalf("remove = %d", s)
	}

	// Purchases and equipment are audited with the person as actor.
	page, err := e.store.QueryAudit(ctx, owner.OrgID, domain.AuditQuery{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]bool{}
	for _, l := range page.Items {
		if strings.HasPrefix(l.Action, "shop.") || strings.HasPrefix(l.Action, "item.") || strings.HasPrefix(l.Action, "office.") {
			actions[l.Action] = l.Actor != "system" && l.Actor != ""
		}
	}
	for _, a := range []string{"shop.purchased", "item.equipped", "item.unequipped", "office.item_placed", "office.item_removed"} {
		if !actions[a] {
			t.Errorf("audit %s missing or without a person as actor (%v)", a, actions)
		}
	}

	// The rival's coins are its own, and parallel purchases never overspend.
	earn(ctxRival, rival.OrgID, 9) // 90
	var wg sync.WaitGroup
	var ok atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s := e.status("POST", api1+"/shop/purchases", rival.AccessToken, map[string]string{"item_id": []string{"hat_cap", "scarf_red", "badge_star", "badge_heart"}[i%4]}); s == 201 {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	wl, _ := e.coins.Store.Wallet(ctxRival, rival.OrgID)
	if wl.Balance < 0 || wl.Balance+wl.SpentTotal != 90 || ok.Load() < 1 {
		t.Fatalf("rival wallet %+v after %d purchases", wl, ok.Load())
	}
	if w, _ := e.coins.Store.Wallet(ctx, owner.OrgID); w.Balance != 20 { // 120 - hat_cap 50 - plant_cactus 50
		t.Fatalf("our wallet changed by the rival: %+v", w)
	}
}
