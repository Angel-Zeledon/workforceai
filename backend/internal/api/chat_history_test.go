package api_test

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
)

type threadMsg struct {
	ID             string   `json:"id"`
	ConversationID string   `json:"conversation_id"`
	From           string   `json:"from"`
	Text           string   `json:"text"`
	TurnID         string   `json:"turn_id"`
	AgentIDs       []string `json:"agent_ids"`
}

func (e *env) thread(token, key, query string) ([]threadMsg, string) {
	e.t.Helper()
	resp, body := e.do("GET", "/api/v1/chats/threads/"+key+"/messages"+query, token, nil, nil)
	if resp.StatusCode != 200 {
		e.t.Fatalf("GET thread %s = %d %s", key, resp.StatusCode, body)
	}
	var out []threadMsg
	if err := json.Unmarshal(body, &out); err != nil {
		e.t.Fatalf("thread body: %v: %s", err, body)
	}
	return out, resp.Header.Get("X-Has-More")
}

func (e *env) waitThread(token, key string, n int) []threadMsg {
	e.t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if ms, _ := e.thread(token, key, "?limit=100"); len(ms) >= n {
			return ms
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("timeout waiting for %d messages in thread %s", n, key)
	return nil
}

func texts(ms []threadMsg) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Text)
	}
	return out
}

// What you say in the office reaches the agent's history and what you say to an agent reaches General.
func TestChatHistoryGeneralAndAgentThreadsGoHandInHand(t *testing.T) {
	e := newEnv(t, opts{})
	// a question for the accountant, in the office
	if c := e.status("POST", "/api/v1/messages", "", map[string]string{"conversation": "office", "text": "hablemos del balance"}); c != 202 {
		t.Fatalf("post office = %d", c)
	}
	acc := e.waitThread("", "accounting", 2)
	if acc[0].From != "user" || acc[0].Text != "hablemos del balance" || acc[1].From != "accounting" {
		t.Fatalf("accounting thread: %+v", acc)
	}
	if !slices.Contains(acc[0].AgentIDs, "accounting") {
		t.Fatalf("user message badge = %v", acc[0].AgentIDs)
	}
	// the same message (same id) is in General
	gen := e.waitThread("", "general", 2)
	if gen[0].ID != acc[0].ID {
		t.Fatalf("General and accounting disagree: %s vs %s", gen[0].ID, acc[0].ID)
	}
	// an agent who took no part sees nothing
	if sales, _ := e.thread("", "sales", ""); len(sales) != 0 {
		t.Fatalf("sales thread = %v", texts(sales))
	}

	// a message to the agent's own chat shows in General
	if c := e.status("POST", "/api/v1/messages", "", map[string]string{"conversation": "agent:sales", "text": "hola ventas"}); c != 202 {
		t.Fatalf("post agent = %d", c)
	}
	sales := e.waitThread("", "sales", 2)
	if sales[0].Text != "hola ventas" || sales[0].ConversationID != "agent:sales" {
		t.Fatalf("sales thread: %+v", sales)
	}
	gen = e.waitThread("", "general", 4)
	if !slices.Contains(texts(gen), "hola ventas") || !slices.Contains(texts(gen), "hablemos del balance") {
		t.Fatalf("General misses messages: %v", texts(gen))
	}

	// the lists
	_, raw := e.do("GET", "/api/v1/chats/threads", "", nil, nil)
	var ths []application.ChatThread
	if err := json.Unmarshal(raw, &ths); err != nil || len(ths) < 2 || ths[0].Key != "general" || ths[0].MessageCount < 4 {
		t.Fatalf("threads: %v %s", err, raw)
	}
	by := map[string]application.ChatThread{}
	for _, x := range ths {
		by[x.Key] = x
	}
	if by["sales"].MessageCount != 2 || by["accounting"].MessageCount != 2 || by["hr"].MessageCount != 0 {
		t.Fatalf("thread counts: %+v", by)
	}
	if by["general"].Unread == 0 {
		t.Fatalf("agent replies are unread: %+v", by["general"])
	}
	if c := e.status("POST", "/api/v1/chats/threads/general/read", "", nil); c != 204 {
		t.Fatalf("read = %d", c)
	}
	_, raw = e.do("GET", "/api/v1/chats/threads", "", nil, nil)
	_ = json.Unmarshal(raw, &ths)
	if ths[0].Unread != 0 {
		t.Fatalf("General unread after read: %+v", ths[0])
	}

	// bad inputs
	if c := e.status("GET", "/api/v1/chats/threads/ghost/messages", "", nil); c != 404 {
		t.Fatalf("unknown agent thread = %d", c)
	}
	if c := e.status("GET", "/api/v1/chats/threads/general/messages?limit=-1", "", nil); c != 400 {
		t.Fatalf("bad limit = %d", c)
	}
	if c := e.status("GET", "/api/v1/chats/threads/general/messages?before=nope", "", nil); c != 404 {
		t.Fatalf("bad cursor = %d", c)
	}
	if c := e.status("POST", "/api/v1/chats/threads/ghost/read", "", nil); c != 404 {
		t.Fatalf("read unknown = %d", c)
	}
}

func TestChatHistoryThreadPaging(t *testing.T) {
	e := newEnv(t, opts{})
	for _, txt := range []string{"uno", "dos", "tres"} {
		e.status("POST", "/api/v1/messages", "", map[string]string{"conversation": "agent:sales", "text": txt})
		e.waitThread("", "sales", 2*(slices.Index([]string{"uno", "dos", "tres"}, txt)+1))
	}
	page, more := e.thread("", "sales", "?limit=2")
	if len(page) != 2 || more != "true" {
		t.Fatalf("page 1: %d more=%s", len(page), more)
	}
	resp, body := e.do("GET", "/api/v1/chats/threads/sales/messages?limit=2", "", nil, nil)
	next := resp.Header.Get("X-Next-Before")
	if next != page[0].ID {
		t.Fatalf("X-Next-Before = %q (%s)", next, body)
	}
	older, more := e.thread("", "sales", "?limit=10&before="+next)
	if len(older) != 4 || more != "false" {
		t.Fatalf("older: %d more=%s", len(older), more)
	}
	for _, m := range older {
		if m.ID == page[0].ID || m.ID == page[1].ID {
			t.Fatal("pages overlap")
		}
	}
}

func TestChatHistoryConversationListSearchRenameArchiveDelete(t *testing.T) {
	e := newEnv(t, opts{})
	e.status("POST", "/api/v1/messages", "", map[string]string{"conversation": "agent:accounting", "text": "hablemos del balance de caja"})
	e.waitThread("", "accounting", 2)
	// a work request creates its own conversation
	_, raw := e.do("POST", "/api/v1/requests", "", map[string]string{"text": "Preparar la propuesta comercial para Acme"}, nil)
	var rq struct {
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(raw, &rq)

	var rows []application.ChatConversation
	get := func(q string) []application.ChatConversation {
		t.Helper()
		resp, body := e.do("GET", "/api/v1/conversations"+q, "", nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("GET /conversations%s = %d %s", q, resp.StatusCode, body)
		}
		var out []application.ChatConversation
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("%v: %s", err, body)
		}
		return out
	}
	rows = get("")
	var reqConv application.ChatConversation
	for _, r := range rows {
		if r.Kind == "request" && r.RequestID != nil && *r.RequestID == rq.RequestID {
			reqConv = r
		}
	}
	if reqConv.ID == "" || !strings.HasPrefix(reqConv.Title, "Preparar la propuesta") || reqConv.LastMessageAt.IsZero() {
		t.Fatalf("request conversation missing: %+v", rows)
	}
	ag := get("?agent_id=accounting")
	if len(ag) == 0 || ag[0].ID != "agent:accounting" {
		t.Fatalf("per agent: %+v", ag)
	}
	for _, r := range ag {
		if r.Kind == "request" {
			t.Fatalf("request conversation in the accountant's list: %+v", r)
		}
	}
	if hits := get("?q=" + url.QueryEscape("BALANCE DE")); len(hits) != 1 || hits[0].ID != "agent:accounting" {
		t.Fatalf("search content: %+v", hits)
	}
	if hits := get("?q=" + url.QueryEscape("propuesta comercial")); len(hits) != 1 || hits[0].ID != reqConv.ID {
		t.Fatalf("search title: %+v", hits)
	}
	if c := e.status("GET", "/api/v1/conversations?archived=bogus", "", nil); c != 400 {
		t.Fatalf("bad archived = %d", c)
	}
	if c := e.status("GET", "/api/v1/conversations?limit=x", "", nil); c != 400 {
		t.Fatalf("bad limit = %d", c)
	}
	resp, _ := e.do("GET", "/api/v1/conversations?limit=1", "", nil, nil)
	if resp.Header.Get("X-Has-More") != "true" || resp.Header.Get("X-Next-Offset") != "1" {
		t.Fatalf("paging headers: %v", resp.Header)
	}

	// rename
	resp, body := e.do("PATCH", "/api/v1/conversations/"+reqConv.ID, "", map[string]string{"title": "  Propuesta   Acme "}, nil)
	var renamed application.ChatConversation
	_ = json.Unmarshal(body, &renamed)
	if resp.StatusCode != 200 || renamed.Title != "Propuesta Acme" || !renamed.TitleCustom {
		t.Fatalf("rename: %d %s", resp.StatusCode, body)
	}
	if c := e.status("PATCH", "/api/v1/conversations/"+reqConv.ID, "", map[string]string{"title": "   "}); c != 400 {
		t.Fatalf("blank title = %d", c)
	}
	if c := e.status("PATCH", "/api/v1/conversations/"+reqConv.ID, "", map[string]any{}); c != 400 {
		t.Fatalf("empty patch = %d", c)
	}
	if c := e.status("PATCH", "/api/v1/conversations/ghost", "", map[string]bool{"archived": true}); c != 404 {
		t.Fatalf("ghost patch = %d", c)
	}
	// archive hides it, archived=only shows it
	if c := e.status("PATCH", "/api/v1/conversations/"+reqConv.ID, "", map[string]bool{"archived": true}); c != 200 {
		t.Fatalf("archive = %d", c)
	}
	for _, r := range get("") {
		if r.ID == reqConv.ID {
			t.Fatal("archived conversation still listed")
		}
	}
	if only := get("?archived=only"); len(only) != 1 || only[0].ID != reqConv.ID || !only[0].Archived {
		t.Fatalf("archived only: %+v", only)
	}
	// read marker
	if c := e.status("POST", "/api/v1/conversations/"+reqConv.ID+"/read", "", nil); c != 204 {
		t.Fatalf("read conv = %d", c)
	}
	// delete
	if c := e.status("DELETE", "/api/v1/conversations/"+reqConv.ID, "", nil); c != 204 {
		t.Fatalf("delete = %d", c)
	}
	if c := e.status("DELETE", "/api/v1/conversations/"+reqConv.ID, "", nil); c != 404 {
		t.Fatalf("second delete = %d", c)
	}
	if c := e.status("GET", "/api/v1/conversations/"+reqConv.ID+"/messages", "", nil); c != 404 {
		t.Fatalf("messages of deleted = %d", c)
	}
}

// Rename, archive and delete leave an audit trail (no message text) and obey the roles.
func TestChatHistoryAuditAndRoles(t *testing.T) {
	e := newEnv(t, opts{auth: true})
	owner := e.register("owner-chat@example.com", "chatco")
	member := e.member(owner, "member-chat@example.com", auth.RoleMember)
	viewer := e.member(owner, "viewer-chat@example.com", auth.RoleViewer)
	tok := owner.AccessToken

	if c := e.status("POST", "/api/v1/messages", member, map[string]string{"conversation": "agent:sales", "text": "hola secreto"}); c != 202 {
		t.Fatalf("member posts = %d", c)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		ms, _ := e.thread(viewer, "sales", "")
		if len(ms) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no reply")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// viewers read but cannot change anything
	if c := e.status("GET", "/api/v1/conversations?q=secreto", viewer, nil); c != 200 {
		t.Fatalf("viewer list = %d", c)
	}
	if c := e.status("PATCH", "/api/v1/conversations/agent:sales", viewer, map[string]bool{"archived": true}); c != 403 {
		t.Fatalf("viewer patch = %d", c)
	}
	if c := e.status("POST", "/api/v1/chats/threads/sales/read", viewer, nil); c != 403 {
		t.Fatalf("viewer read marker = %d", c)
	}
	// members rename and archive, but deleting is for administrators
	if c := e.status("PATCH", "/api/v1/conversations/agent:sales", member, map[string]string{"title": "Ventas"}); c != 200 {
		t.Fatalf("member rename = %d", c)
	}
	if c := e.status("DELETE", "/api/v1/conversations/agent:sales", member, nil); c != 403 {
		t.Fatalf("member delete = %d", c)
	}
	if c := e.status("DELETE", "/api/v1/conversations/agent:sales", tok, nil); c != 204 {
		t.Fatalf("owner delete = %d", c)
	}
	_, raw := e.do("GET", "/api/v1/audit?limit=200", tok, nil, nil)
	s := string(raw)
	if !strings.Contains(s, "conversation.updated") || !strings.Contains(s, "conversation.deleted") {
		t.Fatalf("audit lacks the conversation actions: %.400s", s)
	}
	if strings.Contains(s, "hola secreto") {
		t.Fatal("the audit trail must not carry message text")
	}
}
