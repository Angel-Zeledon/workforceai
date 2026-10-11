package main

import (
	"testing"

	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/roles"
)

// Every connection a role template recommends exists, with those capabilities,
// and the assistant and sales roles recommend Google and LinkedIn.
func TestRecommendedConnectionsMatchTheManifests(t *testing.T) {
	ms, err := connections.LoadManifests()
	if err != nil {
		t.Fatal(err)
	}
	for id := range roles.KnownProviders {
		if _, ok := ms[id]; !ok {
			t.Fatalf("roles.KnownProviders lists %q but there is no manifest", id)
		}
	}
	for _, tp := range roles.All() {
		for _, rc := range tp.RecommendedConnections {
			m, ok := ms[rc.Provider]
			if !ok {
				t.Fatalf("%s recommends unknown provider %q", tp.ID, rc.Provider)
			}
			for _, c := range rc.Capabilities {
				if _, ok := m.Capabilities[c]; !ok {
					t.Fatalf("%s recommends %s.%s, which the manifest does not offer", tp.ID, rc.Provider, c)
				}
			}
		}
	}
	for _, role := range []string{"assistant", "sales"} {
		tp, _ := roles.Get(role)
		got := map[string]bool{}
		for _, rc := range tp.RecommendedConnections {
			got[rc.Provider] = true
		}
		if !got["google_gmail"] || !got["google_calendar"] || !got["linkedin"] {
			t.Fatalf("%s must recommend Gmail, Calendar and LinkedIn: %+v", role, tp.RecommendedConnections)
		}
	}
}
