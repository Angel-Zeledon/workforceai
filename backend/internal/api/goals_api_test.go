package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/goals"
)

// withGoals serves the company goals (F1) with the departments of the env.
func withGoals(o *opts) {
	prev := o.deps
	o.deps = func(d *api.Deps) {
		prev(d)
		cfg := goals.Config{Store: goals.NewMemStore()}
		if d.Offices != nil {
			cfg.Offices = d.Offices.Model
		}
		d.Goals = goals.New(cfg)
		d.Orch.SetGoals(d.Goals)
		if d.Projects != nil {
			d.Projects.SetGoalCheck(d.Goals.CheckLink)
		}
	}
}

func goalID(t *testing.T, m map[string]any) string {
	t.Helper()
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("no goal id: %v", m)
	}
	return id
}

// F1 + D3: a private department's goal is its members'; a company goal is the administrators' to write and
// everybody's to read.
func TestGoalsAPISealAndPermissions(t *testing.T) {
	p := newPrivateWork(t, withGoals)
	e := p.e
	carla := e.member(p.owner, "carla-goal@example.com", auth.RoleMember)

	legal := goalID(t, e.json("POST", api1+"/goals", p.ana, map[string]any{"title": "Contratos al día", "office_id": p.dep}, 201))
	company := goalID(t, e.json("POST", api1+"/goals", p.owner.AccessToken, map[string]any{"title": "Subir ventas T4", "period": "2026-T4"}, 201))
	if c := e.status("POST", api1+"/goals", carla, map[string]any{"title": "De empresa"}); c != 403 {
		t.Fatalf("a member creates a company goal = %d", c)
	}
	if c := e.status("POST", api1+"/goals", p.beto, map[string]any{"title": "x", "office_id": p.dep}); c != 403 {
		t.Fatalf("an admin outside Legal creates its goal = %d", c)
	}

	if got := p.body("GET", "/goals", p.ana, 200); !strings.Contains(got, legal) || !strings.Contains(got, company) {
		t.Fatalf("ana's goals = %s", got)
	}
	for _, tok := range []string{p.beto, carla} {
		got := p.body("GET", "/goals", tok, 200)
		if strings.Contains(got, legal) || strings.Contains(got, "Contratos") || !strings.Contains(got, company) {
			t.Fatalf("goals of someone outside Legal = %s", got)
		}
		p.body("GET", "/goals/"+legal, tok, 404)
		if c := e.status("PATCH", api1+"/goals/"+legal, tok, map[string]any{"status": "achieved"}); c != 404 {
			t.Fatalf("outside Legal patches its goal = %d", c)
		}
		if c := e.status("POST", api1+"/requests", tok, map[string]any{"text": "algo", "goal_id": legal}); c != 400 {
			t.Fatalf("outside Legal links its goal = %d", c)
		}
	}
	if c := e.status("PATCH", api1+"/goals/"+company, carla, map[string]any{"status": "achieved"}); c != 403 {
		t.Fatalf("a member marks the company goal achieved = %d", c)
	}
	if got := e.json("PATCH", api1+"/goals/"+company, p.owner.AccessToken, map[string]any{"status": "achieved"}, 200); got["status"] != "achieved" {
		t.Fatalf("achieved = %v", got)
	}
	// an achieved goal takes no new work
	if c := e.status("POST", api1+"/requests", carla, map[string]any{"text": "algo", "goal_id": company}); c != 400 {
		t.Fatalf("link an achieved goal = %d", c)
	}
}

// F1: another organization's goal is refused (and nothing is created); a good one is stored with the request.
func TestGoalOfAnotherOrganizationIsRefused(t *testing.T) {
	p := newPrivateWork(t, withGoals)
	e := p.e
	other := e.register("other-goal@example.com", "Otra")
	foreign := goalID(t, e.json("POST", api1+"/goals", other.AccessToken, map[string]any{"title": "Objetivo ajeno"}, 201))
	if got := p.body("GET", "/goals", p.ana, 200); strings.Contains(got, foreign) {
		t.Fatalf("ana lists another organization's goal: %s", got)
	}
	p.body("GET", "/goals/"+foreign, p.ana, 404)
	before, _ := e.store.ListRequests(context.Background(), p.owner.OrgID)
	if c := e.status("POST", api1+"/requests", p.ana, map[string]any{"text": "pedido", "goal_id": foreign}); c != 400 {
		t.Fatalf("another org's goal = %d", c)
	}
	if c := e.status("POST", api1+"/projects/from-template", p.ana, map[string]any{"goal": "proyecto", "template_id": "tpl-generic", "goal_id": foreign}); c != 400 {
		t.Fatalf("project with another org's goal = %d", c)
	}
	if after, _ := e.store.ListRequests(context.Background(), p.owner.OrgID); len(after) != len(before) {
		t.Fatalf("a request was created: %d -> %d", len(before), len(after))
	}
	mine := goalID(t, e.json("POST", api1+"/goals", p.owner.AccessToken, map[string]any{"title": "Nuestro objetivo"}, 201))
	id := e.json("POST", api1+"/requests", p.ana, map[string]any{"text": "pedido con objetivo", "goal_id": mine}, 202)["request_id"].(string)
	if r, err := e.store.GetRequest(context.Background(), p.owner.OrgID, id); err != nil || r.GoalID != mine {
		t.Fatalf("stored = %+v %v", r, err)
	}
}

// F1: the report says which goal it contributed to, only to who sees the goal; the goal page lists the linked work
// the person sees; work without a goal answers as before.
func TestReportContributesToGoal(t *testing.T) {
	p := newPrivateWork(t, withGoals)
	e := p.e
	legal := goalID(t, e.json("POST", api1+"/goals", p.ana, map[string]any{"title": "Contratos al día", "office_id": p.dep}, 201))
	company := goalID(t, e.json("POST", api1+"/goals", p.owner.AccessToken, map[string]any{"title": "Subir ventas T4"}, 201))
	ctx, org, now := context.Background(), p.owner.OrgID, time.Now().UTC()
	// sales (General) worked for Legal's private goal and for the company's goal
	for id, goal := range map[string]string{"req-gl": legal, "req-gc": company} {
		if err := e.store.CreateRequest(ctx, org, domain.Request{ID: id, Text: "pedido " + id, Status: domain.RequestDone, CreatedAt: now, GoalID: goal}); err != nil {
			t.Fatal(err)
		}
		if err := e.store.CreateTask(ctx, org, domain.Task{ID: "t-" + id, RequestID: id, Title: "tarea", AgentID: "sales", Status: domain.TaskDone,
			DependsOn: []string{}, CreatedAt: now, FinishedAt: &now}); err != nil {
			t.Fatal(err)
		}
		if err := e.store.CreateReport(ctx, org, domain.Report{ID: "rep-" + id, RequestID: id, Title: "informe", Sections: []domain.Section{},
			Contributors: []string{"sales"}, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if got := p.body("GET", "/reports/rep-req-gl", p.ana, 200); !strings.Contains(got, `"goal":{`) || !strings.Contains(got, "Contratos al día") {
		t.Fatalf("ana's report = %s", got)
	}
	if got := p.body("GET", "/reports/rep-req-gl", p.beto, 200); strings.Contains(got, `"goal"`) || strings.Contains(got, legal) {
		t.Fatalf("beto's report shows Legal's goal: %s", got)
	}
	if got := p.body("GET", "/reports/rep-req-gc", p.beto, 200); !strings.Contains(got, "Subir ventas T4") {
		t.Fatalf("beto's report lacks the company goal: %s", got)
	}
	if got := p.body("GET", "/reports", p.beto, 200); strings.Contains(got, legal) || !strings.Contains(got, "Subir ventas T4") {
		t.Fatalf("beto's reports = %s", got)
	}
	if got := p.body("GET", "/requests/req-gl", p.beto, 200); strings.Contains(got, legal) {
		t.Fatalf("beto's request shows Legal's goal: %s", got)
	}
	if got := p.body("GET", "/requests", p.beto, 200); strings.Contains(got, legal) || !strings.Contains(got, company) {
		t.Fatalf("beto's requests = %s", got)
	}
	if got := p.body("GET", "/requests/req-gc", p.ana, 200); !strings.Contains(got, `"goal_id":"`+company) || !strings.Contains(got, "Subir ventas T4") {
		t.Fatalf("ana's request = %s", got)
	}
	// the goal page: its linked work
	if got := p.body("GET", "/goals/"+legal, p.ana, 200); !strings.Contains(got, "req-gl") || strings.Contains(got, "req-gc") {
		t.Fatalf("legal goal page = %s", got)
	}
	// no goal: the old shape
	for _, path := range []string{"/requests/req-sales", "/reports/rep-sales"} {
		if got := p.body("GET", path, p.ana, 200); strings.Contains(got, `"goal`) {
			t.Fatalf("%s without a goal = %s", path, got)
		}
	}
}
