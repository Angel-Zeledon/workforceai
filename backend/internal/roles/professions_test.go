package roles

import (
	"slices"
	"testing"
)

func TestProfessionsAreTheEightOfTheDesign(t *testing.T) {
	want := []string{"legal", "sales", "accounting", "marketing", "operations", "hr", "direction", "other"}
	got := []string{}
	for _, p := range Professions() {
		got = append(got, p.Key)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("professions = %v, want %v", got, want)
	}
}

func TestEveryProfessionSuggestsKnownRolesAndTheAssistant(t *testing.T) {
	for _, p := range Professions() {
		rs := SuggestedRoles(p.Key)
		if len(rs) == 0 || rs[len(rs)-1] != "assistant" {
			t.Fatalf("%s: suggested roles %v must end with the assistant", p.Key, rs)
		}
		for _, r := range rs {
			if _, ok := Get(r); !ok {
				t.Fatalf("%s: role %q is not a template", p.Key, r)
			}
		}
		for _, loc := range []string{"es", "en", "pt"} {
			if p.TeamName(loc) == "" {
				t.Fatalf("%s: no team name in %s", p.Key, loc)
			}
		}
	}
}

func TestSuggestedRolesOfTheDesignExamples(t *testing.T) {
	// equipos-oficinas.md §4: a lawyer starts with Legal and the assistant; a salesperson with Sales and Analysis.
	if got := SuggestedRoles("legal"); !slices.Equal(got, []string{"legal", "assistant"}) {
		t.Fatalf("legal = %v", got)
	}
	if got := SuggestedRoles("sales"); !slices.Equal(got, []string{"sales", "analyst", "assistant"}) {
		t.Fatalf("sales = %v", got)
	}
	// Unknown or empty: only the assistant.
	for _, k := range []string{"", "astronaut"} {
		if got := SuggestedRoles(k); !slices.Equal(got, []string{"assistant"}) {
			t.Fatalf("%q = %v", k, got)
		}
	}
}

func TestValidProfession(t *testing.T) {
	if !ValidProfession("hr") || !ValidProfession("other") || ValidProfession("astronaut") || ValidProfession("") {
		t.Fatal("ValidProfession disagrees with the catalog")
	}
}

func TestSeedAgentsForKeepsTheSeedIdentityAndTheLocale(t *testing.T) {
	es := SeedAgentsFor([]string{"legal", "assistant", "project_manager"}, "es")
	if len(es) != 2 {
		t.Fatalf("only seed roles have a fixed identity, got %d agents", len(es))
	}
	if es[0].ID != "legal" || es[0].Name != "Elena Castro" || es[0].Title != "Abogada" || es[1].ID != "assistant" {
		t.Fatalf("es seed = %+v", es)
	}
	// The same agent the demo gets (texts, tools, autonomy), just fewer of them.
	for _, a := range SeedAgents() {
		if a.ID == "legal" && (a.Autonomy != es[0].Autonomy || !slices.Equal(a.Tools, es[0].Tools) || a.Persona != es[0].Persona) {
			t.Fatalf("legal differs from the seed: %+v vs %+v", es[0], a)
		}
	}
	en := SeedAgentsFor([]string{"legal"}, "en")
	if en[0].Name != "Elena Castro" || en[0].Title == "Abogada" {
		t.Fatalf("en seed must keep the name and translate the texts: %+v", en[0])
	}
}
