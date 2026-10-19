package api_test

import (
	"testing"

	"aiworkforce/backend/internal/auth"
)

// The corporate organization model over HTTP: reads for members, changes for owner/admin, validated, per organization.
func TestOrgChartAPI(t *testing.T) {
	e := newEnv(t, opts{auth: true, orgchart: true})
	owner := e.register("owner-org@example.com", "OwnerOrg")
	member := e.member(owner, "member-org@example.com", auth.RoleMember)
	rival := e.register("rival-org@example.com", "RivalOrg")

	// departments: owner/admin only to change, cycles and duplicates rejected
	hq := e.json("POST", api1+"/org/departments", owner.AccessToken, map[string]any{"name": "HQ", "cost_center": "CC-000"}, 201)
	hqID := hq["id"].(string)
	e.json("POST", api1+"/org/departments", member, map[string]any{"name": "Nope"}, 403)
	fin := e.json("POST", api1+"/org/departments", owner.AccessToken, map[string]any{"name": "Finance", "parent_id": hqID, "cost_center": "CC-100"}, 201)
	finID := fin["id"].(string)
	e.json("POST", api1+"/org/departments", owner.AccessToken, map[string]any{"name": "finance"}, 400)
	e.json("PUT", api1+"/org/departments/"+hqID, owner.AccessToken, map[string]any{"name": "HQ", "parent_id": finID}, 400)
	e.json("PUT", api1+"/org/departments/ghost", owner.AccessToken, map[string]any{"name": "X"}, 404)

	// a job description narrows tools and keeps the department tag (the NAME) in sync on the agent
	e.json("PUT", api1+"/agents/accounting/job", member, map[string]any{}, 403)
	e.json("PUT", api1+"/agents/accounting/job", owner.AccessToken, map[string]any{"allowed_tools": []string{"crm"}}, 400)
	j := e.json("PUT", api1+"/agents/accounting/job", owner.AccessToken, map[string]any{
		"department_id": finID, "allowed_tools": []string{"ledger"}, "responsibilities": []string{"Close the month"},
		"escalation": map[string]any{"kind": "person", "id": "owner"},
		"schedule":   map[string]any{"enabled": true, "timezone": "Europe/Madrid", "days": []int{1, 2, 3, 4, 5}, "start": "09:00", "end": "18:00"},
		"budget_usd": 30}, 200)
	if j["department_id"] != finID {
		t.Fatalf("job: %v", j)
	}
	agent := e.json("GET", api1+"/agents/accounting", member, nil, 200)
	if agent["department"] != "Finance" {
		t.Fatalf("the agent carries the department name as a tag: %v", agent["department"])
	}
	e.json("PUT", api1+"/org/departments/"+finID, owner.AccessToken, map[string]any{"name": "Finance and Tax", "parent_id": hqID}, 200)
	if agent = e.json("GET", api1+"/agents/accounting", member, nil, 200); agent["department"] != "Finance and Tax" {
		t.Fatalf("rename must update the tag: %v", agent["department"])
	}
	av := e.json("GET", api1+"/agents/accounting/availability?at=2026-03-07T10:00:00Z", member, nil, 200) // a Saturday
	if av["open"] != false || av["next_open"] != "2026-03-09T08:00:00Z" {
		t.Fatalf("availability: %v", av)
	}
	e.json("GET", api1+"/agents/accounting/availability?at=yesterday", member, nil, 400)

	// the org chart
	c := e.json("GET", api1+"/org/chart", member, nil, 200)
	roots := c["roots"].([]any)
	if len(roots) != 1 || roots[0].(map[string]any)["name"] != "HQ" {
		t.Fatalf("chart: %v", c)
	}

	// delegation
	e.json("POST", api1+"/org/delegation/rules", owner.AccessToken, map[string]any{"from": map[string]any{"type": "any_agent"},
		"to": map[string]any{"type": "department", "id": finID}, "effect": "allow", "require_approval": true}, 201)
	e.json("POST", api1+"/org/delegation/rules", owner.AccessToken, map[string]any{"from": map[string]any{"type": "bogus"}, "to": map[string]any{"type": "any"}}, 400)
	d := e.json("POST", api1+"/org/delegation/check", member, map[string]any{"from": map[string]any{"kind": "agent", "id": "sales"}, "to": map[string]any{"kind": "agent", "id": "accounting"}}, 200)
	if d["allowed"] != true || d["needs_approval"] != true {
		t.Fatalf("decision: %v", d)
	}

	// industry templates: listed in the requested language, applied once, isolated per organization
	inds := e.json("GET", api1+"/org/industries?locale=pt", member, nil, 200)["items"].([]any)
	if len(inds) < 5 {
		t.Fatalf("industries: %d", len(inds))
	}
	e.json("POST", api1+"/org/industries/law_firm/apply", member, map[string]any{"locale": "en"}, 403)
	e.json("POST", api1+"/org/industries/nothing/apply", owner.AccessToken, map[string]any{"locale": "en"}, 404)
	res := e.json("POST", api1+"/org/industries/law_firm/apply", owner.AccessToken, map[string]any{"locale": "en", "skip_projects": true}, 200)["result"].(map[string]any)
	if len(res["agents"].([]any)) < 6 || len(res["departments"].([]any)) < 4 {
		t.Fatalf("apply: %v", res)
	}
	again := e.json("POST", api1+"/org/industries/law_firm/apply", owner.AccessToken, map[string]any{"locale": "en", "skip_projects": true}, 200)["result"].(map[string]any)
	if len(again["agents"].([]any)) != 0 || again["already_applied"] != true {
		t.Fatalf("second apply: %v", again)
	}
	if r := e.json("GET", api1+"/org/departments", rival.AccessToken, nil, 200)["items"].([]any); len(r) != 0 {
		t.Fatalf("org isolation: %v", r)
	}
}
