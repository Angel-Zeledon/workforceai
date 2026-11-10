package api_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/officeaudience"
)

// sealedEvents is privateWork with the live-event rule of D3 installed on the recorder, as the server wires it.
func sealedEvents(t *testing.T) (*privateWork, *api.Deps, context.Context) {
	t.Helper()
	var d *api.Deps
	p := newPrivateWork(t, func(o *opts) {
		prev := o.deps
		o.deps = func(x *api.Deps) {
			prev(x)
			d = x
		}
	})
	d.Rec.SetAudienceFor(officeaudience.New(d.Offices, p.e.store, func(c context.Context, org, id string) string {
		return d.Orch.RequestedBy(application.WithOrg(c, org), id)
	}, slog.New(slog.NewTextHandler(io.Discard, nil))))
	org := p.owner.OrgID
	// the offices model reads the people of the organization as someone of it (the server reads them directly)
	ctx := auth.WithPrincipal(application.WithOrg(context.Background(), org), auth.Principal{UserID: p.e.userID(p.owner.AccessToken), OrgID: org, Role: auth.RoleOwner})
	return p, d, ctx
}

// The activity line of a sealed event is the private office's: GET /activity shows it to a member and never to an
// admin outside the office, also when the event names no agent (a new request addressed to the office's agent) or
// names an agent of General (the assistant's report of a private request).
func TestSealedEventsActivityStaysSealed(t *testing.T) {
	p, d, ctx := sealedEvents(t)
	d.Rec.Emit(application.WithWorkAgents(ctx, "legal"), application.Action{Type: domain.EvRequestReceived, Entity: "request", EntityID: "req-new",
		Payload: map[string]any{"request_id": "req-new", "text": "secreto-pedido"}, Text: "Nuevo pedido: secreto-pedido"})
	d.Rec.Emit(ctx, application.Action{Type: domain.EvReportCreated, AgentID: "assistant", Entity: "report", EntityID: "rep-x",
		Payload: map[string]any{"report": domain.Report{ID: "rep-x", RequestID: "req-legal", Title: "secreto-informe"}}, Text: "Informe listo: secreto-informe"})
	d.Rec.Emit(ctx, application.Action{Type: domain.EvTaskCompleted, AgentID: "sales", Entity: "task", EntityID: "task-sales",
		Payload: map[string]any{"task": domain.Task{ID: "task-sales", RequestID: "req-sales", AgentID: "sales"}}, Text: "abierto-ventas"})

	beto := p.body("GET", "/activity", p.beto, 200)
	if strings.Contains(beto, "secreto") {
		t.Fatalf("an admin outside the office reads its activity: %s", beto)
	}
	if !strings.Contains(beto, "abierto-ventas") {
		t.Fatalf("the activity of General stays everybody's: %s", beto)
	}
	ana := p.body("GET", "/activity", p.ana, 200)
	for _, want := range []string{"secreto-pedido", "secreto-informe", "abierto-ventas"} {
		if !strings.Contains(ana, want) {
			t.Fatalf("the member must read %q: %s", want, ana)
		}
	}
}

// D3: a reply of a private office's agent in the shared office chat is sealed live (only its members receive it), so
// the stored chat agrees: the General thread and the office conversation leave it out for whoever does not see that
// agent's work; the member reads it.
func TestSharedChatHidesPrivateAgentMessages(t *testing.T) {
	p, _, ctx := sealedEvents(t)
	org, now, st := p.owner.OrgID, time.Now().UTC(), p.e.store
	if _, err := st.GetConversation(ctx, org, domain.ChatOffice); err != nil {
		if err := st.CreateConversation(ctx, org, domain.Conversation{ID: domain.ChatOffice, Title: "Oficina", Participants: []string{"user"}, LastMessageAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	for i, m := range []domain.Message{
		{ID: "m-q", From: "user", To: "legal", Kind: "chat", Text: "pregunta-a-legal", TurnID: "turn-l"},
		{ID: "m-l", From: "legal", To: "user", Kind: "chat", Text: "respuesta-secreta-legal", TurnID: "turn-l"},
		{ID: "m-s", From: "sales", To: "user", Kind: "chat", Text: "respuesta-ventas", TurnID: "turn-s"},
	} {
		m.ConversationID, m.TS = domain.ChatOffice, now.Add(time.Duration(i)*time.Second)
		if err := st.AddMessage(ctx, org, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/chats/threads/general/messages", "/conversations/office/messages"} {
		beto := p.body("GET", path, p.beto, 200)
		if strings.Contains(beto, "secreta-legal") || strings.Contains(beto, "pregunta-a-legal") {
			t.Errorf("beto: GET %s shows the private agent's chat: %s", path, beto)
		}
		if !strings.Contains(beto, "respuesta-ventas") {
			t.Errorf("beto: GET %s lost the rest of the chat: %s", path, beto)
		}
		ana := p.body("GET", path, p.ana, 200)
		if !strings.Contains(ana, "secreta-legal") || !strings.Contains(ana, "pregunta-a-legal") {
			t.Errorf("ana (member): GET %s lacks the private agent's chat: %s", path, ana)
		}
	}
}
