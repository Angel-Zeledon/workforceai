package api_test

import (
	"testing"

	"aiworkforce/backend/internal/auth"
)

func TestRoleTemplatesAndHiring(t *testing.T) {
	e := newEnv(t, opts{auth: true})
	owner := e.register("owner@example.com", "Owner")
	tok := owner.AccessToken

	items := e.list(api1+"/role-templates?locale=en", tok)
	if len(items) != 11 {
		t.Fatalf("templates = %d", len(items))
	}
	byID := map[string]map[string]any{}
	for _, it := range items {
		byID[str(it, "id")] = it
	}
	pm := byID["project_manager"]
	if pm == nil || pm["seed"] != false || pm["title"] != "Project manager" || pm["risk_tier"] != "green" || pm["hired"].(float64) != 0 {
		t.Fatalf("project_manager: %v", pm)
	}
	if byID["sales"]["seed"] != true || byID["sales"]["hired"].(float64) != 1 {
		t.Fatalf("sales: %v", byID["sales"])
	}
	if d := e.json("GET", api1+"/role-templates/software_engineer?locale=es", tok, nil, 200); len(d["disclaimers"].([]any)) == 0 {
		t.Fatalf("software_engineer disclaimers: %v", d)
	}
	if c := e.status("GET", api1+"/role-templates/nope", tok, nil); c != 404 {
		t.Fatalf("unknown template = %d", c)
	}

	// Members and viewers cannot hire; admins and owners can.
	member := e.member(owner, "member@example.com", auth.RoleMember)
	if c := e.status("POST", api1+"/agents/from-template", member, map[string]any{"template_id": "education"}); c != 403 {
		t.Fatalf("member hiring = %d", c)
	}
	if c := e.status("POST", api1+"/agents/from-template", tok, map[string]any{"template_id": "nope"}); c != 404 {
		t.Fatalf("unknown template hire = %d", c)
	}
	if c := e.status("POST", api1+"/agents/from-template", tok, map[string]any{}); c != 400 {
		t.Fatalf("missing template_id = %d", c)
	}
	admin := e.member(owner, "admin@example.com", auth.RoleAdmin)
	res := e.json("POST", api1+"/agents/from-template", admin, map[string]any{"template_id": "project_manager", "locale": "es"}, 201)
	a := res["agent"].(map[string]any)
	if a["id"] != "project_manager" || a["role"] != "project_manager" || a["name"] != "Lucía Herrera" || a["autonomy"] != "approve_each" || a["state"] != "idle" {
		t.Fatalf("hired agent: %v", a)
	}
	// A second one of the same profession gets a new id and the next free name.
	a2 := e.json("POST", api1+"/agents/from-template", tok, map[string]any{"template_id": "project_manager", "locale": "es"}, 201)["agent"].(map[string]any)
	if a2["id"] != "project_manager_2" || a2["name"] != "Andrés Molina" {
		t.Fatalf("second hire: %v", a2)
	}
	named := e.json("POST", api1+"/agents/from-template", tok, map[string]any{"template_id": "education", "name": "Profe Ana"}, 201)["agent"].(map[string]any)
	if named["name"] != "Profe Ana" {
		t.Fatalf("named hire: %v", named)
	}
	found := false
	for _, ag := range e.list(api1+"/agents", tok) {
		found = found || str(ag, "id") == "project_manager_2"
	}
	if !found {
		t.Fatal("the hired agent is not listed")
	}
	if n := e.json("GET", api1+"/role-templates/project_manager", tok, nil, 200)["hired"].(float64); n != 2 {
		t.Fatalf("hired count = %v", n)
	}
	if !e.hasAuditAction("agent.created_from_template") {
		t.Fatal("hiring is not audited")
	}

	// The office has a ceiling.
	for i := 0; i < 20; i++ {
		e.status("POST", api1+"/agents/from-template", tok, map[string]any{"template_id": "data_analyst"})
	}
	if c := e.status("POST", api1+"/agents/from-template", tok, map[string]any{"template_id": "data_analyst"}); c != 409 {
		t.Fatalf("full office = %d", c)
	}
}

func (e *env) hasAuditAction(action string) bool {
	for _, a := range e.store.Audit() {
		if a.Action == action {
			return true
		}
	}
	return false
}
