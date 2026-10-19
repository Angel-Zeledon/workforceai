package orgchart

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/roles"
)

// fakeTemplates runs the existing project template validator over what an industry template creates.
type fakeTemplates struct {
	agentIDs []string
	created  []projects.Template
}

func (f *fakeTemplates) CreateTemplate(_ context.Context, in projects.TemplateInput) (projects.Template, error) {
	t := projects.Template{ID: fmt.Sprintf("tpl-%d", len(f.created)+1), Key: fmt.Sprintf("custom_%d", len(f.created)+1), Version: 1,
		Name: in.Name, Description: in.Description, Params: in.Params, Objectives: in.Objectives}
	for _, is := range projects.ValidateTemplate(t, f.agentIDs, projects.DefaultLimits()) {
		if is.Severity == "error" {
			return projects.Template{}, fmt.Errorf("template %q: %s %s", in.Name, is.Code, is.NodeID)
		}
	}
	f.created = append(f.created, t)
	return t, nil
}

type capSpy struct{ caps map[string]float64 }

func (c *capSpy) SetBudgetCap(_ context.Context, _ string, _ domain.BudgetScope, id string, v float64) error {
	if c.caps == nil {
		c.caps = map[string]float64{}
	}
	if v <= 0 {
		delete(c.caps, id)
	} else {
		c.caps[id] = v
	}
	return nil
}

func newSvc(t *testing.T) (*Service, *memory.Store, *capSpy) {
	t.Helper()
	st := memory.New()
	_ = st.Seed(context.Background(), domain.Organization{ID: "demo"}, roles.SeedAgents())
	caps := &capSpy{}
	svc := &Service{Store: NewMemoryStore(), Agents: st, Writer: st, Caps: caps, DefaultOrg: "demo",
		People: func(context.Context, string) ([]Person, error) {
			return []Person{{UserID: "u1", Name: "Ana Owner", Role: "owner"}, {UserID: "u2", Name: "Bruno", Role: "member"}}, nil
		}}
	return svc, st, caps
}

func agentByID(t *testing.T, st *memory.Store, id string) domain.Agent {
	t.Helper()
	list, _ := st.ListAgents(context.Background(), "demo")
	for _, a := range list {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("agent %s not found", id)
	return domain.Agent{}
}

func TestDepartmentsCycleRenameAndDelete(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newSvc(t)
	hq, err := svc.CreateDepartment(ctx, DepartmentInput{Name: "Headquarters", CostCenter: "CC-000"})
	if err != nil {
		t.Fatal(err)
	}
	fin, err := svc.CreateDepartment(ctx, DepartmentInput{Name: "Finance", ParentID: hq.ID, CostCenter: "CC-100", Head: Ref{Kind: KindPerson, ID: "u2"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateDepartment(ctx, DepartmentInput{Name: "finance"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("duplicate name must be rejected: %v", err)
	}
	if _, err := svc.CreateDepartment(ctx, DepartmentInput{Name: "X", Head: Ref{Kind: KindAgent, ID: "ghost"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown head must be rejected: %v", err)
	}
	// moving HQ under Finance would be a cycle
	if _, err := svc.UpdateDepartment(ctx, hq.ID, DepartmentInput{Name: hq.Name, ParentID: fin.ID}); !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle must be rejected: %v", err)
	}
	// an agent in Finance carries the department NAME as a tag; renaming keeps it in sync
	if _, err := svc.PutJob(ctx, "accounting", JobInput{DepartmentID: fin.ID, AllowedTools: []string{"ledger"}}); err != nil {
		t.Fatal(err)
	}
	if d := agentByID(t, st, "accounting").Department; d == nil || *d != "Finance" {
		t.Fatalf("tag after assigning: %v", d)
	}
	if _, err := svc.UpdateDepartment(ctx, fin.ID, DepartmentInput{Name: "Finance & Tax", ParentID: hq.ID, CostCenter: "CC-100"}); err != nil {
		t.Fatal(err)
	}
	if d := agentByID(t, st, "accounting").Department; d == nil || *d != "Finance & Tax" {
		t.Fatalf("tag after rename: %v", d)
	}
	// the parent cannot be deleted while it has children; the child can, and its agents become unassigned
	if err := svc.DeleteDepartment(ctx, hq.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("got %v", err)
	}
	if err := svc.DeleteDepartment(ctx, fin.ID); err != nil {
		t.Fatal(err)
	}
	if d := agentByID(t, st, "accounting").Department; d != nil {
		t.Fatalf("tag after delete: %v", *d)
	}
	if j, _ := svc.Job(ctx, "accounting"); j.DepartmentID != "" {
		t.Fatalf("job still points to the deleted department: %+v", j)
	}
	if err := svc.DeleteDepartment(ctx, fin.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestPutJobNarrowsAndValidates(t *testing.T) {
	ctx := context.Background()
	svc, st, caps := newSvc(t)
	// an accounting agent has fs, ledger, spreadsheet: it can be narrowed, never widened
	budget := 42.5
	j, err := svc.PutJob(ctx, "accounting", JobInput{
		Responsibilities: []string{"  Close the month ", "", "Reconcile banks"},
		AllowedTools:     []string{"ledger", "ledger", "fs"},
		Escalation:       Ref{Kind: KindPerson, ID: OwnerID},
		Schedule:         Schedule{Enabled: true, Timezone: "Europe/Madrid", Days: []int{5, 1, 3}, Start: "09:00", End: "18:00"},
		BudgetUSD:        &budget,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(j.AllowedTools) != 2 || j.Schedule.Days[0] != 1 || len(j.Responsibilities) != 2 || j.Responsibilities[0] != "Close the month" {
		t.Fatalf("normalization: %+v", j)
	}
	a := agentByID(t, st, "accounting")
	if len(a.Tools) != 2 || len(a.Responsibilities) != 2 {
		t.Fatalf("the agent record follows the job description: %+v", a)
	}
	if caps.caps["accounting"] != 42.5 {
		t.Fatalf("budget cap: %v", caps.caps)
	}
	// clearing the budget removes the cap
	if _, err := svc.PutJob(ctx, "accounting", JobInput{AllowedTools: []string{"fs"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := caps.caps["accounting"]; ok {
		t.Fatal("a nil budget must remove the cap")
	}

	bad := []struct {
		name string
		id   string
		in   JobInput
		want string
	}{
		{"widening tools", "accounting", JobInput{AllowedTools: []string{"crm"}}, "narrow"},
		{"unknown tool", "accounting", JobInput{AllowedTools: []string{"teleport"}}, "not registered"},
		{"bad schedule", "accounting", JobInput{Schedule: Schedule{Enabled: true, Timezone: "Nowhere/X", Days: []int{1}, Start: "09:00", End: "17:00"}}, "schedule"},
		{"negative budget", "accounting", JobInput{BudgetUSD: ptr(-1)}, "budget"},
		{"unknown department", "accounting", JobInput{DepartmentID: "nope"}, "department"},
		{"escalate to self", "accounting", JobInput{Escalation: Ref{Kind: KindAgent, ID: "accounting"}}, "itself"},
		{"unknown person", "accounting", JobInput{ReportsTo: Ref{Kind: KindPerson, ID: "stranger"}}, "unknown person"},
		{"too many responsibilities", "accounting", JobInput{Responsibilities: make([]string, 21)}, ""},
	}
	bad[len(bad)-1].in.Responsibilities = nil
	for i := 0; i < 21; i++ {
		bad[len(bad)-1].in.Responsibilities = append(bad[len(bad)-1].in.Responsibilities, fmt.Sprintf("task %d", i))
	}
	bad[len(bad)-1].want = "at most"
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.PutJob(ctx, c.id, c.in)
			if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v want invalid containing %q", err, c.want)
			}
		})
	}
	if _, err := svc.PutJob(ctx, "ghost", JobInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func ptr(v float64) *float64 { return &v }

func TestReportingLineCycles(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newSvc(t)
	// accounting -> analyst -> sales -> (person) u2; then u2 -> accounting would close the loop
	steps := []struct{ agent, boss string }{{"accounting", "analyst"}, {"analyst", "sales"}}
	for _, s := range steps {
		if _, err := svc.PutJob(ctx, s.agent, JobInput{ReportsTo: Ref{Kind: KindAgent, ID: s.boss}, AllowedTools: []string{"fs"}}); err != nil {
			t.Fatalf("%s -> %s: %v", s.agent, s.boss, err)
		}
	}
	if _, err := svc.PutJob(ctx, "sales", JobInput{ReportsTo: Ref{Kind: KindAgent, ID: "accounting"}, AllowedTools: []string{"fs"}}); !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("an agent cycle must be rejected: %v", err)
	}
	if _, err := svc.PutJob(ctx, "sales", JobInput{ReportsTo: Ref{Kind: KindPerson, ID: "u2"}, AllowedTools: []string{"fs"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PutPlacement(ctx, "u2", PlacementInput{Title: "Controller", ReportsTo: Ref{Kind: KindAgent, ID: "accounting"}}); !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("a person/agent cycle must be rejected: %v", err)
	}
	if _, err := svc.PutPlacement(ctx, "u2", PlacementInput{ReportsTo: Ref{Kind: KindPerson, ID: "u2"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("self report: %v", err)
	}
	if _, err := svc.PutPlacement(ctx, "u2", PlacementInput{Title: "Controller", ReportsTo: Ref{Kind: KindPerson, ID: "u1"}}); err != nil {
		t.Fatal(err)
	}
}

func TestDelegationRulesThroughTheService(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newSvc(t)
	fin, _ := svc.CreateDepartment(ctx, DepartmentInput{Name: "Finance", Head: Ref{Kind: KindPerson, ID: "u2"}})
	if _, err := svc.PutJob(ctx, "accounting", JobInput{DepartmentID: fin.ID, AllowedTools: []string{"fs"}}); err != nil {
		t.Fatal(err)
	}
	// nothing configured: allowed
	d, err := svc.CheckDelegation(ctx, Ref{Kind: KindPerson, ID: "u1"}, Ref{Kind: KindAgent, ID: "accounting"})
	if err != nil || !d.Allowed || d.NeedsApproval {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := svc.CreateRule(ctx, RuleInput{From: Selector{Type: SelAnyAgent}, To: Selector{Type: SelDepartment, ID: fin.ID}, Effect: EffectAllow, RequireApproval: true}); err != nil {
		t.Fatal(err)
	}
	d, _ = svc.CheckDelegation(ctx, Ref{Kind: KindAgent, ID: "sales"}, Ref{Kind: KindAgent, ID: "accounting"})
	if !d.Allowed || !d.NeedsApproval || d.Approver != (Ref{Kind: KindPerson, ID: "u2"}) {
		t.Fatalf("the head of the department approves: %+v", d)
	}
	// invalid rules
	for _, in := range []RuleInput{
		{From: Selector{Type: SelAgent, ID: "ghost"}, To: Selector{Type: SelAny}, Effect: EffectAllow},
		{From: Selector{Type: SelDepartment, ID: "ghost"}, To: Selector{Type: SelAny}, Effect: EffectAllow},
		{From: Selector{Type: SelPerson, ID: "ghost"}, To: Selector{Type: SelAny}, Effect: EffectAllow},
		{From: Selector{Type: SelAny}, To: Selector{Type: SelAny}, Effect: EffectDeny, RequireApproval: true},
	} {
		if _, err := svc.CreateRule(ctx, in); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%+v: %v", in, err)
		}
	}
	if _, err := svc.CheckDelegation(ctx, Ref{}, Ref{Kind: KindAgent, ID: "sales"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("%v", err)
	}
	rules, _ := svc.Rules(ctx)
	if err := svc.DeleteRule(ctx, rules[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteRule(ctx, rules[0].ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestBuildChartHierarchyAndReportingLines(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newSvc(t)
	hq, _ := svc.CreateDepartment(ctx, DepartmentInput{Name: "HQ", Head: Ref{Kind: KindPerson, ID: "u1"}})
	fin, _ := svc.CreateDepartment(ctx, DepartmentInput{Name: "Finance", ParentID: hq.ID, Head: Ref{Kind: KindAgent, ID: "accounting"}})
	sched := Schedule{Enabled: true, Timezone: "UTC", Days: []int{0, 1, 2, 3, 4, 5, 6}, Start: "00:00", End: "24:00"}
	if _, err := svc.PutJob(ctx, "accounting", JobInput{DepartmentID: fin.ID, ReportsTo: Ref{Kind: KindPerson, ID: "u1"}, AllowedTools: []string{"fs"}, Schedule: sched}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PutPlacement(ctx, "u1", PlacementInput{DepartmentID: hq.ID, Title: "CEO"}); err != nil {
		t.Fatal(err)
	}
	c, err := svc.Chart(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Roots) != 1 || c.Roots[0].ID != hq.ID || len(c.Roots[0].Children) != 1 || c.Roots[0].Children[0].ID != fin.ID {
		t.Fatalf("tree: %+v", c.Roots)
	}
	finNode := c.Roots[0].Children[0]
	if finNode.HeadName == "" || finNode.Agents != 1 || !finNode.Members[0].Head || finNode.Members[0].ReportsToName != "Ana Owner" {
		t.Fatalf("finance node: %+v", finNode)
	}
	if finNode.Members[0].Open == nil || !*finNode.Members[0].Open {
		t.Fatalf("a round-the-clock agent is open: %+v", finNode.Members[0])
	}
	if c.Roots[0].People != 1 || c.Roots[0].Members[0].Title != "CEO" || !c.Roots[0].Members[0].Head {
		t.Fatalf("root node: %+v", c.Roots[0])
	}
	if len(c.Unassigned) == 0 || c.People != 2 {
		t.Fatalf("unassigned agents and people: %+v", c)
	}
}

type recGate struct {
	decision application.GateDecision
	calls    int
}

func (g *recGate) GateTask(context.Context, string, domain.Task) application.GateDecision {
	g.calls++
	return g.decision
}
func (g *recGate) GateResolved(context.Context, string, domain.Task, string, bool) {}

func TestTaskGateQueuesOutsideWorkingHours(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newSvc(t)
	// Monday 2026-03-09 10:00 UTC: Madrid is 11:00 CET... use a fixed clock
	clock := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC) // Saturday
	svc.Now = func() time.Time { return clock }
	_, err := svc.PutJob(ctx, "accounting", JobInput{AllowedTools: []string{"fs"},
		Schedule: Schedule{Enabled: true, Timezone: "UTC", Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "17:00"}})
	if err != nil {
		t.Fatal(err)
	}
	inner := &recGate{}
	g := TaskGate{Inner: inner, Svc: svc}
	task := domain.Task{ID: "t1", AgentID: "accounting"}
	d := g.GateTask(ctx, "demo", task)
	if d.Action != application.GateHold || !strings.Contains(d.Reason, "2026-03-09T09:00:00Z") {
		t.Fatalf("Saturday: the work is queued until Monday 09:00: %+v", d)
	}
	// an agent without a schedule is never held
	if d := g.GateTask(ctx, "demo", domain.Task{ID: "t2", AgentID: "sales"}); d.Action != application.GateRun {
		t.Fatalf("%+v", d)
	}
	// Monday during working hours: runs. The cache is refreshed by the TTL, so move the clock past it.
	clock = time.Date(2026, 3, 9, 10, 0, 0, 0, time.UTC)
	if d := g.GateTask(ctx, "demo", task); d.Action != application.GateRun {
		t.Fatalf("Monday 10:00: %+v", d)
	}
	// the inner gate keeps priority: a project pause is not turned into a run
	inner.decision = application.GateDecision{Action: application.GateAbort, Reason: "cancelled"}
	if d := g.GateTask(ctx, "demo", task); d.Action != application.GateAbort {
		t.Fatalf("%+v", d)
	}
	// without an inner gate everything is a normal run when open
	if d := (TaskGate{Svc: svc}).GateTask(ctx, "demo", task); d.Action != application.GateRun {
		t.Fatalf("%+v", d)
	}
	// the wrapper forwards the optional interfaces to a gate that has none
	if (TaskGate{Inner: inner}).OwnsRequest(ctx, "demo", "r") {
		t.Fatal("no owner")
	}
}

func TestEditingTheJobTakesEffectBeforeTheCacheExpires(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newSvc(t)
	svc.Now = func() time.Time { return time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC) }
	g := TaskGate{Svc: svc}
	closed := Schedule{Enabled: true, Timezone: "UTC", Days: []int{1}, Start: "09:00", End: "10:00"}
	if _, err := svc.PutJob(ctx, "sales", JobInput{AllowedTools: []string{"fs"}, Schedule: closed}); err != nil {
		t.Fatal(err)
	}
	if d := g.GateTask(ctx, "demo", domain.Task{AgentID: "sales"}); d.Action != application.GateHold {
		t.Fatalf("%+v", d)
	}
	if _, err := svc.PutJob(ctx, "sales", JobInput{AllowedTools: []string{"fs"}}); err != nil { // schedule removed
		t.Fatal(err)
	}
	if d := g.GateTask(ctx, "demo", domain.Task{AgentID: "sales"}); d.Action != application.GateRun {
		t.Fatalf("a saved change must apply at once: %+v", d)
	}
}

func TestIndustryTemplatesApplyAndAreIdempotent(t *testing.T) {
	for _, key := range catalog.Industries().Keys() {
		for _, loc := range catalog.IndustryLocales {
			t.Run(key+"/"+loc, func(t *testing.T) {
				ctx := context.Background()
				svc, st, caps := newSvc(t)
				before, _ := st.ListAgents(ctx, "demo")
				ft := &fakeTemplates{}
				svc.Templates = ft
				view, _ := catalog.Industries().Industry(key, loc)
				// the sample projects reference the agents the template creates (plus the seeded ones)
				for _, a := range before {
					ft.agentIDs = append(ft.agentIDs, a.ID)
				}
				for _, a := range view.Agents {
					ft.agentIDs = append(ft.agentIDs, AgentID(key, a.Key))
				}
				res, err := svc.ApplyIndustry(ctx, key, ApplyInput{Locale: loc})
				if err != nil {
					t.Fatal(err)
				}
				if len(res.Agents) != len(view.Agents) || len(res.Departments) != len(view.Departments) || len(res.ProjectTemplates) != len(view.Projects) {
					t.Fatalf("result: %+v", res)
				}
				deps, _ := svc.Departments(ctx)
				if err := ValidateDepartments(deps); err != nil {
					t.Fatal(err)
				}
				jobs, _ := svc.Jobs(ctx)
				for _, j := range jobs {
					if err := j.Schedule.Validate(); err != nil || !j.Schedule.Enabled {
						t.Fatalf("%s schedule: %v", j.AgentID, err)
					}
					a := agentByID(t, st, j.AgentID)
					if a.Department == nil || len(a.Tools) != len(j.AllowedTools) || caps.caps[j.AgentID] <= 0 || j.Escalation.IsZero() {
						t.Fatalf("%s is not fully set up: %+v %+v", j.AgentID, a, j)
					}
					if !equalSets(RoleTools(a), a.Tools, true) {
						t.Fatalf("%s tools exceed the role: %v", j.AgentID, a.Tools)
					}
				}
				edges := map[string]string{}
				for _, j := range jobs {
					if !j.ReportsTo.IsZero() {
						edges["agent:"+j.AgentID] = j.ReportsTo.Key()
					}
				}
				if c := ReportingCycle(edges); c != nil {
					t.Fatalf("reporting cycle: %v", c)
				}
				rules, _ := svc.Rules(ctx)
				if len(rules) != len(view.Delegation) {
					t.Fatalf("rules: %d vs %d", len(rules), len(view.Delegation))
				}
				// every department head is an agent of the template
				for _, d := range deps {
					if !d.Head.IsZero() && d.Head.Kind != KindAgent {
						t.Fatalf("head: %+v", d)
					}
				}
				// the chart places every new agent inside the tree
				c, err := svc.Chart(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if got := countAgents(c.Roots); got != len(view.Agents) {
					t.Fatalf("agents in the tree: %d want %d", got, len(view.Agents))
				}
				// applying again creates nothing twice
				again, err := svc.ApplyIndustry(ctx, key, ApplyInput{Locale: loc})
				if err != nil {
					t.Fatal(err)
				}
				if len(again.Agents) != 0 || len(again.Departments) != 0 || len(again.ProjectTemplates) != 0 || !again.AlreadyApplied || len(again.AgentsExisting) != len(view.Agents) {
					t.Fatalf("second run: %+v", again)
				}
				if rules2, _ := svc.Rules(ctx); len(rules2) != len(rules) {
					t.Fatalf("rules duplicated: %d", len(rules2))
				}
				if len(ft.created) != len(view.Projects) {
					t.Fatalf("projects duplicated: %d", len(ft.created))
				}
			})
		}
	}
}

func equalSets(ceiling, tools []string, subset bool) bool {
	for _, t := range tools {
		found := false
		for _, c := range ceiling {
			found = found || c == t
		}
		if !found {
			return false
		}
	}
	return subset || len(ceiling) == len(tools)
}

func countAgents(nodes []DeptNode) int {
	n := 0
	for _, d := range nodes {
		n += d.Agents + countAgents(d.Children)
	}
	return n
}

func TestApplyIndustryRespectsTheOfficeCapAndUnknownKeys(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newSvc(t)
	if _, err := svc.ApplyIndustry(ctx, "no_such_industry", ApplyInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	// fill the office up to 2 below the cap: the template needs more room and must change nothing
	list, _ := st.ListAgents(ctx, "demo")
	for i := len(list); i < MaxAgents-2; i++ {
		_ = st.CreateAgent(ctx, "demo", domain.Agent{ID: fmt.Sprintf("filler_%d", i), Name: "F", Role: "assistant"})
	}
	if _, err := svc.ApplyIndustry(ctx, "law_firm", ApplyInput{}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("%v", err)
	}
	if deps, _ := svc.Departments(ctx); len(deps) != 0 {
		t.Fatalf("a refused template must not leave departments behind: %d", len(deps))
	}
}

// The sample projects must pass the EXISTING project template validator, in every language.
func TestIndustrySampleProjectsPassTheTemplateValidator(t *testing.T) {
	for _, key := range catalog.Industries().Keys() {
		for _, loc := range catalog.IndustryLocales {
			view, _ := catalog.Industries().Industry(key, loc)
			var ids []string
			for _, a := range view.Agents {
				ids = append(ids, AgentID(key, a.Key))
			}
			for _, p := range view.Projects {
				in := ProjectTemplateInput(key, p)
				tpl := projects.Template{ID: "tpl-x", Key: "custom_x", Version: 1, Name: in.Name, Description: in.Description, Params: in.Params, Objectives: in.Objectives}
				for _, is := range projects.ValidateTemplate(tpl, ids, projects.DefaultLimits()) {
					if is.Severity == "error" {
						t.Errorf("%s/%s/%s: %s %s", key, loc, p.Key, is.Code, is.NodeID)
					}
				}
			}
		}
	}
}

// A template that references an agent that does not exist must be caught by the same validator.
func TestTemplateValidatorRejectsUnknownAgentAndCycle(t *testing.T) {
	view, _ := catalog.Industries().Industry("accounting_firm", "en")
	in := ProjectTemplateInput("accounting_firm", view.Projects[0])
	tpl := projects.Template{ID: "tpl-x", Key: "custom_x", Version: 1, Name: in.Name, Params: in.Params, Objectives: in.Objectives}
	hasErr := func(ids []string, tp projects.Template) bool {
		for _, is := range projects.ValidateTemplate(tp, ids, projects.DefaultLimits()) {
			if is.Severity == "error" {
				return true
			}
		}
		return false
	}
	if !hasErr([]string{"someone_else"}, tpl) {
		t.Fatal("agents that do not exist must be reported")
	}
	nodes := tpl.Objectives[0].Workflows[0].Nodes
	nodes[0].Deps = []string{nodes[len(nodes)-1].Key}
	if !hasErr(nil, tpl) {
		t.Fatal("a dependency cycle must be reported")
	}
}
