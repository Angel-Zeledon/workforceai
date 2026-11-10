package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	longmem "aiworkforce/backend/internal/memory"
	"aiworkforce/backend/internal/sandbox"
)

// privateWork (D3): a private office "Legal" led by ana with the agent legal; beto is an admin outside it. The agent
// sales stays in General. The work of both agents is seeded straight into the store.
type privateWork struct {
	e         *env
	owner     *auth.Session
	ana, beto string
}

func newPrivateWork(t *testing.T, more ...func(*opts)) *privateWork {
	t.Helper()
	o := opts{auth: true, orgchart: true, away: true, code: true, v2: true, quality: true, ws: true, trust: true, deps: func(d *api.Deps) {
		withOffices(d)
		docs := longmem.NewMemStore()
		d.Memory = longmem.New(longmem.Config{Docs: docs, Retriever: &longmem.FTS{Docs: docs}})
		d.Commands = &api.CommandsDeps{}
	}}
	for _, f := range more {
		f(&o)
	}
	e := newEnv(t, o)
	owner := e.register("owner-pw@example.com", "OwnerPW")
	p := &privateWork{e: e, owner: owner,
		ana:  e.member(owner, "ana-pw@example.com", auth.RoleAdmin),
		beto: e.member(owner, "beto-pw@example.com", auth.RoleAdmin)}
	anaID := e.userID(p.ana)
	dep := e.json("POST", api1+"/org/departments", owner.AccessToken, map[string]any{"name": "Legal", "head": map[string]any{"kind": "person", "id": anaID}}, 201)
	depID := dep["id"].(string)
	e.json("PUT", api1+"/agents/legal/job", owner.AccessToken, map[string]any{"department_id": depID}, 200)
	e.json("POST", api1+"/offices", owner.AccessToken, map[string]any{"department_id": depID, "visibility": "private"}, 201)

	ctx, org, now := context.Background(), owner.OrgID, time.Now().UTC()
	st := e.store
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, agent := range []string{"legal", "sales"} {
		req := "req-" + agent
		must(st.CreateRequest(ctx, org, domain.Request{ID: req, Text: "pedido " + agent, Status: domain.RequestDone, CreatedAt: now}))
		must(st.CreateTask(ctx, org, domain.Task{ID: "task-" + agent, RequestID: req, Title: "tarea " + agent, AgentID: agent,
			Status: domain.TaskDone, DependsOn: []string{}, CreatedAt: now, FinishedAt: &now, Output: &domain.StructuredOutput{Summary: "resumen " + agent}}))
		must(st.CreateReport(ctx, org, domain.Report{ID: "rep-" + agent, RequestID: req, Title: "informe " + agent,
			Sections: []domain.Section{}, Contributors: []string{agent}, CreatedAt: now}))
		id := agent
		must(st.AddActivity(ctx, org, domain.ActivityItem{ID: "act-" + agent, TS: now, AgentID: &id, Kind: domain.EvTaskCompleted, Text: "terminó " + agent}))
		must(st.CreateConversation(ctx, org, domain.Conversation{ID: "conv-" + agent, Title: "conversación " + agent,
			Participants: []string{"user", agent}, RequestID: &req, LastMessageAt: now}))
		must(e.code.Store.CreateRun(ctx, sandbox.Run{ID: "run-" + agent, OrgID: org, AgentID: agent, TaskID: "task-" + agent, RequestID: req,
			Language: "python", Code: "print(1)", Purpose: "código " + agent, Status: sandbox.StatusSucceeded, CreatedAt: now}))
	}
	return p
}

func (p *privateWork) body(method, path, token string, want int) string {
	p.e.t.Helper()
	resp, raw := p.e.do(method, api1+path, token, nil, nil)
	if resp.StatusCode != want {
		p.e.t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	return string(raw)
}

func threadKeys(t *testing.T, raw string) []string {
	t.Helper()
	var ts []struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(raw), &ts); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, x := range ts {
		out = append(out, x.Key)
	}
	return out
}

func TestPrivateWorkHiddenFromNonMembers(t *testing.T) {
	p := newPrivateWork(t)

	// the member sees all the work
	for path, want := range map[string]string{"/requests": "req-legal", "/tasks": "task-legal", "/reports": "rep-legal",
		"/activity": "act-legal", "/agents/legal/detail": "task-legal"} {
		if got := p.body("GET", path, p.ana, 200); !strings.Contains(got, want) {
			t.Fatalf("ana: GET %s lacks %s: %s", path, want, got)
		}
	}
	for _, path := range []string{"/requests/req-legal", "/requests/req-legal/audit", "/tasks/task-legal", "/reports/rep-legal",
		"/agents/legal/memory", "/chats/threads/legal/messages", "/conversations/conv-legal/messages"} {
		p.body("GET", path, p.ana, 200)
	}
	if keys := threadKeys(t, p.body("GET", "/chats/threads", p.ana, 200)); !has(keys, "legal") {
		t.Fatalf("ana's threads = %v", keys)
	}

	// an admin who is not a member: the listings leave it out, the details answer like a missing one
	for _, path := range []string{"/requests", "/tasks", "/tasks?agent_id=legal", "/reports", "/activity"} {
		got := p.body("GET", path, p.beto, 200)
		if strings.Contains(got, "legal") {
			t.Fatalf("beto: GET %s shows the private work: %s", path, got)
		}
	}
	for path, want := range map[string]string{"/requests": "req-sales", "/tasks": "task-sales", "/reports": "rep-sales", "/activity": "act-sales"} {
		if got := p.body("GET", path, p.beto, 200); !strings.Contains(got, want) {
			t.Fatalf("beto: GET %s lacks the General work %s: %s", path, want, got)
		}
	}
	for _, path := range []string{"/requests/req-legal", "/requests/req-legal/audit", "/tasks/task-legal", "/reports/rep-legal",
		"/reports/rep-legal/evidence", "/agents/legal/memory", "/chats/threads/legal/messages", "/conversations/conv-legal/messages"} {
		p.body("GET", path, p.beto, 404)
	}
	for _, path := range []string{"/requests/req-sales", "/tasks/task-sales", "/reports/rep-sales", "/agents/sales/memory",
		"/conversations/conv-sales/messages"} {
		p.body("GET", path, p.beto, 200)
	}
	// the admin still sees the agent (and may manage it), not its work
	if got := p.body("GET", "/agents/legal/detail", p.beto, 200); strings.Contains(got, "task-legal") || strings.Contains(got, "act-legal") ||
		strings.Contains(got, "conv-legal") {
		t.Fatalf("beto's agent detail shows the work: %s", got)
	}
	if keys := threadKeys(t, p.body("GET", "/chats/threads", p.beto, 200)); has(keys, "legal") || !has(keys, "sales") {
		t.Fatalf("beto's threads = %v", keys)
	}
	if got := p.body("GET", "/conversations", p.beto, 200); strings.Contains(got, "conv-legal") || strings.Contains(got, `"agent:legal"`) {
		t.Fatalf("beto's conversations = %s", got)
	}
	p.body("POST", "/chats/threads/legal/read", p.beto, 404)
}

func TestConversationPostChecksAgentUse(t *testing.T) {
	p := newPrivateWork(t)
	post := func(token, conv string) int {
		return p.e.status("POST", api1+"/conversations/"+conv+"/messages", token, map[string]string{"text": "hola"})
	}
	ghost := post(p.beto, "conv-ghost")
	if ghost != 404 {
		t.Fatalf("missing conversation = %d", ghost)
	}
	if c := post(p.beto, "conv-legal"); c != ghost {
		t.Fatalf("beto posts in legal's conversation = %d, want %d like a missing one", c, ghost)
	}
	// an admin outside the office does not use its agent (E2's answer to an agent you may not use)
	if c := post(p.beto, "agent:legal"); c != 403 {
		t.Fatalf("beto posts in legal's chat = %d, want 403", c)
	}
	if c := p.e.status("POST", api1+"/messages", p.beto, map[string]string{"conversation": "agent:legal", "text": "hola"}); c != 403 {
		t.Fatalf("beto chats with legal = %d, want 403", c)
	}
	// nor calls it from the office chat
	if c := p.e.status("POST", api1+"/messages", p.beto, map[string]string{"conversation": "office", "text": "@legal revisa esto"}); c != 403 {
		t.Fatalf("beto mentions legal in the office chat = %d, want 403", c)
	}
	if c := p.e.status("POST", api1+"/messages", p.ana, map[string]string{"conversation": "office", "text": "@legal revisa esto"}); c != 202 {
		t.Fatalf("ana mentions legal = %d", c)
	}
	if c := post(p.ana, "conv-legal"); c != 201 {
		t.Fatalf("ana posts in legal's conversation = %d", c)
	}
	if c := post(p.beto, "conv-sales"); c != 201 {
		t.Fatalf("beto posts in a General conversation = %d", c)
	}
}

func (p *privateWork) command(token string, body map[string]any) string {
	p.e.t.Helper()
	resp, raw := p.e.do("POST", api1+"/commands/run", token, body, nil)
	if resp.StatusCode != 200 {
		p.e.t.Fatalf("command %v = %d %s", body, resp.StatusCode, raw)
	}
	return string(raw)
}

// The slash commands read the same work through other paths: they leave out what the person may not see.
func TestCommandsLeaveOutPrivateWork(t *testing.T) {
	p := newPrivateWork(t)
	office := map[string]any{"surface": "office"}
	for _, cmd := range []map[string]any{
		{"command": "search", "args": map[string]any{"query": "informe"}, "context": office},
		{"command": "daily-summary", "context": office},
		{"command": "pending", "context": office},
		{"command": "status", "context": office},
	} {
		if got := p.command(p.beto, cmd); strings.Contains(got, "legal") {
			t.Fatalf("beto: /%s shows the private work: %s", cmd["command"], got)
		}
	}
	if got := p.command(p.ana, map[string]any{"command": "search", "args": map[string]any{"query": "informe"}, "context": office}); !strings.Contains(got, "informe legal") {
		t.Fatalf("ana's search = %s", got)
	}
	if got := p.command(p.ana, map[string]any{"command": "daily-summary", "context": office}); !strings.Contains(got, "tarea legal") {
		t.Fatalf("ana's daily summary = %s", got)
	}
	if got := p.command(p.beto, map[string]any{"command": "daily-summary", "context": office}); !strings.Contains(got, "tarea sales") {
		t.Fatalf("beto's daily summary lacks General's work: %s", got)
	}
	// a command naming the hidden agent answers like an unknown agent
	resp, raw := p.e.do("POST", api1+"/commands/run", p.beto, map[string]any{"command": "memory", "args": map[string]any{"agent": "legal"}, "context": office}, nil)
	if resp.StatusCode == 200 || !strings.Contains(string(raw), "unknown_agent") {
		t.Fatalf("beto /memory @legal = %d %s", resp.StatusCode, raw)
	}
}

func TestAwaySummaryLeavesOutPrivateWork(t *testing.T) {
	p := newPrivateWork(t)
	org := p.owner.OrgID
	for _, tok := range []string{p.ana, p.beto} {
		p.e.json("GET", api1+"/away-summary", tok, nil, 200)
		p.e.json("POST", api1+"/away-summary/heartbeat", tok, nil, 200)
	}
	save := func(id, typ, agent string, pl any) {
		b, _ := json.Marshal(pl)
		if err := p.e.store.SaveEvent(context.Background(), org, domain.Event{ID: id, Type: typ, TS: time.Now().UTC(), OrgID: org, AgentID: agent, Payload: json.RawMessage(b)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, agent := range []string{"legal", "sales"} {
		save("aw-done-"+agent, domain.EvRequestCompleted, "", map[string]any{"request_id": "req-" + agent, "report_id": "rep-" + agent})
		save("aw-fail-"+agent, domain.EvTaskFailed, agent, map[string]any{"task": domain.Task{ID: "aw-t-" + agent, AgentID: agent, Title: "falló " + agent}, "error": "timeout " + agent})
		save("aw-state-"+agent, domain.EvAgentState, agent, map[string]any{"agent_id": agent, "state": "working", "activity": "trabajando " + agent})
	}
	time.Sleep(awayTestGap + 100*time.Millisecond)
	if got := p.body("GET", "/away-summary?min_hours=0", p.beto, 200); strings.Contains(got, "legal") || !strings.Contains(got, "falló sales") {
		t.Fatalf("beto's away summary = %s", got)
	}
	if got := p.body("GET", "/away-summary?min_hours=0", p.ana, 200); !strings.Contains(got, "falló legal") || !strings.Contains(got, "pedido legal") {
		t.Fatalf("ana's away summary = %s", got)
	}
}

// Writes on a hidden conversation answer like a missing one, and change nothing.
func TestHiddenConversationWrites(t *testing.T) {
	p := newPrivateWork(t)
	for _, c := range []struct{ method, path string }{
		{"DELETE", "/conversations/conv-legal"}, {"PATCH", "/conversations/conv-legal"}, {"POST", "/conversations/conv-legal/read"},
		{"DELETE", "/conversations/agent:legal"}, {"POST", "/conversations/agent:legal/read"},
	} {
		if code := p.e.status(c.method, api1+c.path, p.beto, map[string]any{"title": "x"}); code != 404 {
			t.Fatalf("beto %s %s = %d, want 404", c.method, c.path, code)
		}
	}
	p.body("GET", "/conversations/conv-legal/messages", p.ana, 200)
	if got := p.body("GET", "/conversations", p.ana, 200); !strings.Contains(got, "conv-legal") {
		t.Fatalf("the conversation is still there for ana: %s", got)
	}
}

// The detail routes around a request, a task, an agent and the code runs.
func TestPrivateWorkDetailRoutes(t *testing.T) {
	p := newPrivateWork(t)
	for _, path := range []string{"/requests/req-legal/cost", "/requests/req-legal/estimate", "/tasks/task-legal/intervention",
		"/agents/legal/quality", "/agents/legal/golden-cases", "/code-runs/run-legal"} {
		p.body("GET", path, p.beto, 404)
		if c := p.e.status("GET", api1+path, p.ana, nil); c == 404 {
			t.Fatalf("ana: GET %s = 404", path)
		}
	}
	if got := p.body("GET", "/code-runs", p.beto, 200); strings.Contains(got, "legal") || !strings.Contains(got, "run-sales") {
		t.Fatalf("beto's code runs = %s", got)
	}
	for _, c := range []struct{ method, path string }{
		{"POST", "/tasks/task-legal/steer"}, {"POST", "/tasks/task-legal/reassign"}, {"POST", "/requests/req-legal/confirm"},
	} {
		if code := p.e.status(c.method, api1+c.path, p.beto, map[string]any{"note": "x", "agent_id": "sales"}); code != 404 {
			t.Fatalf("beto %s %s = %d, want 404", c.method, c.path, code)
		}
	}
}

// D3 decision: a private office's agent in the directory is used by everybody in their own thread; the rest of the
// office's work stays the members'.
func TestDirectoryAgentOfPrivateOffice(t *testing.T) {
	p := newPrivateWork(t)
	p.e.json("PUT", api1+"/agents/legal/access", p.ana, map[string]any{"visibility": "directory"}, 200)
	if c := p.e.status("POST", api1+"/messages", p.beto, map[string]string{"conversation": "agent:legal", "text": "hola"}); c != 202 {
		t.Fatalf("beto chats with the directory agent = %d", c)
	}
	if keys := threadKeys(t, p.body("GET", "/chats/threads", p.beto, 200)); !has(keys, "legal") {
		t.Fatalf("beto's threads = %v", keys)
	}
	p.body("GET", "/chats/threads/legal/messages", p.beto, 200)
	p.body("GET", "/conversations/agent:legal/messages", p.beto, 200)
	for _, path := range []string{"/requests/req-legal", "/agents/legal/memory", "/conversations/conv-legal/messages"} {
		p.body("GET", path, p.beto, 404)
	}
}
