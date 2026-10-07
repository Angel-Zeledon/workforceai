package api_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

func (e *env) messages(token, query string) ([]domain.Message, map[string]string) {
	e.t.Helper()
	resp, body := e.do("GET", "/api/v1/conversations/"+query, token, nil, nil)
	if resp.StatusCode != 200 {
		e.t.Fatalf("GET messages %s = %d %s", query, resp.StatusCode, body)
	}
	var out []domain.Message
	if err := json.Unmarshal(body, &out); err != nil {
		e.t.Fatalf("messages are a plain array: %v: %s", err, body)
	}
	return out, map[string]string{"more": resp.Header.Get("X-Has-More"), "next": resp.Header.Get("X-Next-Before")}
}

// waitMessages polls until the conversation has at least n messages.
func (e *env) waitMessages(token, conv string, n int) []domain.Message {
	e.t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if ms, _ := e.messages(token, conv+"/messages"); len(ms) >= n {
			return ms
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("timeout waiting for %d messages in %s", n, conv)
	return nil
}

func TestPostMessagesAcceptsAndTheTeamAnswersWithoutCreatingARequest(t *testing.T) {
	e := newEnv(t, opts{})
	resp, body := e.do("POST", "/api/v1/messages", "", map[string]string{"conversation": "office", "text": "hablemos del balance"}, nil)
	if resp.StatusCode != 202 {
		t.Fatalf("POST /messages = %d %s", resp.StatusCode, body)
	}
	var turn struct {
		MessageID string `json:"message_id"`
		TurnID    string `json:"turn_id"`
	}
	if err := json.Unmarshal(body, &turn); err != nil || turn.MessageID == "" || turn.TurnID == "" {
		t.Fatalf("202 body: %s", body)
	}
	ms := e.waitMessages("", "office", 2)
	if ms[0].ID != turn.MessageID || ms[0].From != "user" || ms[1].From != "accounting" || ms[1].TurnID != turn.TurnID {
		t.Fatalf("user message then the accountant's reply: %+v", ms)
	}
	// nothing but a chat happened
	_, raw := e.do("GET", "/api/v1/requests", "", nil, nil)
	if strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("requests: %s", raw)
	}
	// the conversation shows in the list under its logical id
	_, raw = e.do("GET", "/api/v1/conversations", "", nil, nil)
	if !strings.Contains(string(raw), `"id":"office"`) {
		t.Fatalf("conversations: %s", raw)
	}
}

func TestPostMessagesDefaultsToTheOfficeAndValidatesInput(t *testing.T) {
	e := newEnv(t, opts{})
	if c := e.status("POST", "/api/v1/messages", "", map[string]string{"text": "gracias"}); c != 202 {
		t.Fatalf("missing conversation defaults to office: %d", c)
	}
	e.waitMessages("", "office", 2)
	for name, c := range map[string]struct {
		body map[string]string
		want int
	}{
		"empty text":       {map[string]string{"conversation": "office", "text": " "}, 400},
		"bad conversation": {map[string]string{"conversation": "nope", "text": "hola"}, 400},
		"unknown agent":    {map[string]string{"conversation": "agent:ghost", "text": "hola"}, 404},
	} {
		if got := e.status("POST", "/api/v1/messages", "", c.body); got != c.want {
			t.Errorf("%s = %d, want %d", name, got, c.want)
		}
	}
	if got := e.status("POST", "/api/v1/messages", "", []byte(`{"text":`)); got != 400 {
		t.Errorf("malformed json = %d", got)
	}
}

func TestDirectChatThroughTheAPIAnswersOnlyThatAgent(t *testing.T) {
	e := newEnv(t, opts{})
	if c := e.status("POST", "/api/v1/messages", "", map[string]string{"conversation": "agent:legal", "text": "hola"}); c != 202 {
		t.Fatalf("status %d", c)
	}
	ms := e.waitMessages("", "agent:legal", 2)
	time.Sleep(150 * time.Millisecond)
	ms, _ = e.messages("", "agent:legal/messages")
	if len(ms) != 2 || ms[1].From != "legal" {
		t.Fatalf("only the lawyer answers: %+v", ms)
	}
	// the office channel stays empty (and a never-started chat is an empty list, not a 404)
	if office, _ := e.messages("", "office/messages"); len(office) != 0 {
		t.Fatalf("office: %+v", office)
	}
}

func TestMessagesPaginationKeepsThePlainArray(t *testing.T) {
	e := newEnv(t, opts{})
	for i := 1; i <= 3; i++ {
		e.status("POST", "/api/v1/messages", "", map[string]string{"conversation": "office", "text": "hablemos del balance"})
		e.waitMessages("", "office", 2*i)
	}
	all, h := e.messages("", "office/messages")
	if len(all) != 6 || h["more"] != "" {
		t.Fatalf("without limit: the full list, no paging headers: %d %v", len(all), h)
	}
	page, h := e.messages("", "office/messages?limit=4")
	if len(page) != 4 || h["more"] != "true" || h["next"] != page[0].ID || page[3].ID != all[5].ID {
		t.Fatalf("newest page: %d %v", len(page), h)
	}
	older, h := e.messages("", "office/messages?limit=4&before="+h["next"])
	if len(older) != 2 || h["more"] != "false" || older[0].ID != all[0].ID {
		t.Fatalf("older page: %d %v", len(older), h)
	}
	if c := e.status("GET", "/api/v1/conversations/office/messages?limit=0", "", nil); c != 400 {
		t.Errorf("limit=0 = %d", c)
	}
	if c := e.status("GET", "/api/v1/conversations/office/messages?limit=2&before=nope", "", nil); c != 404 {
		t.Errorf("unknown cursor = %d", c)
	}
	if c := e.status("GET", "/api/v1/conversations/agent:ghost/messages", "", nil); c != 404 {
		t.Errorf("unknown agent chat = %d", c)
	}
}

func TestPostMessagesNeedsAuthAndTheWritePermission(t *testing.T) {
	e := newEnv(t, opts{auth: true})
	owner := e.register("owner@example.com", "acme")
	viewer := e.member(owner, "viewer@example.com", auth.RoleViewer)
	body := map[string]string{"conversation": "office", "text": "gracias"}
	if c := e.status("POST", "/api/v1/messages", "", body); c != 401 {
		t.Errorf("anonymous = %d, want 401", c)
	}
	if c := e.status("POST", "/api/v1/messages", viewer, body); c != 403 {
		t.Errorf("viewer = %d, want 403 (conversations:write)", c)
	}
	if c := e.status("POST", "/api/v1/messages", owner.AccessToken, body); c != 202 {
		t.Errorf("owner = %d, want 202", c)
	}
	if c := e.status("GET", "/api/v1/conversations/office/messages", viewer, nil); c != 200 {
		t.Errorf("a viewer may read the chat: %d", c)
	}
}
