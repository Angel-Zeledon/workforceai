package application

import (
	"strings"
	"testing"

	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/roles"
)

func collectionsTeam(t *testing.T) catalog.TeamView {
	t.Helper()
	tm, ok := catalog.Default().Team("collections", "es")
	if !ok {
		t.Fatal("collections team missing")
	}
	return tm
}

func seedOf(t *testing.T, role string) domain.Agent {
	t.Helper()
	s := roles.SeedAgentsFor([]string{role}, "es")
	if len(s) == 0 {
		t.Fatalf("no seed for %s", role)
	}
	return s[0]
}

func TestPreviewTeamWithoutAgentsEveryoneIsNew(t *testing.T) {
	p := PreviewTeam(collectionsTeam(t), nil, nil, "general", "es")
	if p.NewCount != 3 || p.ExistingCount != 0 {
		t.Fatalf("counts = %d/%d", p.NewCount, p.ExistingCount)
	}
	for _, a := range p.Agents {
		if a.Status != TeamNew || a.Name == "" || a.Title == "" || a.Does == "" || a.Existing != nil {
			t.Fatalf("agent = %+v", a)
		}
	}
	// the seed identity of the role is the suggestion when it is free (what HireForRole would use)
	if p.Agents[0].Name != seedOf(t, "accounting").Name {
		t.Fatalf("suggested = %q", p.Agents[0].Name)
	}
	if len(p.Skills) == 0 {
		t.Fatal("the default skills of the roles must be listed")
	}
	seen := map[string]bool{}
	for _, s := range p.Skills {
		if seen[s] {
			t.Fatalf("duplicate skill %q", s)
		}
		seen[s] = true
	}
}

func TestPreviewTeamRecognizesWhoIsAlreadyInTheDestination(t *testing.T) {
	acct, sales := seedOf(t, "accounting"), seedOf(t, "sales")
	in := map[string]string{acct.ID: "ventas", sales.ID: "contabilidad"}
	office := func(id string) string { return in[id] }
	p := PreviewTeam(collectionsTeam(t), []domain.Agent{acct, sales}, office, "ventas", "es")
	if p.ExistingCount != 1 || p.NewCount != 2 {
		t.Fatalf("counts = %d/%d: %+v", p.NewCount, p.ExistingCount, p.Agents)
	}
	a := p.Agents[0] // accounting is in ventas
	if a.Role != "accounting" || a.Status != TeamExisting || a.Existing == nil || a.Existing.ID != acct.ID || a.Existing.Name != acct.Name {
		t.Fatalf("accounting = %+v", a)
	}
	// sales exists, but in another department: a new one comes here and its name does not repeat
	s := p.Agents[1]
	if s.Role != "sales" || s.Status != TeamNew || s.Name == "" || strings.EqualFold(s.Name, sales.Name) {
		t.Fatalf("sales = %+v", s)
	}
}

func TestPreviewTeamAlreadyHavingEveryoneInstallsNothing(t *testing.T) {
	var have []domain.Agent
	for _, r := range []string{"accounting", "sales", "assistant"} {
		have = append(have, seedOf(t, r))
	}
	p := PreviewTeam(collectionsTeam(t), have, nil, "general", "es")
	if p.NewCount != 0 || p.ExistingCount != 3 {
		t.Fatalf("counts = %d/%d", p.NewCount, p.ExistingCount)
	}
	if got := p.NewRoles(); len(got) != 0 {
		t.Fatalf("roles to hire = %v", got)
	}
}

func TestPreviewTeamNewNamesAreDistinct(t *testing.T) {
	tm, _ := catalog.Default().Team("month_close", "es")
	p := PreviewTeam(tm, nil, nil, "general", "es")
	names := map[string]bool{}
	for _, a := range p.Agents {
		k := strings.ToLower(a.Name)
		if names[k] {
			t.Fatalf("name %q suggested twice", a.Name)
		}
		names[k] = true
	}
	if got := p.NewRoles(); len(got) != 4 || got[0] != "accounting" {
		t.Fatalf("roles to hire = %v", got)
	}
}
