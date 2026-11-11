package api_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// D3 pending items, area A: the last paths to a private department's work through the API, the chats and the
// conversations.

// memberNamed adds a person with a display name to owner's organization and returns their access token.
func (e *env) memberNamed(owner *auth.Session, email, name string, role auth.Role) string {
	e.t.Helper()
	ctx := context.Background()
	if _, err := e.svc.Register(ctx, auth.RegisterInput{Email: email, Password: goodPW, Name: name, OrgName: "Own of " + email}, auth.ClientMeta{}); err != nil {
		e.t.Fatal(err)
	}
	p := auth.Principal{UserID: owner.User.ID, OrgID: owner.OrgID, Role: auth.RoleOwner}
	if _, err := e.svc.AddMember(ctx, p, email, role); err != nil {
		e.t.Fatal(err)
	}
	s, err := e.svc.Login(ctx, auth.LoginInput{Email: email, Password: goodPW, OrgID: owner.OrgID}, auth.ClientMeta{})
	if err != nil {
		e.t.Fatal(err)
	}
	return s.AccessToken
}

// 1. @name in the office chat names a person before an agent: a colleague called like a private agent's first name
// is not refused.
func TestMentionResolvesThePersonFirst(t *testing.T) {
	p := newPrivateWork(t)
	p.e.memberNamed(p.owner, "elena-pw@example.com", "Elena Gómez", auth.RoleMember) // legal's agent is Elena Castro
	post := func(text string) int {
		return p.e.status("POST", api1+"/messages", p.beto, map[string]string{"conversation": "office", "text": text})
	}
	// the offices model is cached for a clock tick (TTL 1 ns on a coarse clock): Elena is in it once it is read again
	p.e.eventually("beto mentions his colleague Elena (202)", func() bool { return post("@elena mira esto") == 202 })
	if c := post("@legal mira esto"); c != 403 {
		t.Fatalf("beto mentions the agent legal = %d, want 403", c)
	}
}

// 2. /usage (/cost) does not list the spend of the agents whose work the person does not see one by one.
func TestUsageGroupsHiddenAgents(t *testing.T) {
	p := newPrivateWork(t)
	ctx, org, now := context.Background(), p.owner.OrgID, time.Now().UTC()
	for _, u := range []domain.UsageEntry{
		{ID: "u-legal", AgentID: "legal", Kind: domain.UsageRunTask, CostUSD: 0.5, TS: now},
		{ID: "u-sales", AgentID: "sales", Kind: domain.UsageRunTask, CostUSD: 0.25, TS: now},
	} {
		if err := p.e.store.AddUsage(ctx, org, u); err != nil {
			t.Fatal(err)
		}
	}
	usage := map[string]any{"command": "usage", "lang": "es", "context": map[string]any{"surface": "office"}}
	got := p.command(p.beto, usage)
	if strings.Contains(got, "Elena") || strings.Contains(got, "legal") || !strings.Contains(got, "Valeria") || !strings.Contains(got, "Otros agentes") {
		t.Fatalf("beto's /usage = %s", got)
	}
	if got := p.command(p.ana, usage); !strings.Contains(got, "Elena") || strings.Contains(got, "Otros agentes") {
		t.Fatalf("ana's /usage = %s", got)
	}
}

// 5. /learn in the chat of a private department's agent the person uses through the directory says why it cannot.
func TestLearnInADirectoryChatExplains(t *testing.T) {
	p := newPrivateWork(t, withSkills)
	p.e.json("PUT", api1+"/agents/legal/access", p.ana, map[string]any{"visibility": "directory"}, 200)
	code, body := p.cmdRaw(p.beto, map[string]any{"command": "learn", "lang": "es", "context": map[string]any{"surface": "agent", "agent_id": "legal"}})
	if code == 200 || strings.Contains(body, "unknown_agent") || !strings.Contains(body, "departamento privado") {
		t.Fatalf("beto /learn in legal's chat = %d %s", code, body)
	}
	// an agent the person does not even use still answers like an unknown one
	p.e.json("PUT", api1+"/agents/legal/access", p.ana, map[string]any{"visibility": "office"}, 200)
	if code, body := p.cmdRaw(p.beto, map[string]any{"command": "learn", "args": map[string]any{"agent": "legal"}, "context": map[string]any{"surface": "office"}}); code == 200 || !strings.Contains(body, "unknown_agent") {
		t.Fatalf("beto /learn @legal = %d %s", code, body)
	}
}

// 6. /compact in the chat with a private department's agent used through the directory summarizes the person's own
// thread and never the history shared before private threads.
func TestCompactOfADirectoryChatSummarizesOwnThreadOnly(t *testing.T) {
	p := newPrivateWork(t, withConvContext)
	p.legacyLegal(t)
	p.e.json("PUT", api1+"/agents/legal/access", p.ana, map[string]any{"visibility": "directory"}, 200)
	ctx, org, at := context.Background(), p.owner.OrgID, time.Now().UTC().Add(2*time.Hour)
	own := domain.ChatPrivateConv("legal", p.e.userID(p.beto))
	if err := p.e.store.CreateConversation(ctx, org, domain.Conversation{ID: own, Title: "legal", Participants: []string{"user", "legal"}, LastMessageAt: at}); err != nil {
		t.Fatal(err)
	}
	for i, m := range []domain.Message{
		{ID: "own-1", From: "user", To: "legal", Text: "consulta-propia-de-beto", TurnID: "OB1", AuthorID: p.e.userID(p.beto)},
		{ID: "own-2", From: "legal", To: "user", Text: "respuesta-propia-a-beto", TurnID: "OB1"},
	} {
		m.ConversationID, m.Kind, m.TS = own, domain.MsgChat, at.Add(time.Duration(i)*time.Second)
		if err := p.e.store.AddMessage(ctx, org, m); err != nil {
			t.Fatal(err)
		}
	}
	got := p.command(p.beto, map[string]any{"command": "compact", "context": map[string]any{"surface": "agent", "agent_id": "legal", "conversation_id": "agent:legal"}})
	if strings.Contains(got, "secret") || !strings.Contains(got, "propia") {
		t.Fatalf("beto /compact of his thread with legal = %s", got)
	}
}

// 11. Renaming or archiving a hidden agent chat answers like a missing conversation.
func TestPatchHiddenAgentChat(t *testing.T) {
	p := newPrivateWork(t)
	for _, body := range []map[string]any{{"title": "x"}, {"archived": true}} {
		if c := p.e.status("PATCH", api1+"/conversations/agent:legal", p.beto, body); c != 404 {
			t.Fatalf("beto PATCH agent:legal %v = %d, want 404", body, c)
		}
	}
}

// seedChat writes messages to a chat conversation (created when missing), one second apart from at.
func seedChat(t *testing.T, p *privateWork, conv string, at time.Time, msgs ...domain.Message) {
	t.Helper()
	ctx, org, st := context.Background(), p.owner.OrgID, p.e.store
	if _, err := st.GetConversation(ctx, org, conv); err != nil {
		if err := st.CreateConversation(ctx, org, domain.Conversation{ID: conv, Title: conv, Participants: []string{"user"}, LastMessageAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	for i, m := range msgs {
		m.ConversationID, m.TS = conv, at.Add(time.Duration(i)*time.Second)
		if m.Kind == "" {
			m.Kind = domain.MsgChat
		}
		if err := st.AddMessage(ctx, org, m); err != nil {
			t.Fatal(err)
		}
	}
}

// page reads a paged list of messages: their texts and the paging headers.
func (p *privateWork) page(path, token string) (texts []string, more bool, next string) {
	p.e.t.Helper()
	resp, raw := p.e.do("GET", api1+path, token, nil, nil)
	if resp.StatusCode != 200 {
		p.e.t.Fatalf("GET %s = %d %s", path, resp.StatusCode, raw)
	}
	var ms []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &ms); err != nil {
		p.e.t.Fatal(err)
	}
	for _, m := range ms {
		texts = append(texts, m.Text)
	}
	return texts, resp.Header.Get("X-Has-More") == "true", resp.Header.Get("X-Next-Before")
}

// 7. A page of messages left out after reading is filled from older ones, and the cursor is always given.
func TestMessagePagesFillAfterLeavingOut(t *testing.T) {
	p := newPrivateWork(t)
	at := time.Now().UTC().Add(-time.Hour)
	msgs := []domain.Message{
		{ID: "v-0", From: "sales", To: "user", Text: "visible-0", TurnID: "V0"},
		{ID: "v-1", From: "sales", To: "user", Text: "visible-1", TurnID: "V1"},
		{ID: "v-2", From: "sales", To: "user", Text: "visible-2", TurnID: "V2"},
	}
	for i := range 5 {
		msgs = append(msgs, domain.Message{ID: "h-" + string(rune('a'+i)), From: "legal", To: "user", Text: "oculto", TurnID: "H"})
	}
	seedChat(t, p, domain.ChatOffice, at, msgs...)
	for _, base := range []string{"/conversations/office/messages", "/chats/threads/general/messages"} {
		texts, more, next := p.page(base+"?limit=2", p.beto)
		if strings.Join(texts, ",") != "visible-1,visible-2" || !more || next == "" {
			t.Fatalf("beto %s first page = %v more=%v next=%q", base, texts, more, next)
		}
		texts, more, _ = p.page(base+"?limit=2&before="+next, p.beto)
		if strings.Join(texts, ",") != "visible-0" || more {
			t.Fatalf("beto %s second page = %v more=%v", base, texts, more)
		}
		if texts, _, _ := p.page(base+"?limit=2", p.ana); strings.Join(texts, ",") != "oculto,oculto" {
			t.Fatalf("ana %s first page = %v", base, texts)
		}
	}
}

// 7. GET /conversations pages over what the person sees: no empty page that says there is more, and the next offset
// always moves forward.
func TestConversationPagesMoveForward(t *testing.T) {
	p := newPrivateWork(t)
	ctx, org, later := context.Background(), p.owner.OrgID, time.Now().UTC().Add(time.Hour)
	req := "req-legal"
	for i := range 4 {
		id := "conv-legal-" + string(rune('a'+i))
		if err := p.e.store.CreateConversation(ctx, org, domain.Conversation{ID: id, Title: "conversación secreta", Participants: []string{"user", "legal"},
			RequestID: &req, LastMessageAt: later.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	offset, seen := 0, []string{}
	for range 20 {
		resp, raw := p.e.do("GET", api1+"/conversations?limit=1&offset="+strconv.Itoa(offset), p.beto, nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("GET /conversations = %d %s", resp.StatusCode, raw)
		}
		var rows []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &rows); err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			seen = append(seen, r.ID)
		}
		if resp.Header.Get("X-Has-More") != "true" {
			break
		}
		if len(rows) == 0 {
			t.Fatalf("an empty page says there is more (offset %d)", offset)
		}
		next := resp.Header.Get("X-Next-Offset")
		if n := atoi(t, next); n <= offset {
			t.Fatalf("the next offset %q does not move forward from %d", next, offset)
		} else {
			offset = n
		}
	}
	if strings.Contains(strings.Join(seen, ","), "legal") || !has(seen, "conv-sales") {
		t.Fatalf("beto's conversations = %v", seen)
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("not a number: %q", s)
	}
	return n
}

// 7. GET /activity keeps reading past what it leaves out until the page is full.
func TestActivityPageFillsPastHiddenLines(t *testing.T) {
	p := newPrivateWork(t)
	ctx, org, now, legal := context.Background(), p.owner.OrgID, time.Now().UTC(), "legal"
	for i := range 1100 {
		if err := p.e.store.AddActivity(ctx, org, domain.ActivityItem{ID: "act-legal-" + strconv.Itoa(i), TS: now, AgentID: &legal, Kind: domain.EvTaskCompleted, Text: "terminó legal"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := p.body("GET", "/activity?limit=5", p.beto, 200); !strings.Contains(got, "act-sales") || strings.Contains(got, "act-legal") {
		t.Fatalf("beto's activity = %.300s", got)
	}
}

// 10. Someone who uses a private department's agent through the directory keeps their own exchange with it in the
// office chat: in the history the model gets, in /export and in the threads. Nobody else outside the office sees it.
func TestOwnOfficeExchangeWithADirectoryAgentStays(t *testing.T) {
	rt := &historySpy{}
	p := newPrivateWork(t, func(o *opts) { o.runtime = rt; o.drive = true })
	p.e.json("PUT", api1+"/agents/legal/access", p.ana, map[string]any{"visibility": "directory"}, 200)
	beto := p.e.userID(p.beto)
	seedChat(t, p, domain.ChatOffice, time.Now().UTC().Add(-time.Hour),
		domain.Message{ID: "b-1", From: "user", To: "legal", Text: "pregunta-propia-de-beto", TurnID: "B1", AuthorID: beto},
		domain.Message{ID: "b-2", From: "legal", To: "user", Text: "respuesta-de-legal-a-beto", TurnID: "B1"},
		domain.Message{ID: "x-1", From: "user", To: "legal", Text: "pregunta-de-ana", TurnID: "A1", AuthorID: p.e.userID(p.ana)},
		domain.Message{ID: "x-2", From: "legal", To: "user", Text: "respuesta-de-legal-a-ana", TurnID: "A1"},
	)
	export := func(token string) string {
		t.Helper()
		resp, raw := p.e.do("POST", api1+"/commands/run", token, map[string]any{"command": "export", "context": map[string]any{"surface": "office", "conversation_id": "office"}}, nil)
		if resp.StatusCode != 200 {
			return string(raw) // nothing the person may read: nothing to export
		}
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		resp, raw = p.e.do("GET", resultOf(out)["url"].(string), token, nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("download = %d %s", resp.StatusCode, raw)
		}
		return string(raw)
	}
	reads := map[string]func(string) string{
		"export":  export,
		"thread":  func(tok string) string { return p.body("GET", "/chats/threads/general/messages", tok, 200) },
		"history": func(tok string) string { return p.body("GET", "/conversations/office/messages", tok, 200) },
	}
	for name, read := range reads {
		got := read(p.beto)
		if !strings.Contains(got, "respuesta-de-legal-a-beto") || !strings.Contains(got, "pregunta-propia-de-beto") || strings.Contains(got, "a-ana") || strings.Contains(got, "pregunta-de-ana") {
			t.Errorf("beto's %s: %s", name, got)
		}
		if got := read(p.owner.AccessToken); strings.Contains(got, "beto") {
			t.Errorf("the owner's %s shows beto's exchange with legal: %s", name, got)
		}
	}
	if r, b := p.e.do("POST", api1+"/messages", p.beto, map[string]any{"conversation": "office", "text": "sigue-beto"}, nil); r.StatusCode >= 300 {
		t.Fatalf("post = %d %s", r.StatusCode, b)
	}
	if h := rt.historyOf(t, "sigue-beto"); !strings.Contains(h, "respuesta-de-legal-a-beto") || strings.Contains(h, "a-ana") {
		t.Errorf("the model's history for beto: %s", h)
	}
}
