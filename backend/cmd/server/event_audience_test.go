package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/events"
	"aiworkforce/backend/internal/infrastructure/memory"
	"aiworkforce/backend/internal/offices"
)

// eventAudienceFix: Privada (private office, member u-pri, agent a-priv), Abierta (open, member u-ana, agent a-open),
// a-gen in General; u-boss is an admin who is in no office and u-eve a plain person. A request r-priv has a task of
// a-priv and was asked for by u-eve (who uses a-priv from the directory); r-gen has a task of a-gen.
type eventAudienceFix struct {
	ctx     context.Context
	rec     *application.Recorder
	store   *memory.Store
	sockets map[string]*events.Client
}

func newEventAudienceFix(t *testing.T) *eventAudienceFix {
	t.Helper()
	ctx := application.WithOrg(context.Background(), "org1")
	osvc := offices.New(offices.Config{Store: offices.NewMemStore(),
		Departments: func(context.Context, string) ([]offices.Dept, error) {
			return []offices.Dept{{ID: "d-open", Name: "Abierta"}, {ID: "d-priv", Name: "Privada"}}, nil
		},
		Placements: func(context.Context, string) (map[string]string, error) {
			return map[string]string{"u-ana": "d-open", "u-pri": "d-priv"}, nil
		},
		AgentDepts: func(context.Context, string) (map[string]string, error) {
			return map[string]string{"a-open": "d-open", "a-priv": "d-priv"}, nil
		},
		Agents: func(context.Context, string) ([]offices.AgentRef, error) {
			return []offices.AgentRef{{ID: "a-open"}, {ID: "a-priv"}, {ID: "a-gen"}}, nil
		},
		People: func(context.Context, string) ([]offices.Person, error) {
			return []offices.Person{{UserID: "u-boss", Name: "Boss", Admin: true}, {UserID: "u-ana", Name: "Ana"},
				{UserID: "u-pri", Name: "Pri"}, {UserID: "u-eve", Name: "Eve"}}, nil
		}})
	boss := offices.Viewer{UserID: "u-boss", Admin: true}
	if _, err := osvc.Enable(ctx, "org1", boss, "d-open", offices.Open); err != nil {
		t.Fatal(err)
	}
	if _, err := osvc.Enable(ctx, "org1", boss, "d-priv", offices.Private); err != nil {
		t.Fatal(err)
	}
	st := memory.New()
	now := time.Now().UTC()
	for _, r := range []string{"r-priv", "r-gen", "r-new"} {
		if err := st.CreateRequest(ctx, "org1", domain.Request{ID: r, Text: "pedido " + r, Status: domain.RequestRunning, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tk := range []domain.Task{{ID: "t-priv", RequestID: "r-priv", AgentID: "a-priv", Title: "cuentas"},
		{ID: "t-gen", RequestID: "r-gen", AgentID: "a-gen", Title: "agenda"}} {
		if err := st.CreateTask(ctx, "org1", tk); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CreateConversation(ctx, "org1", domain.Conversation{ID: "c-priv", Participants: []string{"user", "assistant"}, RequestID: ptr("r-priv")}); err != nil {
		t.Fatal(err)
	}
	asked := map[string]string{"r-priv": "u-eve", "r-new": "u-eve"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := events.NewHub(log)
	f := &eventAudienceFix{ctx: ctx, store: st, sockets: map[string]*events.Client{}}
	for _, u := range []string{"u-boss", "u-ana", "u-pri", "u-eve"} {
		f.sockets[u] = hub.RegisterUser("org1", u)
	}
	f.rec = &application.Recorder{OrgID: "org1", Store: st, Pub: events.LocalBus{Hub: hub}, Log: log}
	f.rec.SetAudienceFor(officeAudience(osvc, st, func(_ context.Context, _, id string) string { return asked[id] }, log))
	return f
}

func ptr(s string) *string { return &s }

// got drains every socket and returns, per user, the event types they received.
func (f *eventAudienceFix) got() map[string][]string {
	out := map[string][]string{}
	for u, c := range f.sockets {
		for {
			select {
			case raw := <-c.Send:
				var ev domain.Event
				_ = json.Unmarshal(raw, &ev)
				out[u] = append(out[u], ev.Type)
				continue
			default:
			}
			break
		}
	}
	return out
}

func (f *eventAudienceFix) saved() []domain.Event {
	evs, _ := f.store.ListEvents(f.ctx, "org1", domain.EventQuery{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)})
	return evs
}

func TestPrivateAgentEventsReachOnlyMembers(t *testing.T) {
	task := domain.Task{ID: "t-priv", RequestID: "r-priv", AgentID: "a-priv", Title: "cuentas"}
	cases := []struct {
		name string
		ctx  func(context.Context) context.Context
		act  application.Action
	}{
		{"message.sent of the agent", nil, application.Action{Type: domain.EvMessageSent, AgentID: "a-priv", Text: "a-priv → assistant: listo",
			Payload: map[string]any{"message": domain.Message{ID: "m1", ConversationID: "c-priv", From: "a-priv", To: "assistant", Kind: "delegation", Text: "listo"}}}},
		{"message.sent of the user in the request's conversation", nil, application.Action{Type: domain.EvMessageSent,
			Payload: map[string]any{"message": domain.Message{ID: "m2", ConversationID: "c-priv", From: "user", To: "assistant", Kind: "chat", Text: "secreto"}}}},
		{"task.completed", nil, application.Action{Type: domain.EvTaskCompleted, AgentID: "a-priv", Text: "Tarea completada",
			Payload: map[string]any{"task": task}}},
		{"report.created (assistant, private request)", nil, application.Action{Type: domain.EvReportCreated, AgentID: "assistant", Text: "Informe",
			Payload: map[string]any{"report": domain.Report{ID: "rep1", RequestID: "r-priv", Title: "Cierre"}}}},
		{"approval.requested", nil, application.Action{Type: domain.EvApprovalRequest, AgentID: "a-priv", Text: "Aprobación requerida",
			Payload: map[string]any{"approval": domain.Approval{ID: "ap1", TaskID: "t-priv", AgentID: "a-priv", Title: "pagar"}}}},
		{"request.received addressed to the agent", func(c context.Context) context.Context { return application.WithWorkAgents(c, "a-priv") },
			application.Action{Type: domain.EvRequestReceived, Text: "Nuevo pedido", Payload: map[string]any{"request_id": "r-new", "text": "secreto"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newEventAudienceFix(t)
			ctx := f.ctx
			if c.ctx != nil {
				ctx = c.ctx(ctx)
			}
			f.rec.Emit(ctx, c.act)
			got := f.got()
			if !slices.Contains(got["u-pri"], c.act.Type) {
				t.Errorf("the member must receive it: %v", got)
			}
			if !slices.Contains(got["u-eve"], c.act.Type) {
				t.Errorf("whoever asked for the request must receive it: %v", got)
			}
			for _, u := range []string{"u-boss", "u-ana"} {
				if len(got[u]) != 0 {
					t.Errorf("%s (not a member) received %v", u, got[u])
				}
			}
			for _, ev := range f.saved() {
				if ev.Type == c.act.Type {
					t.Errorf("a private office event must stay out of the organization's event log: %+v", ev)
				}
			}
			if c.act.Text != "" {
				if !slices.Contains(got["u-pri"], domain.EvActivityLogged) {
					t.Errorf("the member must receive the activity line live: %v", got["u-pri"])
				}
				items, _ := f.store.ListActivity(f.ctx, "org1", "", 10)
				if len(items) != 1 {
					t.Errorf("the activity line must be kept (GET /activity filters it): %+v", items)
				}
			}
		})
	}
}

func TestGeneralAgentEventsReachEveryone(t *testing.T) {
	f := newEventAudienceFix(t)
	f.rec.Emit(f.ctx, application.Action{Type: domain.EvTaskCompleted, AgentID: "a-gen", Text: "Tarea completada",
		Payload: map[string]any{"task": domain.Task{ID: "t-gen", RequestID: "r-gen", AgentID: "a-gen"}}})
	f.rec.Emit(f.ctx, application.Action{Type: domain.EvTaskCompleted, AgentID: "a-open",
		Payload: map[string]any{"task": domain.Task{ID: "t-open", AgentID: "a-open"}}})
	got := f.got()
	for _, u := range []string{"u-boss", "u-ana", "u-pri", "u-eve"} {
		if n := len(got[u]); n != 3 { // two task.completed + one activity.logged
			t.Errorf("%s received %v, want everything", u, got[u])
		}
	}
	if n := len(f.saved()); n != 2 {
		t.Errorf("events of General and open offices stay in the event log: %d", n)
	}
}

// A private chat thread already names its audience: it is kept as is (no office members added, no activity).
func TestCallerAudienceIsKept(t *testing.T) {
	f := newEventAudienceFix(t)
	f.rec.Emit(f.ctx, application.Action{Type: domain.EvChatMessage, AgentID: "a-priv", Audience: []string{"u-eve"}, Text: "x",
		Payload: map[string]any{"from": "a-priv", "text": "hola"}})
	got := f.got()
	if len(got["u-eve"]) != 1 || len(got["u-pri"]) != 0 || len(got["u-boss"]) != 0 {
		t.Errorf("the caller's audience must be kept as is: %v", got)
	}
	if items, _ := f.store.ListActivity(f.ctx, "org1", "", 10); len(items) != 0 {
		t.Errorf("a private chat event logs no activity: %+v", items)
	}
}

// A private office nobody is in yet: its events reach nobody (never the whole organization).
func TestPrivateOfficeWithoutMembersReachesNobody(t *testing.T) {
	f := newEventAudienceFix(t)
	osvc := offices.New(offices.Config{Store: offices.NewMemStore(),
		Departments: func(context.Context, string) ([]offices.Dept, error) {
			return []offices.Dept{{ID: "d-empty", Name: "Vacía"}}, nil
		},
		AgentDepts: func(context.Context, string) (map[string]string, error) {
			return map[string]string{"a-x": "d-empty"}, nil
		},
		Agents: func(context.Context, string) ([]offices.AgentRef, error) { return []offices.AgentRef{{ID: "a-x"}}, nil },
		People: func(context.Context, string) ([]offices.Person, error) {
			return []offices.Person{{UserID: "u-boss", Name: "Boss", Admin: true}, {UserID: "u-eve", Name: "Eve"}}, nil
		}})
	if _, err := osvc.Enable(f.ctx, "org1", offices.Viewer{UserID: "u-boss", Admin: true}, "d-empty", offices.Private); err != nil {
		t.Fatal(err)
	}
	f.rec.SetAudienceFor(officeAudience(osvc, f.store, nil, slog.New(slog.NewTextHandler(io.Discard, nil))))
	f.rec.Emit(f.ctx, application.Action{Type: domain.EvTaskStarted, AgentID: "a-x", Text: "empieza", Payload: map[string]any{"task": domain.Task{ID: "t-x", AgentID: "a-x"}}})
	if got := f.got(); len(got) != 0 {
		t.Errorf("nobody may receive it: %v", got)
	}
}
