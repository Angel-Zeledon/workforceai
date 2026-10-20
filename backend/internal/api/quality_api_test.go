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

// B11: the advisory quality score and the golden cases over HTTP.
func TestQualityScoreAndGoldenCasesOverHTTP(t *testing.T) {
	e := newEnv(t, opts{auth: true, quality: true})
	owner := e.register("owner-q@example.com", "OwnerQ")
	admin := e.member(owner, "admin-q@example.com", auth.RoleAdmin)
	viewer := e.member(owner, "viewer-q@example.com", auth.RoleViewer)
	rival := e.register("rival-q@example.com", "RivalQ")

	// No data: a methodology, no score, no invented precision.
	ov := e.json("GET", api1+"/quality/agents", viewer, nil, 200)
	m := ov["methodology"].(map[string]any)
	if m["advisory"] != true || m["window_days"].(float64) != 30 || len(m["components"].([]any)) != 5 {
		t.Fatalf("methodology: %v", m)
	}
	agents := ov["agents"].([]any)
	if len(agents) == 0 {
		t.Fatal("the ranking lists every agent")
	}
	first := agents[0].(map[string]any)
	if first["score"] != nil || first["status"] != "insufficient_data" || first["trend"] != "unknown" {
		t.Fatalf("an agent without data has no score: %v", first)
	}

	// Real signals: 12 approvals (10 approved) and 6 finished tasks for the sales agent.
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 12; i++ {
		st := domain.ApprovalApproved
		if i >= 10 {
			st = domain.ApprovalRejected
		}
		at := now.Add(-time.Hour)
		if err := e.store.CreateApproval(ctx, owner.OrgID, domain.Approval{ID: fmt.Sprintf("q-ap-%d", i), AgentID: "sales", Status: st, CreatedAt: now.Add(-2 * time.Hour), ResolvedAt: &at}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 6; i++ {
		fin := now.Add(-time.Hour)
		if err := e.store.CreateTask(ctx, owner.OrgID, domain.Task{ID: fmt.Sprintf("q-tk-%d", i), AgentID: "sales", Status: domain.TaskDone, CreatedAt: now.Add(-2 * time.Hour), FinishedAt: &fin}); err != nil {
			t.Fatal(err)
		}
	}
	got := e.json("GET", api1+"/agents/sales/quality?window_days=14", viewer, nil, 200)
	ag := got["agent"].(map[string]any)
	// approvals 10/12 (0.30) and tasks 6/6 (0.25): (0.30*0.8333 + 0.25) / 0.55 = 0.9091
	if ag["score"].(float64) != 91 || ag["level"] != "excellent" || got["methodology"].(map[string]any)["window_days"].(float64) != 14 {
		t.Fatalf("score: %v", got)
	}
	comps := ag["components"].([]any)
	if c := comps[0].(map[string]any); c["key"] != "approvals" || c["sample"].(float64) != 12 || c["included"] != true {
		t.Fatalf("approvals component: %v", c)
	}
	if r, _ := e.do("GET", api1+"/agents/ghost/quality", viewer, nil, nil); r.StatusCode != 404 {
		t.Fatalf("unknown agent = %d", r.StatusCode)
	}
	ranked := e.json("GET", api1+"/quality/agents", viewer, nil, 200)["agents"].([]any)
	if top := ranked[0].(map[string]any); top["agent_id"] != "sales" {
		t.Fatalf("the agent with a score ranks first: %v", top["agent_id"])
	}

	// Golden cases: viewers read, admins write; every write is audited and never changes autonomy.
	cases := api1 + "/agents/sales/golden-cases"
	body := map[string]any{"name": "Quote total", "input": "Quote 3 units at 100", "checks": []map[string]string{{"kind": "contains", "value": "300"}}}
	if s := e.status("POST", cases, viewer, body); s != 403 {
		t.Fatalf("viewer create = %d", s)
	}
	if s := e.status("POST", cases, admin, map[string]any{"name": "x", "input": "y", "checks": []map[string]string{}}); s != 400 {
		t.Fatalf("no checks = %d", s)
	}
	c := e.json("POST", cases, admin, body, 201)
	id := str(c, "id")
	if id == "" || c["active"] != true || c["agent_id"] != "sales" {
		t.Fatalf("created: %v", c)
	}
	if l := e.list(cases, viewer); len(l) != 1 {
		t.Fatalf("list: %v", l)
	}
	if s := e.status("POST", api1+"/golden-cases/"+id+"/runs", viewer, map[string]any{"passed": true}); s != 403 {
		t.Fatalf("viewer run = %d", s)
	}
	run := e.json("POST", api1+"/golden-cases/"+id+"/runs", admin, map[string]any{"output": "The total is 300 USD", "task_id": "tk-9"}, 201)
	if run["passed"] != true || run["checks_passed"].(float64) != 1 {
		t.Fatalf("run: %v", run)
	}
	if s := e.status("POST", api1+"/golden-cases/"+id+"/runs", admin, map[string]any{}); s != 400 {
		t.Fatalf("empty run = %d", s)
	}
	if runs := e.list(api1+"/golden-cases/"+id+"/runs", viewer); len(runs) != 1 {
		t.Fatalf("runs: %v", runs)
	}
	// The recorded output text is not echoed or stored anywhere in the run.
	if _, raw := e.do("GET", api1+"/golden-cases/"+id+"/runs", viewer, nil, nil); strings.Contains(string(raw), "300 USD") {
		t.Fatalf("run leaks the output: %s", raw)
	}
	upd := map[string]any{"name": "Quote total v2", "input": "Quote 3 units at 100", "checks": body["checks"], "active": false}
	if u := e.json("PUT", api1+"/golden-cases/"+id, admin, upd, 200); u["active"] != false || u["name"] != "Quote total v2" {
		t.Fatalf("update: %v", u)
	}
	if s := e.status("POST", api1+"/golden-cases/"+id+"/runs", admin, map[string]any{"passed": true}); s != 409 {
		t.Fatalf("inactive case run = %d", s)
	}

	// Another organization neither sees nor touches them.
	if s := e.status("PUT", api1+"/golden-cases/"+id, rival.AccessToken, upd); s != 404 {
		t.Fatalf("rival update = %d", s)
	}
	if s := e.status("GET", api1+"/golden-cases/"+id+"/runs", rival.AccessToken, nil); s != 404 {
		t.Fatalf("rival runs = %d", s)
	}
	if l := e.list(cases, rival.AccessToken); len(l) != 0 {
		t.Fatalf("rival sees cases: %v", l)
	}

	if s := e.status("DELETE", api1+"/golden-cases/"+id, viewer, nil); s != 403 {
		t.Fatalf("viewer delete = %d", s)
	}
	if s := e.status("DELETE", api1+"/golden-cases/"+id, admin, nil); s != 204 {
		t.Fatalf("delete = %d", s)
	}
	if s := e.status("DELETE", api1+"/golden-cases/"+id, admin, nil); s != 404 {
		t.Fatalf("second delete = %d", s)
	}

	// Audit: created, run_recorded, updated, deleted, and the agent's autonomy is untouched.
	_, raw := e.do("GET", api1+"/audit?type=quality.*&limit=50", admin, nil, nil)
	for _, a := range []string{"quality.case_created", "quality.run_recorded", "quality.case_updated", "quality.case_deleted"} {
		if !strings.Contains(string(raw), a) {
			t.Errorf("audit lacks %s: %s", a, raw)
		}
	}
	if a := e.json("GET", api1+"/agents/sales", viewer, nil, 200); str(a, "autonomy") == "" {
		t.Fatalf("agent: %v", a)
	}
}
