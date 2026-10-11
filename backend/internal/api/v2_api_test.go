package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

func TestV2EndpointsAndRoles(t *testing.T) {
	e := newEnv(t, opts{auth: true, v2: true})
	owner := e.register("owner@example.com", "Owner")
	tok := owner.AccessToken
	member := e.member(owner, "member@example.com", auth.RoleMember)
	admin := e.member(owner, "admin@example.com", auth.RoleAdmin)

	// profile: everyone reads, only admins/owners write
	p := e.json("GET", api1+"/agents/sales/profile", member, nil, 200)
	if p["version"].(float64) != 0 || p["creativity"] != "medium" || len(p["role_skills"].([]any)) == 0 {
		t.Fatalf("default profile: %v", p)
	}
	body := map[string]any{"instructions": "Se breve.", "creativity": "low", "skills": []string{"margenes"}}
	if c := e.status("PUT", api1+"/agents/sales/profile", member, body); c != 403 {
		t.Fatalf("member edit = %d", c)
	}
	if c := e.status("PUT", api1+"/agents/sales/profile", admin, map[string]any{"instructions": "clave " + strings.Repeat("sk-live-", 1) + "ABCDEFGHIJKLMNOPQRSTUV"}); c != 400 {
		t.Fatalf("secret accepted = %d", c)
	}
	p = e.json("PUT", api1+"/agents/sales/profile", admin, body, 200)
	if p["version"].(float64) != 1 || p["creativity"] != "low" {
		t.Fatalf("saved: %v", p)
	}
	vs := e.list(api1+"/agents/sales/profile/versions", member)
	if len(vs) != 1 || vs[0]["author"] == "" {
		t.Fatalf("versions: %v", vs)
	}
	e.json("PUT", api1+"/agents/sales/profile", admin, map[string]any{"instructions": "Otra.", "creativity": "high"}, 200)
	if r := e.json("POST", api1+"/agents/sales/profile/restore", admin, map[string]any{"version": 1}, 200); r["version"].(float64) != 3 || r["instructions"] != "Se breve." {
		t.Fatalf("restore: %v", r)
	}
	if c := e.status("POST", api1+"/agents/sales/profile/restore", admin, map[string]any{"version": 99}); c != 404 {
		t.Fatalf("restore unknown = %d", c)
	}

	// thought audit: admins/owners only; export is audited
	org := domain.DemoOrgID
	_ = e.store.AddTrace(context.Background(), org, application.Trace{ID: "tr1", AgentID: "sales", TaskID: "k", CreatedAt: time.Now().UTC(), Status: "ok",
		PromptStored: true, Prompt: &application.TracePrompt{System: "s"}, ToolRequests: []application.TraceTool{{Tool: "email", Action: "x"}}})
	if c := e.status("GET", api1+"/agents/sales/traces", member, nil); c != 403 {
		t.Fatalf("member reads traces = %d", c)
	}
	if c := e.status("GET", api1+"/agents/sales/traces/export", member, nil); c != 403 {
		t.Fatalf("member exports traces = %d", c)
	}
	l := e.json("GET", api1+"/agents/sales/traces", admin, nil, 200)
	trs := l["traces"].([]any)
	if len(trs) != 1 || trs[0].(map[string]any)["prompt"] != nil || l["store_prompts"] != true || l["retention_days"].(float64) != 30 {
		t.Fatalf("list: %v", l)
	}
	if d := e.json("GET", api1+"/agents/sales/traces/tr1", admin, nil, 200); d["prompt"] == nil {
		t.Fatalf("detail without prompt: %v", d)
	}
	if c := e.status("GET", api1+"/agents/sales/traces/export", admin, nil); c != 200 {
		t.Fatalf("export = %d", c)
	}
	if !e.hasAuditAction("trace.exported") {
		t.Fatal("trace export not audited")
	}
	if c := e.status("PUT", api1+"/settings/traces", member, map[string]any{"store_prompts": false}); c != 403 {
		t.Fatalf("member sets store_prompts = %d", c)
	}
	e.json("PUT", api1+"/settings/traces", tok, map[string]any{"store_prompts": false}, 200)
	if g := e.json("GET", api1+"/settings/traces", admin, nil, 200); g["store_prompts"] != false {
		t.Fatalf("settings: %v", g)
	}

	// intervention: tasks:manage
	if c := e.status("POST", api1+"/tasks/nope/steer", member, map[string]any{"note": "x"}); c != 403 {
		t.Fatalf("member steers = %d", c)
	}
	if c := e.status("POST", api1+"/tasks/nope/steer", admin, map[string]any{"note": "x"}); c != 404 {
		t.Fatalf("steer unknown task = %d", c)
	}
	if c := e.status("POST", api1+"/tasks/nope/reassign", admin, map[string]any{"agent_id": "hr"}); c != 404 {
		t.Fatalf("reassign unknown task = %d", c)
	}
}
