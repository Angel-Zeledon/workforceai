package positions

import "slices"

// Starter positions for any department, created with one click and editable afterwards. The visible name is chosen
// by the screen in the person's language ("Gerente de Contabilidad", "Asistente de gerente", "Especialista"); the
// package is proposed here and, for the subordinates, already cut to the department's manager.

// Template is a starter position and what it proposes.
type Template struct {
	Key       string  `json:"key"`
	ReportsTo string  `json:"reports_to"`
	Proposal  Package `json:"proposal"`
}

// Starter skill names (built-in library, internal/skills/builtin). Only the ones the department may carry and that
// exist are proposed. The assistant's are a subset of the manager's (it reports to the manager).
var (
	managerSkills   = []string{"executive-summary", "decision-log", "okr-planning", "weekly-status-report", "meeting-agenda", "meeting-minutes", "kpi-dashboard-brief", "performance-review-draft"}
	assistantSkills = []string{"meeting-agenda", "meeting-minutes", "weekly-status-report"}
)

// templateKeys in the order the screen shows them.
var templateKeys = []string{TemplateManager, TemplateAssistant, TemplateSpecialist}

func validTemplate(k string) bool { return k == "" || slices.Contains(templateKeys, k) }

// starter is the proposal of a template before it is cut to a superior: the manager reads and writes the whole
// department and reads /Shared; the assistant only reads them; the specialist works in the department like today
// (its own skills come from its trade).
func starter(key string, b Bounds, skillIDs func(names []string) []string) Package {
	office := func(level string) []Folder {
		var out []Folder
		if b.Office != "" {
			out = append(out, Folder{Path: b.Office, Level: level})
		}
		return append(out, Folder{Path: b.Shared, Level: LevelRead})
	}
	switch key {
	case TemplateManager:
		return Package{Skills: skillIDs(managerSkills), Library: office(LevelEdit)}
	case TemplateAssistant:
		return Package{Skills: skillIDs(assistantSkills), Library: office(LevelRead)}
	}
	return Package{Skills: []string{}, Library: office(LevelEdit)}
}
