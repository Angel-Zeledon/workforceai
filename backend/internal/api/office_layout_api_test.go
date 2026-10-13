package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// Office editor: the layout document over HTTP (docs/architecture/office-editor.md).
func TestOfficeLayoutOverHTTP(t *testing.T) {
	e := newEnv(t, opts{auth: true, rewards: true})
	owner := e.register("owner-layout@example.com", "OwnerLayout")
	member := e.member(owner, "member-layout@example.com", auth.RoleMember)
	viewer := e.member(owner, "viewer-layout@example.com", auth.RoleViewer)
	rival := e.register("rival-layout@example.com", "RivalLayout")
	ctx := application.WithOrg(context.Background(), owner.OrgID)
	for i := 0; i < 12; i++ {
		e.coins.OnTaskCompleted(ctx, owner.OrgID, domain.Task{ID: fmt.Sprintf("lay-%d", i), Title: "work", AgentID: "analyst", Status: domain.TaskDone})
	}

	// Nothing saved yet: null at version 0, also in the shop state.
	g := e.json("GET", api1+"/office/layout", viewer, nil, 200)
	if g["layout"] != nil || g["version"].(float64) != 0 {
		t.Fatalf("empty layout: %v", g)
	}
	if st := e.json("GET", api1+"/shop/state", viewer, nil, 200); st["layout"] != nil || st["layout_version"].(float64) != 0 {
		t.Fatalf("empty state: %v", st)
	}

	piece := func(item string) map[string]any {
		return map[string]any{"layout": map[string]any{"v": 1, "floor": map[string]any{"kind": "wood"},
			"pieces": []any{map[string]any{"uid": "p1", "item": item, "x": 2.5, "z": 3, "rot": 1.5708}}}, "base_version": 0}
	}

	// Viewers read but cannot save.
	if s := e.status("PUT", api1+"/office/layout", viewer, piece("plant_cactus")); s != 403 {
		t.Fatalf("viewer save = %d", s)
	}
	// Not owned: the existing stable code. Unknown item and bad values: 400 with code "invalid".
	r, b := e.do("PUT", api1+"/office/layout", member, piece("plant_cactus"), nil)
	if r.StatusCode != 409 || !strings.Contains(string(b), `"not_owned"`) {
		t.Fatalf("not owned = %d %s", r.StatusCode, b)
	}
	r, b = e.do("PUT", api1+"/office/layout", member, piece("no_such_item"), nil)
	if r.StatusCode != 400 || !strings.Contains(string(b), `"code":"invalid"`) {
		t.Fatalf("unknown item = %d %s", r.StatusCode, b)
	}
	r, b = e.do("PUT", api1+"/office/layout", member, map[string]any{"layout": map[string]any{"v": 1, "walls": map[string]any{"color": "red"}}, "base_version": 0}, nil)
	if r.StatusCode != 400 || !strings.Contains(string(b), `"code":"invalid"`) {
		t.Fatalf("bad color = %d %s", r.StatusCode, b)
	}
	if s := e.status("PUT", api1+"/office/layout", member, map[string]any{"base_version": 0}); s != 400 {
		t.Fatalf("missing layout = %d", s)
	}

	// Buy, then save.
	e.json("POST", api1+"/shop/purchases", member, map[string]string{"item_id": "plant_cactus"}, 201)
	saved := e.json("PUT", api1+"/office/layout", member, piece("plant_cactus"), 200)
	if saved["version"].(float64) != 1 || saved["layout"].(map[string]any)["floor"].(map[string]any)["kind"] != "wood" {
		t.Fatalf("saved: %v", saved)
	}
	g = e.json("GET", api1+"/office/layout", viewer, nil, 200)
	if g["version"].(float64) != 1 || len(g["layout"].(map[string]any)["pieces"].([]any)) != 1 {
		t.Fatalf("get: %v", g)
	}
	if st := e.json("GET", api1+"/shop/state", viewer, nil, 200); st["layout_version"].(float64) != 1 || st["layout"] == nil {
		t.Fatalf("state: %v", st)
	}

	// A stale base answers 409 layout_conflict with the current version and document.
	r, b = e.do("PUT", api1+"/office/layout", owner.AccessToken, piece("plant_cactus"), nil)
	if r.StatusCode != 409 || !strings.Contains(string(b), `"code":"layout_conflict"`) || !strings.Contains(string(b), `"version":1`) || !strings.Contains(string(b), `"uid":"p1"`) {
		t.Fatalf("conflict = %d %s", r.StatusCode, b)
	}
	next := piece("plant_cactus")
	next["base_version"] = 1
	if s := e.json("PUT", api1+"/office/layout", owner.AccessToken, next, 200); s["version"].(float64) != 2 {
		t.Fatalf("second save: %v", s)
	}

	// The layout is private to the organization.
	if g := e.json("GET", api1+"/office/layout", rival.AccessToken, nil, 200); g["layout"] != nil || g["version"].(float64) != 0 {
		t.Fatalf("rival sees our layout: %v", g)
	}

	// Audited with the person as actor.
	page, err := e.store.QueryAudit(ctx, owner.OrgID, domain.AuditQuery{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range page.Items {
		if l.Action == "office.layout_updated" && l.Actor != "system" && l.Actor != "" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("audited saves = %d", n)
	}
}
