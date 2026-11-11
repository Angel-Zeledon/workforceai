package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/controls"
	"aiworkforce/backend/internal/customers"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/gateway"
	"aiworkforce/backend/internal/rituals"
)

// sealedWork is privateWork with every other path to a department's work mounted (final review D3, C1): questions,
// plan reviews, rehearsals, artifacts, customers, rituals, debates, initiatives and standing approvals. carla is a
// viewer outside the office (beto is the admin outside it, ana its head).
type sealedWork struct {
	*privateWork
	carla    string
	gw       *gateway.Gateway
	arts     *artifacts.Service
	cust     *customers.Service
	projects map[string]string // project id -> customer id (sealedCustomerWork)
	artLegal string
	artSales string
	rituals  *rituals.MemStore
}

// sealedCustomerWork reads the requests of the store and the projects registered in the fixture.
type sealedCustomerWork struct {
	store    application.Store
	projects *map[string]string
}

func (w sealedCustomerWork) Projects(context.Context, string) ([]customers.ProjectInfo, error) {
	var out []customers.ProjectInfo
	for id, cid := range *w.projects {
		out = append(out, customers.ProjectInfo{ID: id, Name: "proyecto " + id, Status: "draft", CustomerID: cid})
	}
	return out, nil
}
func (w sealedCustomerWork) ProjectCustomer(_ context.Context, _, id string) (string, error) {
	return (*w.projects)[id], nil
}
func (w sealedCustomerWork) Request(ctx context.Context, org, id string) (customers.RequestInfo, error) {
	r, err := w.store.GetRequest(ctx, org, id)
	return customers.RequestInfo{ID: r.ID, Text: r.Text, Status: string(r.Status), CreatedAt: r.CreatedAt}, err
}

// sealedHistory offers the fixture's requests and projects as earlier work without a customer.
type sealedHistory struct {
	store    application.Store
	projects *map[string]string
}

func (h sealedHistory) Project(context.Context, string, string) (customers.HistoryProjectInfo, error) {
	return customers.HistoryProjectInfo{}, domain.ErrNotFound
}
func (h sealedHistory) SetProjectCustomer(context.Context, string, string, string) error { return nil }
func (h sealedHistory) RequestFinished(context.Context, string, string) (bool, error) {
	return true, nil
}
func (h sealedHistory) Memory(context.Context, string, string) (customers.HistoryMemoryInfo, error) {
	return customers.HistoryMemoryInfo{}, domain.ErrNotFound
}
func (h sealedHistory) RetagMemories(context.Context, string, string, string, string, string) (int, error) {
	return 0, nil
}
func (h sealedHistory) Candidates(ctx context.Context, org string, _ int) ([]customers.ProjectInfo, []customers.RequestInfo, error) {
	var ps []customers.ProjectInfo
	for id := range *h.projects {
		ps = append(ps, customers.ProjectInfo{ID: id, Name: "proyecto " + id, Status: "draft"})
	}
	var rs []customers.RequestInfo
	for _, id := range []string{"req-legal", "req-sales"} {
		r, err := h.store.GetRequest(ctx, org, id)
		if err != nil {
			return nil, nil, err
		}
		rs = append(rs, customers.RequestInfo{ID: r.ID, Text: r.Text, Status: string(r.Status), CreatedAt: r.CreatedAt})
	}
	return ps, rs, nil
}

func newSealedWork(t *testing.T) *sealedWork {
	t.Helper()
	sw := &sealedWork{projects: map[string]string{}}
	rs := rituals.NewMemStore()
	p := newPrivateWork(t, withGateway(&sw.gw), func(o *opts) {
		o.questions, o.rehearsal, o.initiative, o.standing, o.customers = true, true, true, true, true
		o.debater = &debaterStub{}
		prev := o.deps
		o.deps = func(d *api.Deps) {
			prev(d)
			d.Rituals = rituals.New(rituals.Config{Store: rs, Core: d.Store, OrgID: d.Cfg.OrgID})
			sw.arts, sw.cust = d.Artifacts, d.Customers
			d.Customers.SetWork(sealedCustomerWork{store: d.Store, projects: &sw.projects})
			d.Customers.SetHistory(sealedHistory{store: d.Store, projects: &sw.projects})
		}
	})
	sw.privateWork, sw.rituals = p, rs
	sw.carla = p.e.member(p.owner, "carla-pw@example.com", auth.RoleViewer)
	ctx, org, now := application.WithOrg(context.Background(), p.owner.OrgID), p.owner.OrgID, time.Now().UTC()
	st := p.e.store
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, agent := range []string{"legal", "sales"} {
		must(st.CreateQuestion(ctx, org, application.Question{ID: "q-" + agent, TaskID: "task-" + agent, RequestID: "req-" + agent, AgentID: agent,
			Kind: "data", WhatHappened: "falta dato " + agent, Need: "dato " + agent, Options: []string{}, Status: application.QuestionOpen, CreatedAt: now}))
		must(st.AddRehearsalAction(ctx, org, domain.RehearsalAction{ID: "ra-" + agent, RequestID: "req-" + agent, TaskID: "task-" + agent, AgentID: agent,
			Tool: "email", Action: "send", Source: "gateway", Scope: "agent", WouldDecision: "approve", Risk: "high",
			Recipients: []string{agent + "@cliente.com"}, Flags: []string{}, Preview: map[string]string{"subject": "ensayo " + agent}, CreatedAt: now}))
		must(st.CreateInitiative(ctx, org, application.Initiative{ID: "ini-" + agent, AgentID: agent, Signal: "s", SignalKey: agent,
			Title: "idea " + agent, Body: "cuerpo " + agent, Prompt: "haz " + agent, Status: application.InitiativePending, CreatedAt: now}))
		a, err := sw.arts.Create(ctx, artifacts.Actor{Kind: artifacts.ActorAgent, ID: agent}, artifacts.CreateInput{Kind: artifacts.KindDoc,
			Title: "artefacto " + agent, TaskID: "task-" + agent})
		must(err)
		if agent == "legal" {
			sw.artLegal = a.ID
		} else {
			sw.artSales = a.ID
		}
	}
	must(st.CreateDebate(ctx, org, domain.Debate{ID: "deb-legal", Topic: "debate legal", RequestID: "req-legal", Options: []string{},
		Participants: []string{"legal", "sales"}, Turns: []domain.DebateTurn{{Seq: 1, AgentID: "legal", Kind: "position", Text: "postura legal"}},
		Status: "done", CreatedAt: now}))
	must(st.CreateDebate(ctx, org, domain.Debate{ID: "deb-free", Topic: "debate libre", Options: []string{},
		Participants: []string{"legal", "sales"}, Turns: []domain.DebateTurn{}, Status: "done", CreatedAt: now}))
	must(st.CreateDebate(ctx, org, domain.Debate{ID: "deb-sales", Topic: "debate sales", RequestID: "req-sales", Options: []string{},
		Participants: []string{"sales"}, Turns: []domain.DebateTurn{}, Status: "done", CreatedAt: now}))
	entry := func(agent string) rituals.Entry {
		return rituals.Entry{AgentID: agent, Name: agent, Role: agent, Done: []rituals.Item{{TaskID: "task-" + agent, Title: "tarea " + agent, Status: "done"}},
			Next: []rituals.Item{}, Blockers: []rituals.Item{}, Lines: []string{"terminó tarea " + agent}}
	}
	if _, err := rs.CreateRitual(ctx, org, rituals.Ritual{ID: "rit-1", Kind: rituals.KindRetro, Origin: rituals.OriginManual, Status: rituals.StatusHeld,
		Locale: "es", Entries: []rituals.Entry{entry("legal"), entry("sales")}, Absent: []rituals.Absent{},
		Summary: "el equipo y legal revisaron el contrato", SummarySource: rituals.SummaryRuntime, HeldAt: now}); err != nil {
		t.Fatal(err)
	}
	must(rs.CreateProposals(ctx, org, []rituals.Proposal{
		{ID: "prop-legal", RitualID: "rit-1", AgentID: "legal", Kind: rituals.ProposalSkillSuggestion, Signal: rituals.SignalReworks,
			Title: "propuesta legal", Text: "x", Evidence: []string{"tarea legal"}, Status: rituals.ProposalPending, CreatedAt: now},
		{ID: "prop-sales", RitualID: "rit-1", AgentID: "sales", Kind: rituals.ProposalSkillSuggestion, Signal: rituals.SignalReworks,
			Title: "propuesta sales", Text: "x", Evidence: []string{"tarea sales"}, Status: rituals.ProposalPending, CreatedAt: now}}))
	if _, err := sw.gw.Controls.SetSettings(ctx, org, controls.Settings{PlanReview: controls.PlanReviewAlways}, "test"); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"legal", "sales"} {
		if _, _, _, err := sw.gw.Begin(ctx, org, "req-"+agent, []application.PlanTaskInfo{{ID: "task-" + agent, Title: "tarea " + agent, AgentID: agent, DependsOn: []string{}}}); err != nil {
			t.Fatal(err)
		}
	}
	return sw
}

// outsiders are the people outside the private office: an admin and a viewer.
func (sw *sealedWork) outsiders() map[string]string {
	return map[string]string{"beto": sw.beto, "carla": sw.carla}
}

// listing checks that the outsiders' listing at path leaves out hidden and keeps shown, and that ana sees hidden.
func (sw *sealedWork) listing(t *testing.T, path string, hidden []string, shown string) {
	t.Helper()
	for who, tok := range sw.outsiders() {
		got := sw.body("GET", path, tok, 200)
		for _, h := range hidden {
			if strings.Contains(got, h) {
				t.Fatalf("%s: GET %s shows %q: %s", who, path, h, got)
			}
		}
		if shown != "" && !strings.Contains(got, shown) {
			t.Fatalf("%s: GET %s lacks the General work %q: %s", who, path, shown, got)
		}
	}
	if got := sw.body("GET", path, sw.ana, 200); !strings.Contains(got, hidden[0]) {
		t.Fatalf("ana: GET %s lacks %q: %s", path, hidden[0], got)
	}
}

// hiddenDetail checks that the outsiders get 404 at path and ana does not.
func (sw *sealedWork) hiddenDetail(t *testing.T, path string) {
	t.Helper()
	for who, tok := range sw.outsiders() {
		if c := sw.e.status("GET", api1+path, tok, nil); c != 404 {
			t.Fatalf("%s: GET %s = %d, want 404", who, path, c)
		}
	}
	if c := sw.e.status("GET", api1+path, sw.ana, nil); c != 200 {
		t.Fatalf("ana: GET %s = %d", path, c)
	}
}

// hiddenWrite checks that beto (an admin outside the office) gets 404 for a write on hidden work.
func (sw *sealedWork) hiddenWrite(t *testing.T, method, path string, body any) {
	t.Helper()
	if c := sw.e.status(method, api1+path, sw.beto, body); c != 404 {
		t.Fatalf("beto: %s %s = %d, want 404", method, path, c)
	}
}

func TestSealedQuestions(t *testing.T) {
	sw := newSealedWork(t)
	sw.listing(t, "/questions?status=all", []string{"q-legal", "falta dato legal"}, "q-sales")
	sw.hiddenDetail(t, "/questions/q-legal")
	sw.hiddenWrite(t, "POST", "/questions/q-legal/answer", map[string]any{"kind": "text", "text": "x"})
	sw.hiddenWrite(t, "POST", "/questions/q-legal/answer", map[string]any{"kind": "skip", "confirm": true})
	resp, raw := sw.e.do("POST", api1+"/questions/answer-batch", sw.beto, map[string]any{"ids": []string{"q-legal"}, "kind": "text", "text": "x"}, nil)
	var batch struct {
		Answered []string          `json:"answered"`
		Failed   map[string]string `json:"failed"`
	}
	if resp.StatusCode != 200 || json.Unmarshal(raw, &batch) != nil || len(batch.Answered) != 0 || batch.Failed["q-legal"] == "" {
		t.Fatalf("beto's batch = %d %s", resp.StatusCode, raw)
	}
	_, ghost := sw.e.do("POST", api1+"/questions/answer-batch", sw.beto, map[string]any{"ids": []string{"q-ghost"}, "kind": "text", "text": "x"}, nil)
	if strings.ReplaceAll(string(raw), "q-legal", "q-ghost") != string(ghost) {
		t.Fatalf("a hidden question answers %s, a missing one %s", raw, ghost)
	}
	if got := sw.body("GET", "/questions/q-legal", sw.ana, 200); !strings.Contains(got, `"status":"open"`) {
		t.Fatalf("the question changed: %s", got)
	}
}

func TestSealedPlanReviews(t *testing.T) {
	sw := newSealedWork(t)
	sw.listing(t, "/plan-reviews", []string{"req-legal", "pedido legal", "tarea legal"}, "req-sales")
}

func TestSealedRehearsal(t *testing.T) {
	sw := newSealedWork(t)
	sw.listing(t, "/rehearsal/actions", []string{"ra-legal", "legal@cliente.com", "ensayo legal"}, "ra-sales")
	sw.listing(t, "/rehearsal/actions?agent_id=legal", []string{"ra-legal"}, "")
	sw.hiddenWrite(t, "POST", "/rehearsal/actions/ra-legal/verdict", map[string]any{"verdict": "would_approve"})
	if got := sw.body("GET", "/rehearsal/actions", sw.ana, 200); strings.Contains(got, "would_approve") {
		t.Fatalf("the verdict was recorded: %s", got)
	}
	if c := sw.e.status("POST", api1+"/rehearsal/actions/ra-sales/verdict", sw.beto, map[string]any{"verdict": "would_approve"}); c != 200 {
		t.Fatalf("beto's verdict on General's rehearsal = %d", c)
	}
}

func TestSealedArtifacts(t *testing.T) {
	sw := newSealedWork(t)
	sw.listing(t, "/artifacts", []string{sw.artLegal, "artefacto legal"}, sw.artSales)
	sw.listing(t, "/artifacts?agent_id=legal", []string{sw.artLegal}, "")
	for _, sub := range []string{"", "/versions", "/versions/1", "/comments", "/proposals", "/links"} {
		sw.hiddenDetail(t, "/artifacts/"+sw.artLegal+sub)
	}
	// a viewer may not export at all (403 whatever the artifact); an admin outside the office gets 404
	sw.hiddenWrite(t, "GET", "/artifacts/"+sw.artLegal+"/export?format=docx", nil)
	if c := sw.e.status("GET", api1+"/artifacts/"+sw.artLegal+"/export?format=docx", sw.ana, nil); c != 200 {
		t.Fatalf("ana's export = %d", c)
	}
	for _, c := range []struct{ method, sub string }{
		{"POST", "/comments"}, {"POST", "/proposals"}, {"POST", "/ask"}, {"PATCH", ""}, {"DELETE", ""}, {"POST", "/versions"},
		{"POST", "/restore"}, {"POST", "/links"}, {"POST", "/refresh-dependencies"}, {"PUT", "/attachments/sales"},
	} {
		sw.hiddenWrite(t, c.method, "/artifacts/"+sw.artLegal+c.sub, map[string]any{"body": "x", "text": "x", "question": "x", "title": "x"})
	}
	sw.body("GET", "/artifacts/"+sw.artSales, sw.beto, 200)
}

func TestSealedCustomers(t *testing.T) {
	sw := newSealedWork(t)
	only := sw.project(t, "solo legal", func(int) string { return "legal" })
	general := sw.project(t, "solo ventas", func(int) string { return "sales" })
	c := sw.e.json("POST", api1+"/customers", sw.owner.AccessToken, map[string]any{"name": "Ferretería López"}, 201)
	cid := c["id"].(string)
	sw.projects[only], sw.projects[general] = cid, cid
	ctx := application.WithOrg(context.Background(), sw.owner.OrgID)
	for _, req := range []string{"req-legal", "req-sales"} {
		if err := sw.cust.LinkRequest(ctx, sw.owner.OrgID, req, cid); err != nil {
			t.Fatal(err)
		}
	}
	sw.listing(t, "/customers/"+cid, []string{"pedido legal", "req-legal", "proyecto " + only}, "pedido sales")
	if got := sw.body("GET", "/customers/"+cid, sw.beto, 200); !strings.Contains(got, "proyecto "+general) {
		t.Fatalf("beto lacks General's project: %s", got)
	}
}

func TestSealedCustomerCandidates(t *testing.T) {
	sw := newSealedWork(t)
	only := sw.project(t, "solo legal", func(int) string { return "legal" })
	sw.projects[only] = ""
	got := sw.body("GET", "/customers/history/candidates", sw.beto, 200)
	if strings.Contains(got, "req-legal") || strings.Contains(got, "proyecto "+only) || !strings.Contains(got, "req-sales") {
		t.Fatalf("beto's candidates = %s", got)
	}
	if got := sw.body("GET", "/customers/history/candidates", sw.ana, 200); !strings.Contains(got, "req-legal") || !strings.Contains(got, "proyecto "+only) {
		t.Fatalf("ana's candidates = %s", got)
	}
}

func TestSealedRituals(t *testing.T) {
	sw := newSealedWork(t)
	hidden := []string{"tarea legal", `"agent_id":"legal"`, "y legal revisaron"}
	sw.listing(t, "/rituals", hidden, "tarea sales")
	sw.listing(t, "/rituals/rit-1", append(hidden, "prop-legal"), "prop-sales")
	sw.listing(t, "/rituals/proposals", []string{"prop-legal", "propuesta legal"}, "prop-sales")
	sw.hiddenWrite(t, "POST", "/rituals/proposals/prop-legal/accept", nil)
	sw.hiddenWrite(t, "POST", "/rituals/proposals/prop-legal/dismiss", nil)
	if got := sw.body("GET", "/rituals/proposals", sw.ana, 200); strings.Contains(got, `"status":"accepted"`) || strings.Contains(got, `"status":"dismissed"`) {
		t.Fatalf("a proposal was resolved: %s", got)
	}
}

// Holding a ritual answers with the new ritual, redacted as GET /rituals/{id} redacts it (final re-review).
func TestSealedRitualRun(t *testing.T) {
	sw := newSealedWork(t)
	if got := sw.body("POST", "/rituals/standup/run", sw.beto, 201); strings.Contains(got, `"agent_id":"legal"`) {
		t.Fatalf("beto: POST /rituals/standup/run shows legal's entry: %s", got)
	}
	if got := sw.body("POST", "/rituals/retro/run", sw.ana, 201); !strings.Contains(got, `"agent_id":"legal"`) {
		t.Fatalf("ana: POST /rituals/retro/run lacks legal's entry: %s", got)
	}
}

func TestSealedDebates(t *testing.T) {
	sw := newSealedWork(t)
	sw.listing(t, "/debates", []string{"deb-legal", "deb-free", "postura legal"}, "deb-sales")
	sw.hiddenDetail(t, "/debates/deb-legal")
	sw.hiddenDetail(t, "/debates/deb-free")
	sw.body("GET", "/debates/deb-sales", sw.beto, 200)
}

func TestSealedInitiatives(t *testing.T) {
	sw := newSealedWork(t)
	sw.listing(t, "/initiatives", []string{"ini-legal", "haz legal"}, "ini-sales")
	sw.hiddenWrite(t, "POST", "/initiatives/ini-legal/accept", nil)
	sw.hiddenWrite(t, "POST", "/initiatives/ini-legal/dismiss", nil)
	if got := sw.body("GET", "/initiatives?status=pending", sw.ana, 200); !strings.Contains(got, "ini-legal") {
		t.Fatalf("the initiative was resolved: %s", got)
	}
}

func TestSealedStandingApprovals(t *testing.T) {
	sw := newSealedWork(t)
	only := sw.project(t, "solo legal", func(int) string { return "legal" })
	mixed := sw.project(t, "mixto", func(i int) string {
		if i == 0 {
			return "legal"
		}
		return "sales"
	})
	ctx, org, now := context.Background(), sw.owner.OrgID, time.Now().UTC()
	for _, s := range []application.StandingApproval{
		{ID: "sa-only", ProjectID: only, AgentID: "legal", Tool: "email", Action: "send"},
		{ID: "sa-legal", ProjectID: mixed, AgentID: "legal", Tool: "email", Action: "send"},
		{ID: "sa-sales", ProjectID: mixed, AgentID: "sales", Tool: "email", Action: "send"},
	} {
		s.CreatedBy, s.CreatedAt, s.ExpiresAt = "owner", now, now.Add(time.Hour)
		if err := sw.e.store.CreateStanding(ctx, org, s); err != nil {
			t.Fatal(err)
		}
	}
	sw.hiddenDetail(t, "/projects/"+only+"/standing-approvals")
	sw.listing(t, "/projects/"+mixed+"/standing-approvals", []string{"sa-legal"}, "sa-sales")
	sw.hiddenWrite(t, "DELETE", "/projects/"+only+"/standing-approvals/sa-only", nil)
	sw.hiddenWrite(t, "DELETE", "/projects/"+mixed+"/standing-approvals/sa-legal", nil)
	if got := sw.body("GET", "/projects/"+mixed+"/standing-approvals", sw.ana, 200); strings.Contains(got, "revoked") {
		t.Fatalf("a standing approval was revoked: %s", got)
	}
}

// I2: the agent's live activity names its current task: an outsider sees the agent and its state, not that.
func TestAgentActivityHiddenFromOutsiders(t *testing.T) {
	p := newPrivateWork(t)
	carla := p.e.member(p.owner, "carla-act@example.com", auth.RoleViewer)
	tid := "task-legal"
	if err := p.e.store.UpdateAgentState(context.Background(), p.owner.OrgID, "legal", domain.StateWorking, "Completó: tarea legal", &tid, 60); err != nil {
		t.Fatal(err)
	}
	p.e.json("PUT", api1+"/agents/legal/access", p.ana, map[string]any{"visibility": "directory"}, 200) // carla sees the agent too
	type agentOut struct {
		ID            string  `json:"id"`
		State         string  `json:"state"`
		Activity      string  `json:"activity"`
		CurrentTaskID *string `json:"current_task_id"`
		Progress      int     `json:"progress"`
	}
	legalOf := func(tok string) []agentOut {
		var all []agentOut
		if err := json.Unmarshal([]byte(p.body("GET", "/agents", tok, 200)), &all); err != nil {
			t.Fatal(err)
		}
		var one agentOut
		_ = json.Unmarshal([]byte(p.body("GET", "/agents/legal", tok, 200)), &one)
		var d struct {
			Agent agentOut `json:"agent"`
		}
		_ = json.Unmarshal([]byte(p.body("GET", "/agents/legal/detail", tok, 200)), &d)
		out := []agentOut{one, d.Agent}
		for _, a := range all {
			if a.ID == "legal" {
				out = append(out, a)
			}
		}
		if len(out) != 3 {
			t.Fatalf("legal is not listed: %v", all)
		}
		return out
	}
	for who, tok := range map[string]string{"beto": p.beto, "carla": carla} {
		for _, a := range legalOf(tok) {
			if a.Activity != "" || a.CurrentTaskID != nil || a.Progress != 0 || a.State != string(domain.StateWorking) {
				t.Fatalf("%s sees legal's work: %+v", who, a)
			}
		}
	}
	for _, a := range legalOf(p.ana) {
		if a.Activity != "Completó: tarea legal" || a.CurrentTaskID == nil || a.Progress != 60 {
			t.Fatalf("ana's view of legal: %+v", a)
		}
	}
}

// The WebSocket hello carries the agents as GET /agents shows them: an outsider never gets a private agent's activity
// there either (final re-review).
func TestWebSocketHelloHidesPrivateActivity(t *testing.T) {
	p := newPrivateWork(t)
	tid := "task-legal"
	if err := p.e.store.UpdateAgentState(context.Background(), p.owner.OrgID, "legal", domain.StateWorking, "Completó: tarea legal", &tid, 60); err != nil {
		t.Fatal(err)
	}
	hello := func(tok string) string {
		c, _, err := dialWS(t, p.e, tok, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		h := readFrame(t, c)
		if h.Type != domain.EvHello {
			t.Fatalf("first frame = %+v", h)
		}
		raw, _ := json.Marshal(h.Payload)
		return string(raw)
	}
	if got := hello(p.beto); strings.Contains(got, "tarea legal") || strings.Contains(got, "task-legal") {
		t.Fatalf("beto's hello shows legal's work: %s", got)
	}
	if got := hello(p.ana); !strings.Contains(got, "tarea legal") {
		t.Fatalf("ana's hello lacks legal's activity: %s", got)
	}
}
