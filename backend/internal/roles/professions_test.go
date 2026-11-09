package roles

import (
	"slices"
	"testing"
)

func TestProfessionsAreTheNineOfTheDesign(t *testing.T) {
	want := []string{"legal", "sales", "accounting", "marketing", "operations", "hr", "direction", "education", "other"}
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

func TestProfessionFolders(t *testing.T) {
	cases := []struct {
		key, loc string
		want     []string
	}{
		{"accounting", "es", []string{"Clientes", "Proveedores", "Bancos y conciliaciones", "Impuestos", "Cierres mensuales", "Políticas"}},
		{"legal", "es", []string{"Casos", "Contratos", "Plantillas de escritos", "Plazos", "Políticas"}},
		{"hr", "es", []string{"Colaboradores", "Reclutamiento", "Planillas", "Políticas", "Capacitación"}},
		{"education", "es", []string{"Cursos", "Exámenes", "Rúbricas", "Calificaciones"}},
		{"sales", "es", []string{"Documentos", "Políticas"}},
		{"accounting", "en", []string{"Clients", "Suppliers", "Banks and reconciliations", "Taxes", "Monthly closings", "Policies"}},
		{"legal", "en", []string{"Cases", "Contracts", "Filing templates", "Deadlines", "Policies"}},
		{"hr", "en", []string{"People", "Recruiting", "Payroll", "Policies", "Training"}},
		{"education", "en", []string{"Courses", "Exams", "Rubrics", "Grades"}},
		{"other", "en", []string{"Documents", "Policies"}},
		{"accounting", "pt", []string{"Clientes", "Fornecedores", "Bancos e conciliações", "Impostos", "Fechamentos mensais", "Políticas"}},
		{"legal", "pt", []string{"Casos", "Contratos", "Modelos de petições", "Prazos", "Políticas"}},
		{"hr", "pt", []string{"Colaboradores", "Recrutamento", "Folha de pagamento", "Políticas", "Treinamento"}},
		{"education", "pt", []string{"Cursos", "Provas", "Rubricas", "Notas"}},
		{"marketing", "pt", []string{"Documentos", "Políticas"}},
		{"legal", "xx", []string{"Casos", "Contratos", "Plantillas de escritos", "Plazos", "Políticas"}},
		{"legal", "en-US", []string{"Cases", "Contracts", "Filing templates", "Deadlines", "Policies"}},
	}
	for _, c := range cases {
		p, ok := ProfessionOf(c.key)
		if !ok {
			t.Fatalf("no profession %s", c.key)
		}
		if got := p.Folders(c.loc); !slices.Equal(got, c.want) {
			t.Fatalf("%s/%s = %v, want %v", c.key, c.loc, got, c.want)
		}
	}
}

func TestEducationProfession(t *testing.T) {
	p, ok := ProfessionOf("education")
	if !ok || p.Roles != nil {
		t.Fatalf("education = %+v, %v", p, ok)
	}
	if p.TeamName("es") != "Educación" || p.TeamName("en") != "Education" || p.TeamName("pt") != "Educação" {
		t.Fatal("education team names wrong")
	}
}
