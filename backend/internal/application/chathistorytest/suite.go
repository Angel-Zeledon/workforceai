// Package chathistorytest is the behaviour suite of application.ChatHistoryStore.
// The in-memory store and the Postgres store run the same suite, so both give
// the same answers (the General and per-agent views, search, paging, unread...).
package chathistorytest

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Fixture is a store seeded with the organization Org and its agents
// (sales, accounting, assistant...), plus a second empty organization Other.
type Fixture struct {
	Org, Other string
	Core       application.Store
	Hist       application.ChatHistoryStore
}

type conv struct {
	id, title string
	req       *string
}

// Run runs the suite; mk returns a fresh fixture for every subtest.
func Run(t *testing.T, mk func(t *testing.T) Fixture) {
	t.Run("views go hand in hand", func(t *testing.T) { viewsGoHandInHand(t, mk(t)) })
	t.Run("paging", func(t *testing.T) { paging(t, mk(t)) })
	t.Run("conversation list, filter and search", func(t *testing.T) { listAndSearch(t, mk(t)) })
	t.Run("rename and archive", func(t *testing.T) { renameArchive(t, mk(t)) })
	t.Run("unread and read markers", func(t *testing.T) { unread(t, mk(t)) })
	t.Run("delete", func(t *testing.T) { del(t, mk(t)) })
	t.Run("automatic title", func(t *testing.T) { autoTitle(t, mk(t)) })
	t.Run("other organization sees nothing", func(t *testing.T) {
		f := mk(t)
		if f.Other == "" {
			t.Skip("single-tenant store")
		}
		isolation(t, f)
	})
}

var base = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

type seeded struct {
	f      Fixture
	n      int
	future bool              // timestamps after now (to test messages newer than a read marker)
	ids    map[string]string // label -> message id
}

func ptr(s string) *string { return &s }

// seed builds the scenario used by most tests:
//
//	T1 office: user "hola" -> assistant + accounting answer
//	T2 office: user "revisa el margen" -> accounting answers
//	T3 agent:sales 1:1: user "estado del pipeline, crecimos 100% este mes" -> sales answers
//	T4 office: user "prepara la propuesta" -> assistant confirms, sales speaks, a request is created
//	rq1 (request conversation): assistant delegates to sales
func seed(t *testing.T, f Fixture) *seeded {
	t.Helper()
	s := &seeded{f: f, ids: map[string]string{}}
	ctx := context.Background()
	for _, c := range []conv{{id: "office", title: "Oficina"}, {id: "agent:sales", title: "Chat con Sales"}, {id: "agent:accounting", title: "Chat con Accounting"}, {id: "rq1", title: "Preparar la propuesta", req: ptr("req-1")}} {
		err := f.Core.CreateConversation(ctx, f.Org, domain.Conversation{ID: c.id, Title: c.title, Participants: []string{"user"}, RequestID: c.req, LastMessageAt: base})
		if err != nil {
			t.Fatalf("create conversation %s: %v", c.id, err)
		}
	}
	s.msg(t, "u1", "office", "user", "all", "T1", "hola", "")
	s.msg(t, "a1", "office", "assistant", "user", "T1", "Hola, aquí estamos", "")
	s.msg(t, "c1", "office", "accounting", "user", "T1", "Hola, soy contabilidad", "")
	s.msg(t, "u2", "office", "user", "all", "T2", "revisa el margen", "")
	s.msg(t, "c2", "office", "accounting", "user", "T2", "El margen es del 31 por ciento", "")
	s.msg(t, "u3", "agent:sales", "user", "sales", "T3", "estado del pipeline, crecimos 100% este mes", "")
	s.msg(t, "s3", "agent:sales", "sales", "user", "T3", "Pipeline sano", "")
	s.msg(t, "u4", "office", "user", "all", "T4", "prepara la propuesta", "")
	s.msg(t, "a4", "office", "assistant", "user", "T4", "Voy con ello", "req-1")
	s.msg(t, "s4", "office", "sales", "user", "T4", "Me encargo del borrador", "")
	s.msg(t, "d1", "rq1", "assistant", "sales", "", "Tarea para ventas", "")
	return s
}

func (s *seeded) msg(t *testing.T, label, conv, from, to, turn, text, req string) {
	t.Helper()
	s.n++
	m := domain.Message{ID: fmt.Sprintf("%s-%s", s.f.Org, label), ConversationID: conv, From: from, To: to, Kind: domain.MsgChat, Text: text,
		TS: base.Add(time.Duration(s.n) * time.Second), TurnID: turn}
	if s.future {
		m.TS = time.Now().UTC().Add(time.Hour + time.Duration(s.n)*time.Second).Truncate(time.Microsecond)
	}
	if req != "" {
		m.RequestID = ptr(req)
	}
	if err := s.f.Core.AddMessage(context.Background(), s.f.Org, m); err != nil {
		t.Fatalf("add message %s: %v", label, err)
	}
	s.ids[label] = m.ID
}

func labels(s *seeded, ms []application.ChatMessage) []string {
	rev := map[string]string{}
	for k, v := range s.ids {
		rev[v] = k
	}
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, rev[m.ID])
	}
	return out
}

func thread(t *testing.T, s *seeded, key string, before string, limit int) ([]application.ChatMessage, bool) {
	t.Helper()
	ms, more, err := s.f.Hist.ChatThreadMessages(context.Background(), s.f.Org, application.ChatThreadQuery{Thread: key, BeforeID: before, Limit: limit})
	if err != nil {
		t.Fatalf("thread %s: %v", key, err)
	}
	return ms, more
}

func eq(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func viewsGoHandInHand(t *testing.T, f Fixture) {
	s := seed(t, f)
	// General: everything of the office and of every 1:1 chat, chronologically; request conversations stay out.
	gen, more := thread(t, s, application.ThreadGeneral, "", 100)
	if more {
		t.Fatal("no more pages expected")
	}
	eq(t, "general", labels(s, gen), []string{"u1", "a1", "c1", "u2", "c2", "u3", "s3", "u4", "a4", "s4"})

	// Accounting: its turns (the user message that was routed to it and its answers), not other agents' business.
	acc, _ := thread(t, s, "accounting", "", 100)
	eq(t, "accounting", labels(s, acc), []string{"u1", "c1", "u2", "c2"})
	// Sales: its 1:1 chat, and the office turn in which it spoke.
	sales, _ := thread(t, s, "sales", "", 100)
	eq(t, "sales", labels(s, sales), []string{"u3", "s3", "u4", "s4"})
	// An agent that never took part sees nothing.
	if hr, _ := thread(t, s, "hr", "", 100); len(hr) != 0 {
		t.Fatalf("hr thread = %v", labels(s, hr))
	}

	// Same message, not a copy: the id (and the stored row) is the one in General.
	inGen := map[string]application.ChatMessage{}
	for _, m := range gen {
		inGen[m.ID] = m
	}
	for _, m := range append(slices.Clone(acc), sales...) {
		g, ok := inGen[m.ID]
		if !ok || g.Text != m.Text || g.ConversationID != m.ConversationID || !g.TS.Equal(m.TS) {
			t.Fatalf("agent view message %s is not the General message: %+v vs %+v", m.ID, m, g)
		}
	}
	// ...and a 1:1 message shows in General with its agent badge.
	if m := inGen[s.ids["u3"]]; !slices.Contains(m.AgentIDs, "sales") {
		t.Fatalf("u3 agent_ids = %v", m.AgentIDs)
	}
	// The user's office greeting carries every agent that took part in the turn.
	if m := inGen[s.ids["u1"]]; !slices.Contains(m.AgentIDs, "assistant") || !slices.Contains(m.AgentIDs, "accounting") || slices.Contains(m.AgentIDs, "sales") {
		t.Fatalf("u1 agent_ids = %v", m.AgentIDs)
	}
	if m := inGen[s.ids["u2"]]; len(m.AgentIDs) != 1 || m.AgentIDs[0] != "accounting" {
		t.Fatalf("u2 agent_ids = %v", m.AgentIDs)
	}
	// The request the work created is referenced from the message.
	if m := inGen[s.ids["a4"]]; m.RequestID == nil || *m.RequestID != "req-1" {
		t.Fatalf("a4 request = %v", m.RequestID)
	}

	// A message sent later in an agent's 1:1 chat shows up in General (vice versa).
	s.msg(t, "u5", "agent:accounting", "user", "accounting", "T5", "¿y el flujo de caja?", "")
	gen, _ = thread(t, s, application.ThreadGeneral, "", 100)
	if got := labels(s, gen); got[len(got)-1] != "u5" {
		t.Fatalf("general after 1:1 = %v", got)
	}
	acc, _ = thread(t, s, "accounting", "", 100)
	if got := labels(s, acc); got[len(got)-1] != "u5" {
		t.Fatalf("accounting after 1:1 = %v", got)
	}
}
