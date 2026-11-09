package roles

import (
	"slices"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// Professions (docs/architecture/equipos-oficinas.md §4 and §6): what a person answers to "what do you do?" at sign-up or
// in an invitation. A profession only SUGGESTS agents: the roles below are created for the person's organization when
// they join (always with the assistant), and nothing else is granted. A new organization no longer gets the 7 fixed
// agents; it starts with the agents of its people's work.

// Profession is one answer to "what do you do?".
type Profession struct {
	Key string
	// Roles are the role templates suggested for this work, the assistant excluded (SuggestedRoles adds it).
	Roles []string
	// teams is the default team (department) name per locale: es, en, pt.
	teams map[string]string
}

// AssistantRole is the role every organization keeps: it receives requests and coordinates the others.
const AssistantRole = "assistant"

// ProfessionOther is the answer for any other work: only the assistant.
const ProfessionOther = "other"

var professions = []Profession{
	{Key: "legal", Roles: []string{"legal"}, teams: map[string]string{"es": "Legal", "en": "Legal", "pt": "Jurídico"}},
	{Key: "sales", Roles: []string{"sales", "analyst"}, teams: map[string]string{"es": "Ventas", "en": "Sales", "pt": "Vendas"}},
	{Key: "accounting", Roles: []string{"accounting", "analyst"}, teams: map[string]string{"es": "Contabilidad", "en": "Accounting", "pt": "Contabilidade"}},
	// No marketing template yet: the sales role writes the commercial communication, the analyst measures it.
	{Key: "marketing", Roles: []string{"sales", "analyst"}, teams: map[string]string{"es": "Marketing", "en": "Marketing", "pt": "Marketing"}},
	{Key: "operations", Roles: []string{"operations", "project_manager"}, teams: map[string]string{"es": "Operaciones", "en": "Operations", "pt": "Operações"}},
	{Key: "hr", Roles: []string{"hr"}, teams: map[string]string{"es": "Recursos Humanos", "en": "People", "pt": "Recursos Humanos"}},
	{Key: "direction", Roles: []string{"analyst", "project_manager"}, teams: map[string]string{"es": "Dirección", "en": "Management", "pt": "Direção"}},
	{Key: "education", Roles: nil, teams: map[string]string{"es": "Educación", "en": "Education", "pt": "Educação"}},
	{Key: ProfessionOther, Roles: nil, teams: map[string]string{"es": "General", "en": "General", "pt": "Geral"}},
}

// Professions returns the catalog in the order the forms show it.
func Professions() []Profession { return slices.Clone(professions) }

// ProfessionOf returns a profession by key.
func ProfessionOf(key string) (Profession, bool) {
	for _, p := range professions {
		if p.Key == key {
			return p, true
		}
	}
	return Profession{}, false
}

// ValidProfession reports whether key is in the catalog.
func ValidProfession(key string) bool { _, ok := ProfessionOf(key); return ok }

// TeamName is the default name of a team of this profession (es, en, pt; Spanish otherwise).
func (p Profession) TeamName(loc string) string {
	l := strings.ToLower(loc)
	switch {
	case strings.HasPrefix(l, "en"):
		return p.teams["en"]
	case strings.HasPrefix(l, "pt"):
		return p.teams["pt"]
	}
	return p.teams["es"]
}

// officeFolders is the folder template of an office per profession and locale (es, en, pt). The professions that are not
// listed use defaultFolders.
var officeFolders = map[string]map[string][]string{
	"accounting": {
		"es": {"Clientes", "Proveedores", "Bancos y conciliaciones", "Impuestos", "Cierres mensuales", "Políticas"},
		"en": {"Clients", "Suppliers", "Banks and reconciliations", "Taxes", "Monthly closings", "Policies"},
		"pt": {"Clientes", "Fornecedores", "Bancos e conciliações", "Impostos", "Fechamentos mensais", "Políticas"},
	},
	"legal": {
		"es": {"Casos", "Contratos", "Plantillas de escritos", "Plazos", "Políticas"},
		"en": {"Cases", "Contracts", "Filing templates", "Deadlines", "Policies"},
		"pt": {"Casos", "Contratos", "Modelos de petições", "Prazos", "Políticas"},
	},
	"hr": {
		"es": {"Colaboradores", "Reclutamiento", "Planillas", "Políticas", "Capacitación"},
		"en": {"People", "Recruiting", "Payroll", "Policies", "Training"},
		"pt": {"Colaboradores", "Recrutamento", "Folha de pagamento", "Políticas", "Treinamento"},
	},
	"education": {
		"es": {"Cursos", "Exámenes", "Rúbricas", "Calificaciones"},
		"en": {"Courses", "Exams", "Rubrics", "Grades"},
		"pt": {"Cursos", "Provas", "Rubricas", "Notas"},
	},
}

var defaultFolders = map[string][]string{
	"es": {"Documentos", "Políticas"},
	"en": {"Documents", "Policies"},
	"pt": {"Documentos", "Políticas"},
}

// Folders is the template of subfolders the office folder of this profession starts with (es, en, pt; Spanish
// otherwise). The returned slice is a copy.
func (p Profession) Folders(loc string) []string {
	l := strings.ToLower(loc)
	key := "es"
	switch {
	case strings.HasPrefix(l, "en"):
		key = "en"
	case strings.HasPrefix(l, "pt"):
		key = "pt"
	}
	byLoc, ok := officeFolders[p.Key]
	if !ok {
		byLoc = defaultFolders
	}
	return slices.Clone(byLoc[key])
}

// SuggestedRoles returns the roles a person of this profession starts with, the assistant last. An unknown or empty
// profession gets only the assistant.
func SuggestedRoles(profession string) []string {
	p, _ := ProfessionOf(profession)
	return append(slices.Clone(p.Roles), AssistantRole)
}

// SeedAgentsFor returns the seed agents (fixed id and name, as every organization used to get them) of the given roles,
// with their texts in the locale. Roles without a seed identity are skipped: they are hired like any template.
func SeedAgentsFor(roleIDs []string, loc string) []domain.Agent {
	ensure()
	var out []domain.Agent
	for _, id := range roleIDs {
		t, ok := byID[id]
		if !ok || t.Seed == nil {
			continue
		}
		a := Instantiate(t, loc, t.Seed.AgentID, t.Seed.Name)
		a.Autonomy = t.Autonomy.Default
		out = append(out, a)
	}
	return out
}
