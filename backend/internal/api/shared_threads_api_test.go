package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// historySpy records the chat history the model is given for each routed turn (and then fails the routing, so the
// turn falls back to the local rules).
type historySpy struct {
	routeSpy
	hmu     sync.Mutex
	history map[string]string // turn text -> history texts
}

func (h *historySpy) Route(ctx context.Context, in application.RouteRequest) (application.RouteResponse, error) {
	h.hmu.Lock()
	if h.history == nil {
		h.history = map[string]string{}
	}
	var b strings.Builder
	for _, it := range in.History {
		b.WriteString(it.From + ": " + it.Text + "\n")
	}
	h.history[in.Text] = b.String()
	h.hmu.Unlock()
	return h.routeSpy.Route(ctx, in)
}

func (h *historySpy) historyOf(t *testing.T, text string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.hmu.Lock()
		got, ok := h.history[text]
		h.hmu.Unlock()
		if ok {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no turn %q was routed", text)
	return ""
}

// seedShared writes the shared office chat (ana asked legal, legal answered, sales answered, and then sales consulted
// legal: the newest message) and the 1:1 history the organization shared with sales before private threads existed,
// with a consultation of legal in it.
func seedShared(t *testing.T, p *privateWork) {
	t.Helper()
	ctx, org, now, st := context.Background(), p.owner.OrgID, time.Now().UTC().Add(-time.Hour), p.e.store
	for _, c := range []string{domain.ChatOffice, "agent:sales"} {
		if _, err := st.GetConversation(ctx, org, c); err != nil {
			if err := st.CreateConversation(ctx, org, domain.Conversation{ID: c, Title: c, Participants: []string{"user"}, LastMessageAt: now}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i, m := range []domain.Message{
		{ID: "o-1", ConversationID: domain.ChatOffice, From: "user", To: "legal", Kind: domain.MsgChat, Text: "pregunta-a-legal", TurnID: "o-t1"},
		{ID: "o-2", ConversationID: domain.ChatOffice, From: "legal", To: "user", Kind: domain.MsgChat, Text: "respuesta-secreta-legal", TurnID: "o-t1"},
		{ID: "o-3", ConversationID: domain.ChatOffice, From: "sales", To: "user", Kind: domain.MsgChat, Text: "respuesta-ventas", TurnID: "o-t2"},
		{ID: "s-1", ConversationID: "agent:sales", From: "user", To: "sales", Kind: domain.MsgChat, Text: "hola-ventas", TurnID: "s-t1"},
		{ID: "s-2", ConversationID: "agent:sales", From: "legal", To: "sales", Kind: domain.MsgConsult, Text: "consulta-secreta-de-legal", TurnID: "s-t1"},
		{ID: "s-3", ConversationID: "agent:sales", From: "sales", To: "user", Kind: domain.MsgChat, Text: "ventas-responde", TurnID: "s-t1"},
		{ID: "o-4", ConversationID: domain.ChatOffice, From: "sales", To: "legal", Kind: domain.MsgConsult, Text: "consulta-ventas-a-legal", TurnID: "o-t2"},
	} {
		m.TS = now.Add(time.Duration(i) * time.Second)
		if err := st.AddMessage(ctx, org, m); err != nil {
			t.Fatal(err)
		}
	}
}

// D3, round 2: a private office's messages stay out of every stored read of a shared thread for whoever does not see
// that office's work: /export and /compact of the office chat, the earlier shared history of an agent of General,
// the preview of the thread list and the history the model gets when a non-member writes in the office chat.
func TestSharedThreadsLeaveOutPrivateMessagesEverywhere(t *testing.T) {
	rt := &historySpy{}
	p := newPrivateWork(t, withConvContext, func(o *opts) { o.runtime = rt; o.drive = true })
	seedShared(t, p)
	secret := []string{"secreta-legal", "pregunta-a-legal", "consulta-secreta-de-legal", "consulta-ventas-a-legal"}
	leaks := func(s string) string {
		for _, x := range secret {
			if strings.Contains(s, x) {
				return x
			}
		}
		return ""
	}

	// the earlier shared history of sales (a General agent): the consultation of legal is legal's
	beto := p.body("GET", "/conversations/agent:sales/messages", p.beto, 200)
	if x := leaks(beto); x != "" || !strings.Contains(beto, "ventas-responde") {
		t.Errorf("beto: agent:sales history leaks %q: %s", x, beto)
	}
	if ana := p.body("GET", "/conversations/agent:sales/messages", p.ana, 200); !strings.Contains(ana, "consulta-secreta-de-legal") {
		t.Errorf("ana: agent:sales history lacks legal's consultation: %s", ana)
	}

	// the thread list: General's preview is the consultation sales → legal
	for key, e := range threadEntries(t, p.body("GET", "/chats/threads", p.beto, 200)) {
		if x := leaks(e.Preview); x != "" {
			t.Errorf("beto: the %s thread previews %q", key, x)
		}
	}
	if g := threadEntries(t, p.body("GET", "/chats/threads", p.ana, 200))["general"]; !strings.Contains(g.Preview, "consulta-ventas-a-legal") {
		t.Errorf("ana: General's preview = %+v", g)
	}

	// /export of the office chat
	export := func(token string) string {
		t.Helper()
		out := p.e.json("POST", api1+"/commands/run", token, map[string]any{"command": "export", "context": map[string]any{"surface": "office", "conversation_id": "office"}}, 200)
		r := resultOf(out)
		resp, raw := p.e.do("GET", r["url"].(string), token, nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("download = %d %s", resp.StatusCode, raw)
		}
		return string(raw)
	}
	if got := export(p.beto); leaks(got) != "" || !strings.Contains(got, "respuesta-ventas") {
		t.Errorf("beto /export office: %s", got)
	}
	if got := export(p.ana); !strings.Contains(got, "respuesta-secreta-legal") {
		t.Errorf("ana /export office lacks legal: %s", got)
	}

	// the model's history when beto writes in the office chat; ana's includes legal
	for token, text := range map[string]string{p.beto: "hola-de-beto", p.ana: "hola-de-ana"} {
		if r, b := p.e.do("POST", api1+"/messages", token, map[string]any{"conversation": "office", "text": text}, nil); r.StatusCode >= 300 {
			t.Fatalf("post %s = %d %s", text, r.StatusCode, b)
		}
	}
	if h := rt.historyOf(t, "hola-de-beto"); leaks(h) != "" || !strings.Contains(h, "respuesta-ventas") {
		t.Errorf("the model gets legal's messages for beto: %s", h)
	}
	if h := rt.historyOf(t, "hola-de-ana"); !strings.Contains(h, "respuesta-secreta-legal") {
		t.Errorf("the model lacks legal's messages for ana: %s", h)
	}

	// /compact of the office chat (the local summary quotes the messages)
	compact := func(token string) string {
		t.Helper()
		return p.command(token, map[string]any{"command": "compact", "context": map[string]any{"surface": "office", "conversation_id": "office"}})
	}
	if got := compact(p.ana); !strings.Contains(got, "secreta-legal") {
		t.Errorf("ana /compact office lacks legal: %s", got)
	}
	// ana's summary of the shared chat now replaces its older messages for the model: beto's turns do not get it,
	// nor does beto's own /compact carry it over
	if r, b := p.e.do("POST", api1+"/messages", p.beto, map[string]any{"conversation": "office", "text": "otra-de-beto"}, nil); r.StatusCode >= 300 {
		t.Fatalf("post = %d %s", r.StatusCode, b)
	}
	if h := rt.historyOf(t, "otra-de-beto"); leaks(h) != "" {
		t.Errorf("the model gets ana's summary with legal for beto: %s", h)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, got := p.cmdRaw(p.beto, map[string]any{"command": "compact", "context": map[string]any{"surface": "office", "conversation_id": "office"}})
		if code == 200 {
			if leaks(got) != "" {
				t.Errorf("beto /compact office: %s", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("beto /compact office = %d %s", code, got)
		}
		time.Sleep(20 * time.Millisecond) // the reply to beto's turn (two spoken messages) may still be on its way
	}
}

// D3, round 3: the preview of a conversation row (GET /conversations, the row PATCH answers with) never quotes what an
// agent whose work the person does not see said or was told: here sales consulting legal, the newest message of the
// earlier shared history with sales and of the office chat.
func TestConversationPreviewsStaySealed(t *testing.T) {
	p := newPrivateWork(t)
	seedShared(t, p)
	ctx, org := context.Background(), p.owner.OrgID
	if err := p.e.store.AddMessage(ctx, org, domain.Message{ID: "s-4", ConversationID: "agent:sales", From: "sales", To: "legal",
		Kind: domain.MsgConsult, Text: "consulta-final-de-ventas-a-legal", TurnID: "s-t2", TS: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	type row struct {
		ID      string `json:"id"`
		Preview string `json:"preview"`
	}
	rows := func(token string) map[string]string {
		t.Helper()
		var rs []row
		if err := json.Unmarshal([]byte(p.body("GET", "/conversations?archived=all", token, 200)), &rs); err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, r := range rs {
			out[r.ID] = r.Preview
		}
		return out
	}
	for id, preview := range rows(p.beto) {
		if strings.Contains(preview, "consulta") {
			t.Errorf("beto: the %s row previews %q", id, preview)
		}
	}
	ana := rows(p.ana)
	if !strings.Contains(ana["agent:sales"], "consulta-final-de-ventas-a-legal") || !strings.Contains(ana["office"], "consulta-ventas-a-legal") {
		t.Errorf("ana's previews = %v", ana)
	}

	for token, want := range map[string]bool{p.beto: false, p.ana: true} {
		got := p.e.json("PATCH", api1+"/conversations/office", token, map[string]any{"archived": false}, 200)
		if strings.Contains(fmt.Sprint(got["preview"]), "consulta-ventas-a-legal") != want {
			t.Errorf("PATCH /conversations/office (member %v) answers the preview %q", want, got["preview"])
		}
	}
}
