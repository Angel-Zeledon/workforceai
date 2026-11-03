package api_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// E0 (docs/architecture/equipos-oficinas.md, chat-history.md): each person has their own private thread with
// each agent. These tests check that one person can never read another person's thread with the same agent:
// not through the messages API, the history views, the search, the read markers nor the WebSocket.

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// userIDOf reads the subject of an access token (tests only: no signature check).
func userIDOf(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c.Sub
}

func (e *env) convMessages(token, conv string) []threadMsg {
	e.t.Helper()
	resp, body := e.do("GET", "/api/v1/conversations/"+conv+"/messages", token, nil, nil)
	if resp.StatusCode != 200 {
		e.t.Fatalf("GET %s messages = %d %s", conv, resp.StatusCode, body)
	}
	var out []threadMsg
	if err := json.Unmarshal(body, &out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func msgTexts(ms []threadMsg) string {
	var b strings.Builder
	for _, m := range ms {
		b.WriteString(m.Text + "\n")
	}
	return b.String()
}

func TestPrivateAgentThreadsPerPerson(t *testing.T) {
	e := newEnv(t, opts{auth: true})
	owner := e.register("ana@example.com", "lawco")
	ana := owner.AccessToken
	luis := e.member(owner, "luis@example.com", auth.RoleMember)
	admin := e.member(owner, "adm@example.com", auth.RoleAdmin)
	anaID, luisID := userIDOf(t, ana), userIDOf(t, luis)

	// A WebSocket for Luis and one for the administrator, opened before anyone talks.
	wsAna, wsLuis, wsAdmin := e.dialWS(ana), e.dialWS(luis), e.dialWS(admin)
	defer wsAna.Close()
	defer wsLuis.Close()
	defer wsAdmin.Close()

	if c := e.status("POST", "/api/v1/messages", ana, map[string]string{"conversation": "agent:legal", "text": "contrato secreto de Ana"}); c != 202 {
		t.Fatalf("ana posts = %d", c)
	}
	waitFor(t, func() bool { return len(e.convMessages(ana, "agent:legal")) >= 2 })
	if c := e.status("POST", "/api/v1/messages", luis, map[string]string{"conversation": "agent:legal", "text": "pregunta de Luis"}); c != 202 {
		t.Fatalf("luis posts = %d", c)
	}
	waitFor(t, func() bool { return len(e.convMessages(luis, "agent:legal")) >= 2 })

	// 1. messages API: each sees only their own thread, with the public id and their name as author
	am, lm := e.convMessages(ana, "agent:legal"), e.convMessages(luis, "agent:legal")
	if strings.Contains(msgTexts(lm), "Ana") || strings.Contains(msgTexts(am), "Luis") {
		t.Fatalf("threads mixed:\nana=%s\nluis=%s", msgTexts(am), msgTexts(lm))
	}
	for _, m := range append(am, lm...) {
		if m.ConversationID != "agent:legal" {
			t.Fatalf("stored id leaked: %+v", m)
		}
	}
	if am[0].From != "user" || am[0].AuthorID != anaID || am[0].AuthorName == "" {
		t.Fatalf("author of ana's message = %+v", am[0])
	}
	// the admin does not read them either
	if ms := e.convMessages(admin, "agent:legal"); len(ms) != 0 {
		t.Fatalf("admin reads private threads: %s", msgTexts(ms))
	}
	// the stored id of someone else's thread is not a way in
	for _, raw := range []string{"agent:legal@" + anaID, "agent:legal%40" + anaID} {
		if c := e.status("GET", "/api/v1/conversations/"+raw+"/messages", luis, nil); c != 404 {
			t.Fatalf("GET %s by luis = %d", raw, c)
		}
		if c := e.status("POST", "/api/v1/messages", luis, map[string]string{"conversation": "agent:legal@" + anaID, "text": "x"}); c != 404 {
			t.Fatalf("POST into ana's thread by luis = %d", c)
		}
	}

	// 2. history views: the agent's thread and General
	for _, key := range []string{"legal", "general"} {
		lt, _ := e.thread(luis, key, "")
		if strings.Contains(msgTexts(lt), "secreto") {
			t.Fatalf("luis sees ana's message in %s: %s", key, msgTexts(lt))
		}
		at, _ := e.thread(ana, key, "")
		if !strings.Contains(msgTexts(at), "secreto") || strings.Contains(msgTexts(at), "Luis") {
			t.Fatalf("ana's %s view = %s", key, msgTexts(at))
		}
	}
	// 3. search and the conversation list
	_, raw := e.do("GET", "/api/v1/conversations?q=secreto", luis, nil, nil)
	if strings.Contains(string(raw), "agent:legal") || strings.Contains(string(raw), "secreto") {
		t.Fatalf("search leaks: %s", raw)
	}
	_, raw = e.do("GET", "/api/v1/conversations?q=secreto", admin, nil, nil)
	if strings.Contains(string(raw), "agent:legal") {
		t.Fatalf("admin search leaks: %s", raw)
	}
	_, raw = e.do("GET", "/api/v1/conversations?q=secreto", ana, nil, nil)
	if !strings.Contains(string(raw), `"agent:legal"`) || strings.Contains(string(raw), anaID) {
		t.Fatalf("ana's own search = %s", raw)
	}
	// unread is per person: Luis' marker does not read Ana's thread
	if c := e.status("POST", "/api/v1/chats/threads/legal/read", luis, nil); c != 204 {
		t.Fatalf("luis read = %d", c)
	}
	var ts []struct {
		Key    string `json:"key"`
		Unread int    `json:"unread"`
		Count  int    `json:"message_count"`
	}
	_, raw = e.do("GET", "/api/v1/chats/threads", ana, nil, nil)
	_ = json.Unmarshal(raw, &ts)
	for _, x := range ts {
		if x.Key == "legal" && (x.Unread == 0 || x.Count < 2) {
			t.Fatalf("ana's legal thread after luis read his = %+v", x)
		}
	}
	// 4. WebSocket: Ana's socket got her private frames (public id), Luis' and the admin's never did
	got := strings.Join(drainWS(wsAna, 300*time.Millisecond), " ")
	if !strings.Contains(got, "contrato secreto de Ana") || !strings.Contains(got, `"conversation":"agent:legal"`) || strings.Contains(got, "pregunta de Luis") {
		t.Fatalf("ana's socket: %.600s", got)
	}
	for name, c := range map[string]*websocket.Conn{"luis": wsLuis, "admin": wsAdmin} {
		for _, f := range drainWS(c, 300*time.Millisecond) {
			if strings.Contains(f, "secreto") || strings.Contains(f, "agent:legal@") {
				t.Fatalf("%s socket got a private frame: %s", name, f)
			}
			if name == "admin" && strings.Contains(f, "pregunta de Luis") {
				t.Fatalf("admin socket got luis' frame: %s", f)
			}
		}
	}
	_ = luisID
}

// The office stays shared and every message shows who wrote it; the legacy shared 1:1 history stays visible,
// read-only and marked, and is not given to the model as the history of the new private thread.
func TestOfficeAuthorsAndLegacyHistory(t *testing.T) {
	e := newEnv(t, opts{auth: true})
	owner := e.register("ana2@example.com", "officeco")
	ana := owner.AccessToken
	luis := e.member(owner, "luis2@example.com", auth.RoleMember)
	anaID := userIDOf(t, ana)

	// history written before E0: a shared 1:1 chat without author
	ctx := context.Background()
	org := owner.OrgID
	_ = e.store.CreateConversation(ctx, org, domain.Conversation{ID: "agent:sales", Title: "Chat", LastMessageAt: time.Now().UTC()})
	_ = e.store.AddMessage(ctx, org, domain.Message{ID: "old1", ConversationID: "agent:sales", From: "user", To: "sales", Kind: "chat",
		Text: "mensaje viejo compartido", TS: time.Now().UTC().Add(-time.Hour)})

	if c := e.status("POST", "/api/v1/messages", ana, map[string]string{"conversation": "office", "text": "hola equipo"}); c != 202 {
		t.Fatalf("ana office = %d", c)
	}
	waitFor(t, func() bool { return len(e.convMessages(luis, "office")) >= 2 })
	var mine *threadMsg
	for _, m := range e.convMessages(luis, "office") {
		if m.Text == "hola equipo" {
			mine = &m
		}
	}
	if mine == nil || mine.AuthorID != anaID || mine.AuthorName == "" {
		t.Fatalf("luis must see ana as the author in the office: %+v", mine)
	}
	// the legacy history: visible to both, marked, read-only (renaming the legacy row is not possible)
	for _, tok := range []string{ana, luis} {
		ms := e.convMessages(tok, "agent:sales")
		if len(ms) != 1 || !ms[0].SharedLegacy || ms[0].ConversationID != "agent:sales" {
			t.Fatalf("legacy history = %+v", ms)
		}
	}
	if c := e.status("PATCH", "/api/v1/conversations/agent:sales", luis, map[string]string{"title": "mío"}); c != 404 {
		t.Fatalf("legacy rename = %d", c)
	}
	if c := e.status("POST", "/api/v1/messages", luis, map[string]string{"conversation": "agent:sales", "text": "hola nuevo"}); c != 202 {
		t.Fatal("post")
	}
	waitFor(t, func() bool { return len(e.convMessages(luis, "agent:sales")) >= 3 })
	ms := e.convMessages(luis, "agent:sales")
	legacy := 0
	for _, m := range ms {
		if m.SharedLegacy {
			legacy++
		}
	}
	if legacy != 1 {
		t.Fatalf("only the old message is legacy: %+v", ms)
	}
	// Ana still sees only the legacy message, not Luis' new thread
	if ms := e.convMessages(ana, "agent:sales"); len(ms) != 1 {
		t.Fatalf("ana sees %d messages in sales", len(ms))
	}
}

// More attachments than allowed is a clear 400, never a silent cut.
func TestChatAttachmentLimit(t *testing.T) {
	e := newEnv(t, opts{auth: true})
	owner := e.register("att@example.com", "attco")
	paths := make([]string, 11)
	for i := range paths {
		paths[i] = "/Shared/f" + string(rune('a'+i)) + ".txt"
	}
	resp, body := e.do("POST", "/api/v1/messages", owner.AccessToken, map[string]any{"conversation": "office", "text": "mira", "attachments": paths}, nil)
	if resp.StatusCode != 400 || !strings.Contains(string(body), "max 10") {
		t.Fatalf("11 attachments = %d %s", resp.StatusCode, body)
	}
}

func (e *env) dialWS(token string) *websocket.Conn {
	e.t.Helper()
	u := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/ws?access_token=" + token
	c, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		e.t.Fatalf("dial ws: %v", err)
	}
	return c
}

func drainWS(c *websocket.Conn, quiet time.Duration) []string {
	var out []string
	for {
		_ = c.SetReadDeadline(time.Now().Add(quiet))
		_, msg, err := c.ReadMessage()
		if err != nil {
			return out
		}
		out = append(out, string(msg))
	}
}
