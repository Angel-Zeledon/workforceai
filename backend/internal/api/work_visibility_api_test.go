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
)

// privateWork (D3): a private office "Legal" led by ana with the agent legal; beto is an admin outside it. The agent
// sales stays in General. The work of both agents is seeded straight into the store.
type privateWork struct {
	e         *env
	owner     *auth.Session
	ana, beto string
}

func newPrivateWork(t *testing.T) *privateWork {
	t.Helper()
	e := newEnv(t, opts{auth: true, orgchart: true, deps: func(d *api.Deps) {
		withOffices(d)
		docs := longmem.NewMemStore()
		d.Memory = longmem.New(longmem.Config{Docs: docs, Retriever: &longmem.FTS{Docs: docs}})
	}})
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
			Status: domain.TaskDone, DependsOn: []string{}, CreatedAt: now}))
		must(st.CreateReport(ctx, org, domain.Report{ID: "rep-" + agent, RequestID: req, Title: "informe " + agent,
			Sections: []domain.Section{}, Contributors: []string{agent}, CreatedAt: now}))
		id := agent
		must(st.AddActivity(ctx, org, domain.ActivityItem{ID: "act-" + agent, TS: now, AgentID: &id, Kind: domain.EvTaskCompleted, Text: "terminó " + agent}))
		must(st.CreateConversation(ctx, org, domain.Conversation{ID: "conv-" + agent, Title: "conversación " + agent,
			Participants: []string{"user", agent}, RequestID: &req, LastMessageAt: now}))
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
	if c := post(p.beto, "agent:legal"); c != ghost {
		t.Fatalf("beto posts in legal's chat = %d, want %d", c, ghost)
	}
	if c := p.e.status("POST", api1+"/messages", p.beto, map[string]string{"conversation": "agent:legal", "text": "hola"}); c != ghost {
		t.Fatalf("beto chats with legal = %d, want %d", c, ghost)
	}
	if c := post(p.ana, "conv-legal"); c != 201 {
		t.Fatalf("ana posts in legal's conversation = %d", c)
	}
	if c := post(p.beto, "conv-sales"); c != 201 {
		t.Fatalf("beto posts in a General conversation = %d", c)
	}
}
