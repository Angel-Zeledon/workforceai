package application_test

import (
	"context"
	"slices"
	"testing"

	"aiworkforce/backend/internal/application"
)

// A debate held for a request asked from an office has only agents that work for that office, also when the topic or
// the request names others or the participants are picked for the topic; with no office everybody may take part.
func TestIsolation_DebateOnlyHasAgentsOfTheRequestsOffice(t *testing.T) {
	h := newHarness(t, &fakeRuntime{}, nil)
	h.orch.SetOfficeScope(scopeFake{}) // sales: Legal + Ventas; accounting, legal, hr...: Ventas
	for _, c := range []struct {
		office string
		names  []string
		want   []string
	}{
		{"o-ven", []string{"sales", "accounting", "legal"}, []string{"sales", "accounting", "legal"}},
		{"o-legal", []string{"sales", "accounting", "legal"}, nil}, // only sales works for Legal: fewer than two, no debate
		{"", []string{"sales", "accounting", "legal"}, []string{"sales", "accounting", "legal"}},
	} {
		rt := chatty("sales", "accounting", "legal")
		svc := newDebates(h, rt)
		svc.OfficeAgents = h.orch.OfficeAgents
		ctx := application.WithRequestOffice(application.WithActor(context.Background(), "ana"), c.office)
		d, err := svc.Run(ctx, application.DebateInput{Topic: "¿Lanzamos el plan 2027?", AgentIDs: c.names, Locale: "es"})
		if c.want == nil {
			if err == nil && len(d.Participants) >= 2 {
				t.Errorf("office %q: debated with %v", c.office, d.Participants)
			}
			continue
		}
		if err != nil {
			t.Fatalf("office %q: %v", c.office, err)
		}
		if !slices.Equal(d.Participants, c.want) {
			t.Errorf("office %q: participants %v, want %v", c.office, d.Participants, c.want)
		}
	}
	// picked for the topic (no names): never an agent of another office
	svc := newDebates(h, chatty("sales", "accounting", "legal"))
	svc.OfficeAgents = h.orch.OfficeAgents
	ctx := application.WithRequestOffice(application.WithActor(context.Background(), "ana"), "o-ven")
	if d, err := svc.Run(ctx, application.DebateInput{Topic: "contrato de ventas y márgenes", Locale: "es"}); err == nil {
		for _, p := range d.Participants {
			if ok, _ := (scopeFake{}).WorksFor(ctx, "", p, "o-ven"); !ok && p != "assistant" {
				t.Errorf("picked %s, who does not work for o-ven", p)
			}
		}
	}
}
