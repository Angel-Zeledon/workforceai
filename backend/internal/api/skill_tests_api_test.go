package api_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// probeRuntime answers a test run: it asks for skill.load, then for tools that write, then answers with a cost.
type probeRuntime struct {
	fakeRuntime
	mu   sync.Mutex
	reqs []application.RunTaskRequest
}

func (p *probeRuntime) RunTask(_ context.Context, in application.RunTaskRequest) (application.RunTaskResponse, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, in)
	p.mu.Unlock()
	usage := application.Usage{Model: "sim", InputTokens: 120, OutputTokens: 30, CostUSD: 0.004}
	if len(in.LoadedSkills) == 0 {
		return application.RunTaskResponse{Output: domain.StructuredOutput{Summary: "cargando"}, Usage: usage,
			ToolRequests: []application.ToolRequest{
				{Tool: "skill", Action: "load", Args: map[string]any{"name": "weekly-report"}, Risk: "low"},
				{Tool: "email", Action: "send", Args: map[string]any{"to": "cliente@example.com"}, Risk: "high"},
				{Tool: "fs", Action: "write", Args: map[string]any{"path": "/Shared/x.md"}, Risk: "medium"},
			}}, nil
	}
	return application.RunTaskResponse{Output: domain.StructuredOutput{Summary: "informe hecho con: " + in.LoadedSkills[0].Text}, Usage: usage}, nil
}

// Testing a skill over HTTP: who may do what, a draft is tried without being activated, nothing with effects runs,
// the budget is charged, and another organization sees nothing.
func TestSkillTestsOverHTTP(t *testing.T) {
	rt := &probeRuntime{}
	e := newEnv(t, opts{auth: true, skills: true, runtime: rt})
	owner := e.register("owner-sktest@example.com", "OwnerSkTest")
	admin := e.member(owner, "admin-sktest@example.com", auth.RoleAdmin)
	member := e.member(owner, "member-sktest@example.com", auth.RoleMember)
	viewer := e.member(owner, "viewer-sktest@example.com", auth.RoleViewer)
	rival := e.register("rival-sktest@example.com", "RivalSkTest")
	base := api1 + "/skills"
	usage := func() float64 {
		var sum float64
		l, _ := e.store.ListUsage(context.Background(), owner.OrgID)
		for _, u := range l {
			sum += u.CostUSD
		}
		return sum
	}

	d := e.json("POST", base, member, map[string]any{"name": "weekly-report", "description": "Use for the weekly report", "instructions": "# Weekly\n\nSteps.\n"}, 201)
	id := str(d, "id")
	e.json("PUT", api1+"/agents/sales/skills/"+id, admin, nil, 200)
	tests := base + "/" + id + "/tests"

	// cases: a member writes them, a viewer only reads, anonymous nothing
	body := map[string]any{"prompt": "Prepara el informe semanal", "expect": "Incluye totales"}
	if c := e.status("POST", tests+"/cases", viewer, body); c != 403 {
		t.Fatalf("viewer saves a case = %d", c)
	}
	if r, _ := e.do("GET", tests, "", nil, nil); r.StatusCode != 401 {
		t.Fatalf("anonymous = %d", r.StatusCode)
	}
	c := e.json("POST", tests+"/cases", member, body, 201)
	cid := str(c, "id")
	if cid == "" || str(c, "title") == "" {
		t.Fatalf("case: %v", c)
	}
	if got := e.json("PUT", tests+"/cases/"+cid, member, map[string]any{"title": "Semanal", "prompt": "Otro pedido"}, 200); str(got, "title") != "Semanal" {
		t.Fatalf("update: %v", got)
	}
	if r, b := e.do("POST", tests+"/cases", member, map[string]any{"prompt": strings.Repeat("a", 3000)}, nil); r.StatusCode != 400 || !strings.Contains(string(b), "case_prompt_too_long") {
		t.Fatalf("long prompt = %d %s", r.StatusCode, b)
	}
	ov := e.json("GET", tests, viewer, nil, 200)
	if cs, _ := ov["cases"].([]any); len(cs) != 1 {
		t.Fatalf("overview: %v", ov)
	}
	if lv, _ := ov["last_by_version"].([]any); len(lv) != 0 {
		t.Fatalf("untested: %v", ov)
	}

	// candidates: the agent that has the skill (a draft can be tried)
	cands := e.list(tests+"/candidates", member)
	if len(cands) != 1 || str(cands[0], "id") != "sales" {
		t.Fatalf("candidates: %v", cands)
	}

	// running spends budget: only who may activate (skills:review)
	run := map[string]any{"case_id": cid, "agent_id": "sales"}
	for who, tok := range map[string]string{"member": member, "viewer": viewer} {
		if c := e.status("POST", tests+"/run", tok, run); c != 403 {
			t.Errorf("%s runs = %d", who, c)
		}
		if c := e.status("POST", tests+"/estimate", tok, run); c != 403 {
			t.Errorf("%s estimates = %d", who, c)
		}
	}
	if len(rt.reqs) != 0 {
		t.Fatal("a refused person must not reach the model")
	}
	est := e.json("POST", tests+"/estimate", admin, run, 200)
	if f, _ := est["estimate_usd"].(float64); f <= 0 {
		t.Fatalf("estimate before: %v", est)
	}
	if len(rt.reqs) != 0 {
		t.Fatal("estimating must not call the model")
	}
	before := usage()
	res := e.json("POST", tests+"/run", admin, run, 200)
	runs, _ := res["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("run: %v", res)
	}
	r := runs[0].(map[string]any)
	if str(r, "status") != "done" || r["version"].(float64) != 1 || r["used_skill"] != true || !strings.Contains(str(r, "output"), "Steps.") {
		t.Fatalf("result: %v", r)
	}
	if bl, _ := r["blocked"].([]any); len(bl) != 2 || bl[0] != "email.send" || bl[1] != "fs.write" {
		t.Fatalf("write tools must be refused: %v", r["blocked"])
	}
	if cost, _ := r["cost_usd"].(float64); cost < 0.0079 || cost > 0.0081 {
		t.Fatalf("real cost: %v", r["cost_usd"])
	}
	if after := usage(); after-before < 0.0079 {
		t.Fatalf("the run must be charged to the budget: %v -> %v", before, after)
	}
	for _, q := range rt.reqs {
		if len(q.Agent.Tools) != 0 {
			t.Fatalf("the agent must not be given tools in a test: %v", q.Agent.Tools)
		}
	}
	// still a draft, nothing activated
	if got := e.json("GET", base+"/"+id, viewer, nil, 200); str(got, "status") != "draft" || got["active_version"].(float64) != 0 {
		t.Fatalf("testing must not activate: %v", got)
	}
	// the reviewer sees it was tested; history of the case
	ov = e.json("GET", tests, admin, nil, 200)
	lv, _ := ov["last_by_version"].([]any)
	if len(lv) != 1 || lv[0].(map[string]any)["version"].(float64) != 1 {
		t.Fatalf("last tested: %v", ov["last_by_version"])
	}
	if h := e.list(tests+"/cases/"+cid+"/runs", viewer); len(h) != 1 {
		t.Fatalf("history: %v", h)
	}
	// an agent that does not have the skill, a ghost agent
	if r, b := e.do("POST", tests+"/run", admin, map[string]any{"case_id": cid, "agent_id": "hr"}, nil); r.StatusCode != 400 || !strings.Contains(string(b), "agent_not_eligible") {
		t.Fatalf("agent without the skill = %d %s", r.StatusCode, b)
	}
	if c := e.status("POST", tests+"/run", admin, map[string]any{"case_id": cid, "agent_id": "ghost"}); c != 404 {
		t.Fatalf("ghost agent = %d", c)
	}
	if c := e.status("POST", tests+"/run", admin, map[string]any{"case_id": "nope", "agent_id": "sales"}); c != 404 {
		t.Fatalf("unknown case = %d", c)
	}

	// another organization sees nothing of it
	for _, p := range []struct{ m, u string }{{"GET", tests}, {"GET", tests + "/candidates"}, {"GET", tests + "/cases/" + cid + "/runs"}} {
		if c := e.status(p.m, p.u, rival.AccessToken, nil); c != 404 {
			t.Errorf("rival %s %s = %d", p.m, p.u, c)
		}
	}
	if c := e.status("POST", tests+"/run", rival.AccessToken, run); c != 404 {
		t.Errorf("rival run = %d", c)
	}
	// delete a case: the history stays
	if c := e.status("DELETE", tests+"/cases/"+cid, member, nil); c != 204 {
		t.Fatalf("delete = %d", c)
	}
	if h := e.list(tests+"/cases/"+cid+"/runs", viewer); len(h) != 1 {
		t.Fatalf("history after deleting the case: %v", h)
	}
}
