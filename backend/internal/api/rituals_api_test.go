package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// B4: the stand-up and the retro over HTTP: manual run, office chat note, proposals accepted by a person into the
// work profile, settings, RBAC and tenancy.
func TestRitualsOverHTTP(t *testing.T) {
	e := newEnv(t, opts{auth: true, rituals: true})
	owner := e.register("owner-r@example.com", "OwnerR")
	tok := owner.AccessToken
	viewer := e.member(owner, "viewer-r@example.com", auth.RoleViewer)
	member := e.member(owner, "member-r@example.com", auth.RoleMember)
	rival := e.register("rival-r@example.com", "RivalR")

	ctx := context.Background()
	now := time.Now().UTC()
	fin := now.Add(-time.Hour)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.store.CreateTask(ctx, owner.OrgID, domain.Task{ID: "rt-done", Title: "Cotizar a ACME", AgentID: "sales", Status: domain.TaskDone, CreatedAt: now.Add(-2 * time.Hour), FinishedAt: &fin}))
	must(e.store.CreateTask(ctx, owner.OrgID, domain.Task{ID: "rt-run", Title: "Llamar a ACME", AgentID: "sales", Status: domain.TaskRunning, CreatedAt: now.Add(-30 * time.Minute)}))

	// Defaults: both rituals off, no model.
	st := e.json("GET", api1+"/settings/rituals", viewer, nil, 200)
	if st["standup"].(map[string]any)["enabled"] != false || st["retro"].(map[string]any)["enabled"] != false || st["use_model"] != false {
		t.Fatalf("defaults: %v", st)
	}
	if c := e.status("POST", api1+"/rituals/standup/run", viewer, nil); c != 403 {
		t.Errorf("viewer run = %d", c)
	}
	r := e.json("POST", api1+"/rituals/standup/run", member, nil, 201)
	if r["kind"] != "standup" || r["status"] != "held" || r["origin"] != "manual" {
		t.Fatalf("stand-up: %v", r)
	}
	var sales map[string]any
	for _, x := range r["entries"].([]any) {
		if m := x.(map[string]any); m["agent_id"] == "sales" {
			sales = m
		}
	}
	if sales == nil || len(sales["done"].([]any)) != 1 || len(sales["next"].([]any)) != 1 || !strings.Contains(fmt.Sprint(sales["lines"]), "«Llamar a ACME» (en curso)") {
		t.Fatalf("sales entry: %v", sales)
	}
	if c := e.status("POST", api1+"/rituals/party/run", tok, nil); c != 400 {
		t.Errorf("unknown kind = %d", c)
	}
	// The note is in the office chat.
	_, body := e.do("GET", api1+"/conversations/office/messages", tok, nil, nil)
	if !strings.Contains(string(body), "Stand-up diario") {
		t.Errorf("office chat: %s", body)
	}
	id := r["id"].(string)
	if got := e.json("GET", api1+"/rituals/"+id, viewer, nil, 200); got["id"] != id {
		t.Errorf("get: %v", got)
	}
	if c := e.status("GET", api1+"/rituals/"+id, rival.AccessToken, nil); c != 404 {
		t.Errorf("another organization reads it: %d", c)
	}
	_, body = e.do("GET", api1+"/rituals?kind=standup", rival.AccessToken, nil, nil)
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("rival list: %s", body)
	}

	// Retro with two human rejections and two failures for sales -> proposals.
	for i := 0; i < 2; i++ {
		res := now.Add(-time.Hour)
		must(e.store.CreateApproval(ctx, owner.OrgID, domain.Approval{ID: fmt.Sprintf("rt-ap-%d", i), TaskID: "x", AgentID: "sales", Title: "Enviar", Status: domain.ApprovalRejected, Note: "no", CreatedAt: now.Add(-2 * time.Hour), ResolvedAt: &res}))
		must(e.store.CreateTask(ctx, owner.OrgID, domain.Task{ID: fmt.Sprintf("rt-f-%d", i), Title: "Fallo", AgentID: "sales", Status: domain.TaskFailed, CreatedAt: now.Add(-2 * time.Hour), FinishedAt: &fin}))
	}
	retro := e.json("POST", api1+"/rituals/retro/run", tok, nil, 201)
	props := retro["proposals"].([]any)
	if len(props) != 2 {
		t.Fatalf("proposals: %v", retro["proposals"])
	}
	pid := props[0].(map[string]any)["id"].(string)
	pend := e.json("GET", api1+"/rituals/proposals?status=pending&agent_id=sales", viewer, nil, 200)
	_ = pend
	if c := e.status("POST", api1+"/rituals/proposals/"+pid+"/accept", member, nil); c != 403 {
		t.Errorf("a member (no agents:write) accepts = %d", c)
	}
	acc := e.json("POST", api1+"/rituals/proposals/"+pid+"/accept", tok, nil, 200)
	if acc["applied"] != true || acc["proposal"].(map[string]any)["status"] != "accepted" {
		t.Fatalf("accept: %v", acc)
	}
	if c := e.status("POST", api1+"/rituals/proposals/"+pid+"/accept", tok, nil); c != 409 {
		t.Errorf("accept twice = %d", c)
	}
	pid2 := props[1].(map[string]any)["id"].(string)
	if c := e.status("POST", api1+"/rituals/proposals/"+pid2+"/dismiss", rival.AccessToken, nil); c != 404 {
		t.Errorf("rival dismiss = %d", c)
	}
	if d := e.json("POST", api1+"/rituals/proposals/"+pid2+"/dismiss", tok, nil, 200); d["status"] != "dismissed" {
		t.Errorf("dismiss: %v", d)
	}

	// Settings: only org:manage changes them; invalid ones are refused.
	body2 := map[string]any{"timezone": "America/Mexico_City", "standup": map[string]any{"enabled": true, "time": "09:00", "weekdays": []int{1, 2, 3, 4, 5}}, "retro": map[string]any{"enabled": true, "time": "16:00", "weekdays": []int{5}}}
	if c := e.status("PUT", api1+"/settings/rituals", member, body2); c != 403 {
		t.Errorf("member put = %d", c)
	}
	if got := e.json("PUT", api1+"/settings/rituals", tok, body2, 200); got["standup"].(map[string]any)["enabled"] != true {
		t.Errorf("put: %v", got)
	}
	if c := e.status("PUT", api1+"/settings/rituals", tok, map[string]any{"timezone": "Nowhere/City"}); c != 400 {
		t.Errorf("bad tz = %d", c)
	}
}

// Manual runs work in the open demo and are capped per day with a stable code (the kill switch case is covered in
// internal/rituals).
func TestRitualManualRunsAreCapped(t *testing.T) {
	e := newEnv(t, opts{rituals: true})
	r := e.json("POST", api1+"/rituals/standup/run", "", nil, 201)
	if r["status"] != "held" {
		t.Fatalf("open demo run: %v", r)
	}
	// manual runs per day are capped (10 by default)
	for i := 0; i < 9; i++ {
		e.json("POST", api1+"/rituals/standup/run", "", nil, 201)
	}
	resp, body := e.do("POST", api1+"/rituals/standup/run", "", nil, nil)
	if resp.StatusCode != 429 || !strings.Contains(string(body), `"code":"limit"`) {
		t.Errorf("11th run = %d %s", resp.StatusCode, body)
	}
}
