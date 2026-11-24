package api_test

import (
	"strings"
	"testing"

	"aiworkforce/backend/internal/auth"
)

// officesWorld: Ventas (open, led by Ana, agent "sales") and Soporte (open, led by Marta), plus the owner.
func officesWorld(t *testing.T, multi bool) (e *env, owner, ana, marta, luis, ventas, soporte string) {
	t.Helper()
	deps := withOffices
	if multi {
		deps = withOfficesMulti
	}
	e = newEnv(t, opts{auth: true, orgchart: true, deps: deps})
	o := e.register("owner-ao@example.com", "OwnerAO")
	owner = o.AccessToken
	ana = e.member(o, "ana-ao@example.com", auth.RoleMember)
	marta = e.member(o, "marta-ao@example.com", auth.RoleMember)
	luis = e.member(o, "luis-ao@example.com", auth.RoleMember)
	anaID, martaID := e.userID(ana), e.userID(marta)
	v := e.json("POST", api1+"/org/departments", owner, map[string]any{"name": "Ventas", "head": map[string]any{"kind": "person", "id": anaID}}, 201)
	ventas = v["id"].(string)
	s := e.json("POST", api1+"/org/departments", owner, map[string]any{"name": "Soporte", "head": map[string]any{"kind": "person", "id": martaID}}, 201)
	soporte = s["id"].(string)
	e.json("PUT", api1+"/agents/sales/job", owner, map[string]any{"department_id": ventas}, 200)
	e.json("POST", api1+"/offices", owner, map[string]any{"department_id": ventas}, 201)
	e.json("POST", api1+"/offices", owner, map[string]any{"department_id": soporte}, 201)
	return
}

func TestAgentOfficesAPI(t *testing.T) {
	e, owner, ana, marta, luis, ventas, soporte := officesWorld(t, true)
	path := api1 + "/agents/sales/offices"

	got := e.json("GET", path, luis, nil, 200)
	if got["base_office"].(map[string]any)["id"] != ventas || got["base_office"].(map[string]any)["name"] != "Ventas" ||
		len(got["offices"].([]any)) != 0 || got["enabled"] != true {
		t.Fatalf("view = %v", got)
	}
	e.json("POST", path, luis, map[string]any{"office_id": soporte}, 403)  // not a lead
	e.json("POST", path, marta, map[string]any{"office_id": soporte}, 403) // leads the other office only
	e.json("POST", path, ana, map[string]any{}, 400)
	e.json("POST", path, ana, map[string]any{"office_id": "nowhere"}, 404)
	e.json("POST", path, ana, map[string]any{"office_id": ventas}, 400)
	v := e.json("POST", path, ana, map[string]any{"office_id": soporte}, 200)
	x := v["offices"].([]any)[0].(map[string]any)
	if x["office_id"] != soporte || x["status"] != "pending" || x["name"] != "Soporte" {
		t.Fatalf("grant = %v", v)
	}
	e.json("POST", path, ana, map[string]any{"office_id": soporte}, 200) // again: same
	e.json("POST", path+"/"+soporte+"/accept", ana, nil, 403)
	e.json("POST", path+"/"+soporte+"/accept", luis, nil, 403)
	e.json("POST", path+"/"+ventas+"/accept", marta, nil, 404) // never offered there
	v = e.json("POST", path+"/"+soporte+"/accept", marta, nil, 200)
	if x := v["offices"].([]any)[0].(map[string]any); x["status"] != "accepted" || x["accepted_by"] == "" {
		t.Fatalf("accept = %v", v)
	}
	if n := len(e.json("GET", path, owner, nil, 200)["offices"].([]any)); n != 1 {
		t.Fatalf("offices after accept = %d", n)
	}
	e.json("DELETE", path+"/"+soporte, luis, nil, 403)
	v = e.json("DELETE", path+"/"+soporte, ana, nil, 200)
	if len(v["offices"].([]any)) != 0 {
		t.Fatalf("remove = %v", v)
	}
	e.json("DELETE", path+"/"+soporte, ana, nil, 200) // nothing left: still fine
	e.json("GET", api1+"/agents/ghost/offices", owner, nil, 404)
}

func TestAgentOfficesAPIFlagOff(t *testing.T) {
	e, owner, ana, marta, _, _, soporte := officesWorld(t, false)
	path := api1 + "/agents/sales/offices"
	e.json("POST", path, ana, map[string]any{"office_id": soporte}, 403)
	e.json("POST", path, owner, map[string]any{"office_id": soporte}, 403)
	e.json("POST", path+"/"+soporte+"/accept", marta, nil, 403)
	got := e.json("GET", path, marta, nil, 200)
	if got["enabled"] != false || len(got["offices"].([]any)) != 0 {
		t.Fatalf("view = %v", got)
	}
}

// The same agent says the same base office in the directory, the position of the agent and the org chart.
func TestBaseOfficeIsTheSameEverywhere(t *testing.T) {
	e, owner, _, _, _, ventas, _ := officesWorld(t, false)
	dir := mustJSON(t, e.json("GET", api1+"/agents/directory", owner, nil, 200)["agents"])
	if !strings.Contains(dir, `"id":"sales"`) || !strings.Contains(dir, `"office_id":"`+ventas+`"`) {
		t.Fatalf("directory = %s", dir)
	}
	chart := mustJSON(t, e.json("GET", api1+"/org/chart", owner, nil, 200))
	if strings.Count(chart, `"office_id":"`+ventas+`"`) != 1 || !strings.Contains(chart, `"office_name":"Ventas"`) {
		t.Fatalf("chart does not say Ventas once: %s", chart)
	}
	if strings.Contains(chart, `"unassigned":[{`) && !strings.Contains(chart, `"office_id":"general"`) {
		t.Fatalf("an agent without a job must say General in the chart: %s", chart)
	}
	// positions.Service.Agent reads m.Agent(id).OfficeID from the same model (TestBaseOfficeViewIsTheModelsEffective)
}
