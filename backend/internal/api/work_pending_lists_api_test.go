package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/rituals"
)

// D3 pending items, area A: the listings, aggregates and actions that reach a private department's work.

// countingProjects counts the reads of whole projects.
type countingProjects struct {
	*projects.MemStore
	gets atomic.Int64
}

func (c *countingProjects) Get(ctx context.Context, org, id string) (projects.Record, error) {
	c.gets.Add(1)
	return c.MemStore.Get(ctx, org, id)
}

// withProjectStore serves the projects over st.
func withProjectStore(t *testing.T, st *countingProjects) func(*opts) {
	return func(o *opts) {
		prev := o.deps
		o.deps = func(d *api.Deps) {
			prev(d)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			d.Projects = projects.New(ctx, projects.Config{Store: st, Orch: d.Orch, Core: d.Store, Approvals: d.Approvals, Rec: d.Rec,
				Runtime: fakeRuntime{}, Sink: d.Artifacts, OrgID: d.Cfg.OrgID, Poll: 10 * time.Millisecond, Log: d.Log})
		}
	}
}

// 3. GET /projects reads each project once when the person sees every agent (no second read to redact it).
func TestProjectsListReadsOnceWhenNothingIsHidden(t *testing.T) {
	st := &countingProjects{MemStore: projects.NewMemStore()}
	p := newPrivateWork(t, withProjectStore(t, st))
	for _, goal := range []string{"uno", "dos", "tres"} {
		p.project(t, goal, func(int) string { return "sales" })
	}
	st.gets.Store(0)
	p.body("GET", "/projects", p.beto, 200) // an admin outside Legal: legal's work is hidden
	hidden := st.gets.Load()
	st.gets.Store(0)
	if got := p.body("GET", "/projects", p.ana, 200); !strings.Contains(got, "uno") { // ana sees every agent's work
		t.Fatalf("ana's projects = %s", got)
	}
	if open := st.gets.Load(); open != 3 || hidden <= open {
		t.Fatalf("project reads: %d with hidden agents, %d without (want 3)", hidden, open)
	}
}

// 13. A question cannot be handed to an agent whose work the person does not see, nor is one offered.
func TestQuestionReassignLeavesOutHiddenAgents(t *testing.T) {
	sw := newSealedWork(t)
	var q struct {
		Candidates []struct {
			ID string `json:"id"`
		} `json:"reassign_candidates"`
	}
	if err := json.Unmarshal([]byte(sw.body("GET", "/questions/q-sales", sw.beto, 200)), &q); err != nil {
		t.Fatal(err)
	}
	for _, c := range q.Candidates {
		if c.ID == "legal" {
			t.Fatalf("beto is offered legal: %+v", q.Candidates)
		}
	}
	if len(q.Candidates) == 0 {
		t.Fatal("no candidates at all")
	}
	if c := sw.e.status("POST", api1+"/questions/q-sales/answer", sw.beto, map[string]any{"kind": "reassign", "agent_id": "legal"}); c != 404 {
		t.Fatalf("beto hands q-sales to legal = %d, want 404", c)
	}
	if got := sw.body("GET", "/questions/q-sales", sw.ana, 200); !strings.Contains(got, `"status":"open"`) {
		t.Fatalf("the question changed: %s", got)
	}
}

// 14. A debate cannot be held with an agent whose work the person does not see.
func TestDebateWithHiddenAgentIsRefused(t *testing.T) {
	sw := newSealedWork(t)
	body := map[string]any{"topic": "¿precio o plazo?", "agent_ids": []string{"legal", "sales"}}
	if c := sw.e.status("POST", api1+"/debates", sw.beto, body); c != 404 {
		t.Fatalf("beto's debate with legal = %d, want 404", c)
	}
	if c := sw.e.status("POST", api1+"/debates", sw.ana, body); c != 201 {
		t.Fatalf("ana's debate with legal = %d", c)
	}
}

// 15. The aggregates count only the work the person sees: rehearsals per agent, the customer district and the
// metrics of GET /agents.
func TestAggregatesCountOnlyVisibleWork(t *testing.T) {
	sw := newSealedWork(t)
	type summary struct {
		AgentID string `json:"agent_id"`
		Actions int    `json:"actions"`
	}
	rehearsals := func(tok string) map[string]int {
		var out struct {
			Agents []summary `json:"agents"`
		}
		if err := json.Unmarshal([]byte(sw.body("GET", "/rehearsal/agents", tok, 200)), &out); err != nil {
			t.Fatal(err)
		}
		m := map[string]int{}
		for _, a := range out.Agents {
			m[a.AgentID] = a.Actions
		}
		return m
	}
	if got := rehearsals(sw.beto); got["legal"] != 0 || got["sales"] != 1 {
		t.Fatalf("beto's rehearsals per agent = %v", got)
	}
	if got := rehearsals(sw.ana); got["legal"] != 1 {
		t.Fatalf("ana's rehearsals per agent = %v", got)
	}

	// the district: a failed request of legal blocks the customer only for whoever sees it
	ctx, org, now := context.Background(), sw.owner.OrgID, time.Now().UTC()
	if err := sw.e.store.CreateRequest(ctx, org, domain.Request{ID: "req-legal-fail", Text: "pedido fallido legal", Status: domain.RequestFailed, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := sw.e.store.CreateTask(ctx, org, domain.Task{ID: "task-legal-fail", RequestID: "req-legal-fail", Title: "t", AgentID: "legal",
		Status: domain.TaskFailed, DependsOn: []string{}, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	c := sw.e.json("POST", api1+"/customers", sw.owner.AccessToken, map[string]any{"name": "Ferretería López"}, 201)
	cid := c["id"].(string)
	actx := application.WithOrg(context.Background(), org)
	for _, req := range []string{"req-legal-fail", "req-sales"} {
		if err := sw.cust.LinkRequest(actx, org, req, cid); err != nil {
			t.Fatal(err)
		}
	}
	district := func(tok string) (status string, failed int) {
		var d struct {
			Customers []struct {
				Customer struct {
					ID string `json:"id"`
				} `json:"customer"`
				Status string `json:"status"`
				Failed int    `json:"failed"`
			} `json:"customers"`
		}
		if err := json.Unmarshal([]byte(sw.body("GET", "/customers/district", tok, 200)), &d); err != nil {
			t.Fatal(err)
		}
		for _, x := range d.Customers {
			if x.Customer.ID == cid {
				return x.Status, x.Failed
			}
		}
		t.Fatalf("customer %s not in the district", cid)
		return "", 0
	}
	if st, n := district(sw.beto); n != 0 || st == "blocked" {
		t.Fatalf("beto's district: %s, %d failed", st, n)
	}
	if st, n := district(sw.ana); n != 1 || st != "blocked" {
		t.Fatalf("ana's district: %s, %d failed", st, n)
	}

	// GET /agents: the metrics of an agent whose work the person does not see
	metrics := func(tok string) int {
		var all []struct {
			ID      string `json:"id"`
			Metrics struct {
				TasksCompleted int `json:"tasks_completed"`
			} `json:"metrics"`
		}
		if err := json.Unmarshal([]byte(sw.body("GET", "/agents", tok, 200)), &all); err != nil {
			t.Fatal(err)
		}
		for _, a := range all {
			if a.ID == "legal" {
				return a.Metrics.TasksCompleted
			}
		}
		return -1
	}
	if n := metrics(sw.beto); n > 0 {
		t.Fatalf("beto sees legal's metrics: %d tasks", n)
	}
	if n := metrics(sw.ana); n != 1 {
		t.Fatalf("ana's view of legal's metrics: %d tasks", n)
	}
}

// 16. GET /project-batches (and by id) leaves out a batch that is only a private department's work.
func TestBatchReadsFollowTheProjectRule(t *testing.T) {
	st := &countingProjects{MemStore: projects.NewMemStore()}
	p := newPrivateWork(t, withProjectStore(t, st))
	only := p.project(t, "solo legal", func(int) string { return "legal" })
	general := p.project(t, "solo ventas", func(int) string { return "sales" })
	ctx, org, now := context.Background(), p.owner.OrgID, time.Now().UTC()
	for id, project := range map[string]string{"bat-legal": only, "bat-sales": general} {
		if err := st.PutBatch(ctx, projects.Batch{ID: id, OrgID: org, Name: "lote " + id, TemplateID: "tpl-generic", Control: "active", Status: "running",
			Items: []projects.BatchItem{{CustomerID: "c1", CustomerName: "Cliente", ProjectID: project, State: projects.ItemLaunched}}, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	p.body("GET", "/project-batches/bat-legal", p.beto, 404)
	p.body("GET", "/project-batches/bat-sales", p.beto, 200)
	p.body("GET", "/project-batches/bat-legal", p.ana, 200)
	if got := p.body("GET", "/project-batches", p.beto, 200); strings.Contains(got, "bat-legal") || !strings.Contains(got, "bat-sales") {
		t.Fatalf("beto's batches = %s", got)
	}
	if got := p.body("GET", "/project-batches", p.ana, 200); !strings.Contains(got, "bat-legal") {
		t.Fatalf("ana's batches = %s", got)
	}
}

// 7. The listings with a limit and no cursor (rehearsals, debates, initiatives, retro proposals) leave out what the
// person does not see before cutting at the limit.
func TestLimitedListingsCutAfterLeavingOut(t *testing.T) {
	sw := newSealedWork(t)
	ctx, org, later := application.WithOrg(context.Background(), sw.owner.OrgID), sw.owner.OrgID, time.Now().UTC().Add(time.Hour)
	st := sw.e.store
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.AddRehearsalAction(ctx, org, domain.RehearsalAction{ID: "ra-legal-2", RequestID: "req-legal", TaskID: "task-legal", AgentID: "legal",
		Tool: "email", Action: "send", Source: "gateway", Scope: "agent", WouldDecision: "approve", Risk: "high", Recipients: []string{}, Flags: []string{},
		Preview: map[string]string{}, CreatedAt: later}))
	must(st.CreateDebate(ctx, org, domain.Debate{ID: "deb-legal-2", Topic: "otro debate legal", RequestID: "req-legal", Options: []string{},
		Participants: []string{"legal"}, Turns: []domain.DebateTurn{}, Status: "done", CreatedAt: later}))
	must(st.CreateInitiative(ctx, org, application.Initiative{ID: "ini-legal-2", AgentID: "legal", Signal: "s", SignalKey: "legal-2",
		Title: "otra idea legal", Body: "b", Prompt: "p", Status: application.InitiativePending, CreatedAt: later}))
	must(sw.rituals.CreateProposals(ctx, org, []rituals.Proposal{{ID: "prop-legal-2", RitualID: "rit-1", AgentID: "legal",
		Kind: rituals.ProposalSkillSuggestion, Signal: rituals.SignalReworks, Title: "otra propuesta legal", Text: "x", Evidence: []string{},
		Status: rituals.ProposalPending, CreatedAt: later}}))
	for path, want := range map[string]string{
		"/rehearsal/actions?limit=1": "ra-sales", "/debates?limit=1": "deb-sales", "/initiatives?limit=1": "ini-sales",
		"/rituals/proposals?limit=1": "prop-sales",
	} {
		if got := sw.body("GET", path, sw.beto, 200); !strings.Contains(got, want) || strings.Contains(got, "legal-2") {
			t.Fatalf("beto: GET %s = %s", path, got)
		}
		if got := sw.body("GET", path, sw.ana, 200); !strings.Contains(got, "legal-2") {
			t.Fatalf("ana: GET %s = %s", path, got)
		}
	}
}

// A visible task cannot be handed to an agent whose work the person does not see either.
func TestTaskReassignToHiddenAgentIsRefused(t *testing.T) {
	sw := newSealedWork(t)
	if c := sw.e.status("POST", api1+"/tasks/task-sales/reassign", sw.beto, map[string]any{"agent_id": "legal"}); c != 404 {
		t.Fatalf("beto hands task-sales to legal = %d, want 404", c)
	}
	if got := sw.body("GET", "/tasks/task-sales", sw.ana, 200); !strings.Contains(got, `"agent_id":"sales"`) {
		t.Fatalf("the task changed hands: %s", got)
	}
}
