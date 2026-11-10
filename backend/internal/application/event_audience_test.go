package application_test

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
)

type capturePub struct {
	mu  sync.Mutex
	evs []domain.Event
}

func (c *capturePub) Publish(_ context.Context, e domain.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evs = append(c.evs, e)
	return nil
}

func TestRefsOfReadsEnvelopePayloadAndContext(t *testing.T) {
	ctx := application.WithWorkAgents(context.Background(), "a-chat")
	ev := domain.Event{Type: domain.EvApprovalRequest, AgentID: "a-env", Payload: map[string]any{
		"approval": domain.Approval{ID: "ap", TaskID: "t1", AgentID: "a-ap", RequestedBy: "u-eve"},
		"report":   domain.Report{RequestID: "r1", Contributors: []string{"a-c1", "a-c2"}},
		"message":  domain.Message{ConversationID: "c1", From: "user", To: "a-to", AuthorID: "u-ana"},
		"actor":    "agent:a-x", // not a reference key
	}}
	r := application.RefsOf(ctx, ev)
	for _, a := range []string{"a-env", "a-chat", "a-ap", "a-c1", "a-c2", "a-to"} {
		if !slices.Contains(r.Agents, a) {
			t.Errorf("agent %s missing: %v", a, r.Agents)
		}
	}
	if slices.Contains(r.Agents, "a-x") {
		t.Errorf("only reference keys name agents: %v", r.Agents)
	}
	if !slices.Equal(r.Requests, []string{"r1"}) || !slices.Equal(r.Tasks, []string{"t1"}) || !slices.Equal(r.Conversations, []string{"c1"}) {
		t.Errorf("refs: %+v", r)
	}
	if !slices.Equal(r.People, []string{"u-ana", "u-eve"}) {
		t.Errorf("people: %v", r.People)
	}
}

// A scoped event: out of the event log, delivered to its audience (never to everyone when nobody may see it), its
// activity line kept and delivered to the same audience. Without the rule, nothing changes.
func TestRecorderScopedEvents(t *testing.T) {
	ctx := application.WithOrg(context.Background(), "org1")
	st, pub := memory.New(), &capturePub{}
	rec := &application.Recorder{OrgID: "org1", Store: st, Pub: pub, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	aud := map[string][]string{"a-priv": {"u-pri"}, "a-empty": nil}
	rec.SetAudienceFor(func(_ context.Context, org string, ev domain.Event) ([]string, bool) {
		if org != "org1" {
			t.Errorf("org %q", org)
		}
		u, ok := aud[ev.AgentID]
		return u, ok
	})
	rec.Emit(ctx, application.Action{Type: domain.EvTaskStarted, AgentID: "a-priv", Text: "empieza"})
	rec.Emit(ctx, application.Action{Type: domain.EvTaskStarted, AgentID: "a-empty", Text: "empieza"})
	rec.Emit(ctx, application.Action{Type: domain.EvTaskStarted, AgentID: "a-gen", Text: "empieza"})
	rec.Activity(ctx, "a-priv", "note", "nota")
	if len(pub.evs) != 7 {
		t.Fatalf("published %d events: %+v", len(pub.evs), pub.evs)
	}
	for i, ev := range pub.evs {
		switch ev.AgentID {
		case "a-priv":
			if !slices.Equal(ev.Audience, []string{"u-pri"}) {
				t.Errorf("event %d (%s): audience %v", i, ev.Type, ev.Audience)
			}
		case "a-empty": // an audience no socket has: never empty (that would be the whole organization)
			if len(ev.Audience) != 1 || ev.Audience[0] == "" || ev.Audience[0] == "u-pri" {
				t.Errorf("event %d (%s): audience %v", i, ev.Type, ev.Audience)
			}
		default:
			if ev.Audience != nil {
				t.Errorf("event %d (%s) of General: audience %v", i, ev.Type, ev.Audience)
			}
		}
	}
	items, _ := st.ListActivity(ctx, "org1", "", 10)
	if len(items) != 4 {
		t.Errorf("every activity line is kept: %d", len(items))
	}
	rec.SetAudienceFor(nil)
	rec.Emit(ctx, application.Action{Type: domain.EvTaskStarted, AgentID: "a-priv"})
	if ev := pub.evs[len(pub.evs)-1]; ev.Audience != nil {
		t.Errorf("without the rule nothing is scoped: %v", ev.Audience)
	}
}
