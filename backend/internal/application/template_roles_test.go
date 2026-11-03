package application_test

import (
	"slices"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// E1: an organization created by the team sign-up has only the agents of its people's work, while the workflow
// templates name the 7 classic roles. A step goes to an agent of its role (whatever its id), or to the assistant.
func TestRoleAgentsResolvesEachRoleToAnAgentOfTheOffice(t *testing.T) {
	agents := []domain.Agent{
		{ID: "assistant", Role: "assistant"},
		{ID: "law_lawyer", Role: "legal"}, // an industry template agent: another id, same role
		{ID: "sales", Role: "sales"},
	}
	m := application.RoleAgents(agents)
	cases := map[string]string{"sales": "sales", "legal": "law_lawyer", "accounting": "assistant", "assistant": "assistant"}
	for role, want := range cases {
		if got := m.AgentFor(role); got != want {
			t.Fatalf("%s -> %s, want %s", role, got, want)
		}
	}
	if got := m.Missing([]string{"sales", "accounting", "hr", "accounting", "legal"}); !slices.Equal(got, []string{"accounting", "hr"}) {
		t.Fatalf("missing = %v", got)
	}
	// The classic office keeps every step where it was.
	if got := application.RoleAgents([]domain.Agent{{ID: "accounting", Role: "accounting"}, {ID: "assistant", Role: "assistant"}}).AgentFor("accounting"); got != "accounting" {
		t.Fatalf("classic = %s", got)
	}
}
