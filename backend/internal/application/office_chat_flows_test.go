package application_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// seedOfficeHistory writes a conversation whose earlier turn was written in office "o-legal" (secret text) and one
// written with no office (General's old history), so a turn asked from another office can be checked against them.
func seedOfficeHistory(t *testing.T, h *harness, conv string) {
	t.Helper()
	ctx := context.Background()
	if err := h.store.CreateConversation(ctx, domain.DemoOrgID, domain.Conversation{ID: conv, Title: conv, Participants: []string{"user", "sales"}, LastMessageAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for i, m := range []domain.Message{
		{ID: "h1", From: "user", To: "sales", Kind: domain.MsgChat, Text: "LEGALSECRET contrato confidencial", OfficeID: "o-legal", TurnID: "t-old1"},
		{ID: "h2", From: "sales", To: "user", Kind: domain.MsgChat, Text: "LEGALSECRET respuesta del contrato", OfficeID: "o-legal", TurnID: "t-old1"},
		{ID: "h3", From: "user", To: "sales", Kind: domain.MsgChat, Text: "GENERALVISIBLE saludo viejo", TurnID: "t-old2"},
	} {
		m.ConversationID, m.TS = conv, time.Now().Add(-time.Hour+time.Duration(i)*time.Second)
		if err := h.store.AddMessage(ctx, domain.DemoOrgID, m); err != nil {
			t.Fatal(err)
		}
	}
}

// What the runtime receives in each turn of the chat, by the agent that is asked.
type chatSpy struct {
	mu   sync.Mutex
	seen map[string][]string // agent -> the requests it received, as JSON
}

func (c *chatSpy) record(in application.ChatReplyRequest) {
	raw, _ := json.Marshal(in)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = map[string][]string{}
	}
	c.seen[in.Agent.ID] = append(c.seen[in.Agent.ID], string(raw))
}

func (c *chatSpy) all(agent string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.seen[agent], "\n")
}

// The consultation between agents (an agent of a 1:1 chat asks a colleague) works for the office the turn was asked
// from: neither the asked agent nor the colleague it consults receives what another office said in that chat, and a
// colleague who does not work for that office is not consulted at all.
func TestIsolation_ConsultBetweenAgentsKeepsToTheRequestsOffice(t *testing.T) {
	for _, c := range []struct {
		office           string
		consulted        bool     // accounting works for Ventas only; sales works for Legal and Ventas
		mine, notInReply []string // what the asked agent must receive / never receive
	}{
		{"o-ven", true, nil, []string{"LEGALSECRET", "GENERALVISIBLE"}},
		{"o-legal", false, []string{"LEGALSECRET"}, []string{"GENERALVISIBLE"}},
		{"", true, []string{"GENERALVISIBLE"}, []string{"LEGALSECRET"}}, // unknown office: what was written without one
	} {
		spy := &chatSpy{}
		rt := newChatRuntime(func(application.RouteRequest) (application.RouteResponse, error) {
			return application.RouteResponse{Intent: "question", Topic: "finance", Responders: []application.RouteResponder{primary("sales")},
				Consult: &application.RouteConsult{AgentID: "accounting", Reason: "es de finanzas"}}, nil
		})
		rt.reply = func(in application.ChatReplyRequest) (application.ChatReplyResponse, error) {
			spy.record(in)
			if in.Consult != nil {
				return application.ChatReplyResponse{Text: "El margen está bien.", Kind: "answer"}, nil
			}
			return application.ChatReplyResponse{Text: "Se lo pregunto a Tomás.",
				Consult: &application.ChatReplyConsult{ToAgentID: "accounting", Question: "¿Cómo está el margen?"}}, nil
		}
		h := newChat(t, rt, nil)
		h.orch.SetOfficeScope(scopeFake{})
		seedOfficeHistory(t, h, "agent:sales")
		if _, err := h.orch.PostChat(application.WithRequestOffice(context.Background(), c.office), "agent:sales", "¿cómo está el margen?"); err != nil {
			t.Fatal(err)
		}
		h.orch.Wait()
		if spy.all("sales") == "" {
			t.Fatalf("office %q: the asked agent did not answer", c.office)
		}
		if asked := spy.all("accounting") != ""; asked != c.consulted {
			t.Errorf("office %q: the colleague was consulted = %v, want %v", c.office, asked, c.consulted)
		}
		for _, agent := range []string{"sales", "accounting"} {
			for _, bad := range c.notInReply {
				if got := spy.all(agent); strings.Contains(got, bad) {
					t.Errorf("office %q: %s received %s: %s", c.office, agent, bad, got)
				}
			}
		}
		for _, want := range c.mine {
			if got := spy.all("sales"); !strings.Contains(got, want) {
				t.Errorf("office %q: the asked agent lost its own office's history (%s)", c.office, want)
			}
		}
	}
}
