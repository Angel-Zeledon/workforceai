package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/orgchart"
	"aiworkforce/backend/internal/roles"
)

// Organization model (migrations 750-754): the store contract, the agents.department tag and tenant isolation under RLS.
func TestOrgChartStorePostgres(t *testing.T) {
	env := testStore(t)
	st := env.Store
	oc := &OrgChartStore{S: st}
	ctx := context.Background()
	seedOrg(t, st, "org-oc1")
	seedOrg(t, st, "org-oc2")
	if err := st.Seed(ctx, domain.Organization{ID: "org-oc1", Name: "oc1", Slug: "oc1"}, roles.SeedAgents()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)

	// departments: round trip, upsert, unique name per organization (case-insensitive), delete
	d := orgchart.Department{ID: "hq", Name: "HQ", CostCenter: "CC-000", Head: orgchart.Ref{Kind: orgchart.KindAgent, ID: "sales"}, CreatedAt: now, UpdatedAt: now}
	if err := oc.PutDepartment(ctx, "org-oc1", d); err != nil {
		t.Fatal(err)
	}
	if err := oc.PutDepartment(ctx, "org-oc1", orgchart.Department{ID: "fin", Name: "Finance", ParentID: "hq", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := oc.PutDepartment(ctx, "org-oc1", orgchart.Department{ID: "dup", Name: "finance", CreatedAt: now, UpdatedAt: now}); err == nil {
		t.Fatal("a second department with the same name must violate the unique index")
	}
	d.Name = "Headquarters"
	if err := oc.PutDepartment(ctx, "org-oc1", d); err != nil {
		t.Fatal(err)
	}
	deps, err := oc.ListDepartments(ctx, "org-oc1")
	if err != nil || len(deps) != 2 || deps[0].Name != "Headquarters" || deps[0].Head.Kind != orgchart.KindAgent || deps[1].ParentID != "hq" {
		t.Fatalf("departments = %+v %v", deps, err)
	}
	if other, _ := oc.ListDepartments(ctx, "org-oc2"); len(other) != 0 {
		t.Fatalf("org-oc2 sees %d departments of org-oc1", len(other))
	}
	if err := oc.DeleteDepartment(ctx, "org-oc1", "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete missing = %v", err)
	}

	// job descriptions
	budget := 25.5
	j := orgchart.Job{AgentID: "sales", DepartmentID: "fin", ReportsTo: orgchart.Ref{Kind: orgchart.KindPerson, ID: "owner"}, Escalation: orgchart.Ref{Kind: orgchart.KindAgent, ID: "accounting"},
		Responsibilities: []string{"a", "b"}, AllowedTools: []string{"crm"}, BudgetUSD: &budget, UpdatedBy: "u1", UpdatedAt: now,
		Schedule: orgchart.Schedule{Enabled: true, Timezone: "Europe/Madrid", Days: []int{1, 2}, Start: "09:00", End: "18:00"}}
	if err := oc.PutJob(ctx, "org-oc1", j); err != nil {
		t.Fatal(err)
	}
	jobs, err := oc.ListJobs(ctx, "org-oc1")
	if err != nil || len(jobs) != 1 || jobs[0].BudgetUSD == nil || *jobs[0].BudgetUSD != 25.5 || jobs[0].Schedule.Timezone != "Europe/Madrid" ||
		len(jobs[0].AllowedTools) != 1 || jobs[0].Escalation.ID != "accounting" {
		t.Fatalf("jobs = %+v %v", jobs, err)
	}
	j.BudgetUSD = nil
	if err := oc.PutJob(ctx, "org-oc1", j); err != nil {
		t.Fatal(err)
	}
	if jobs, _ = oc.ListJobs(ctx, "org-oc1"); jobs[0].BudgetUSD != nil {
		t.Fatal("a nil budget must stay NULL")
	}
	if other, _ := oc.ListJobs(ctx, "org-oc2"); len(other) != 0 {
		t.Fatal("org-oc2 sees the jobs of org-oc1")
	}

	// the agents.department tag
	name := "Finance"
	if err := st.SetAgentDepartment(ctx, "org-oc1", "sales", &name); err != nil {
		t.Fatal(err)
	}
	a, err := st.GetAgent(ctx, "org-oc1", "sales")
	if err != nil || a.Department == nil || *a.Department != "Finance" {
		t.Fatalf("tag = %v %v", a.Department, err)
	}
	if err := st.SetAgentDepartment(ctx, "org-oc1", "sales", nil); err != nil {
		t.Fatal(err)
	}
	if a, _ = st.GetAgent(ctx, "org-oc1", "sales"); a.Department != nil {
		t.Fatal("nil must clear the tag")
	}
	if err := st.SetAgentDepartment(ctx, "org-oc1", "ghost", &name); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing agent = %v", err)
	}
	if err := st.SetAgentTools(ctx, "org-oc1", "sales", []string{"crm"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentResponsibilities(ctx, "org-oc1", "sales", []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if a, _ = st.GetAgent(ctx, "org-oc1", "sales"); len(a.Tools) != 1 || len(a.Responsibilities) != 1 {
		t.Fatalf("agent = %+v", a)
	}

	// placements, rules, applied templates
	if err := oc.PutPlacement(ctx, "org-oc1", orgchart.Placement{UserID: "u1", DepartmentID: "hq", Title: "CEO", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if ps, _ := oc.ListPlacements(ctx, "org-oc1"); len(ps) != 1 || ps[0].Title != "CEO" {
		t.Fatalf("placements = %+v", ps)
	}
	r := orgchart.Rule{ID: "r1", From: orgchart.Selector{Type: orgchart.SelDepartment, ID: "fin"}, To: orgchart.Selector{Type: orgchart.SelAnyAgent}, Effect: orgchart.EffectAllow,
		RequireApproval: true, Approver: orgchart.Ref{Kind: orgchart.KindPerson, ID: "owner"}, CreatedAt: now}
	if err := oc.PutRule(ctx, "org-oc1", r); err != nil {
		t.Fatal(err)
	}
	if rs, _ := oc.ListRules(ctx, "org-oc1"); len(rs) != 1 || rs[0].From.ID != "fin" || !rs[0].RequireApproval || rs[0].Approver.ID != "owner" {
		t.Fatalf("rules = %+v", rs)
	}
	if err := oc.DeleteRule(ctx, "org-oc1", "r1"); err != nil {
		t.Fatal(err)
	}
	if err := oc.DeleteRule(ctx, "org-oc1", "r1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete twice = %v", err)
	}
	if err := oc.PutApplied(ctx, "org-oc1", orgchart.Applied{Key: "law_firm", Version: 1, Locale: "pt", AppliedAt: now, Detail: map[string]any{"agents": []string{"law_litigator"}}}); err != nil {
		t.Fatal(err)
	}
	if ap, ok, err := oc.GetApplied(ctx, "org-oc1", "law_firm"); err != nil || !ok || ap.Locale != "pt" {
		t.Fatalf("applied = %+v %v %v", ap, ok, err)
	}
	if _, ok, _ := oc.GetApplied(ctx, "org-oc2", "law_firm"); ok {
		t.Fatal("org-oc2 sees an application of org-oc1")
	}
	if err := oc.DeleteDepartment(ctx, "org-oc1", "fin"); err != nil {
		t.Fatal(err)
	}
}
