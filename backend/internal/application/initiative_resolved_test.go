package application_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Accepting or dismissing a suggestion is announced (initiative.resolved, additive) so "Te necesitan" of the other
// people refreshes (needs-you.md). The event names the agent, so a private department's stays with its members.
func TestInitiativeResolvedIsAnnounced(t *testing.T) {
	svc, mem, _, _ := newInitiative(t)
	pub := &capturePub{}
	svc.Rec = &application.Recorder{OrgID: domain.DemoOrgID, Store: mem, Pub: pub, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx := application.WithActor(context.Background(), "ana")
	a := raise(t, svc, "sales", "a").Initiative
	b := raise(t, svc, "legal", "b").Initiative
	if _, err := svc.Accept(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Dismiss(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	got := map[string]domain.Event{}
	for _, e := range pub.evs {
		if e.Type == domain.EvInitiativeResolved {
			got[e.Payload.(map[string]any)["initiative_id"].(string)] = e
		}
	}
	if e := got[a.ID]; e.AgentID != "sales" || e.Payload.(map[string]any)["status"] != string(application.InitiativeAccepted) {
		t.Fatalf("accepted event = %+v", e)
	}
	if e := got[b.ID]; e.AgentID != "legal" || e.Payload.(map[string]any)["status"] != string(application.InitiativeDismissed) {
		t.Fatalf("dismissed event = %+v", e)
	}
}
