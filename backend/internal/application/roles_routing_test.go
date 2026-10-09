package application

import (
	"testing"

	"aiworkforce/backend/internal/roles"
)

func officeWith(extra ...string) []RouteAgent {
	o := &Orchestrator{}
	agents := roles.SeedAgents()
	for _, id := range extra {
		t, _ := roles.Get(id)
		agents = append(agents, roles.Instantiate(t, "es", id, id))
	}
	return o.routeAgents(agents, "es")
}

func TestRouteAgentsCarryTheTemplateProfile(t *testing.T) {
	for _, a := range officeWith("project_manager") {
		if a.Topic == "" || len(a.Keywords) == 0 || a.Area == "" {
			t.Fatalf("%s has no profile: %+v", a.ID, a)
		}
	}
}

func TestLocalRouteSendsQuestionsToHiredProfessions(t *testing.T) {
	cases := []struct{ text, role string }{
		{"¿cómo va el cronograma del proyecto?", "project_manager"},
		{"what is the critical path of the project?", "project_manager"},
		{"¿qué rúbrica usamos para el examen del curso?", "education"},
		{"¿la consulta sql de cohortes está lista?", "data_analyst"},
		{"is the pull request for the bug ready?", "software_engineer"},
		{"¿ya está la conciliación bancaria del extracto de septiembre?", "finance_treasury"},
	}
	office := officeWith("project_manager", "education", "data_analyst", "software_engineer", "finance_treasury")
	for _, c := range cases {
		r := localRoute(RouteRequest{Text: c.text, Conversation: "office", Agents: office, Locale: "es"})
		if len(r.Responders) == 0 || r.Responders[0].AgentID != c.role {
			t.Errorf("%q -> %+v, want %s", c.text, r.Responders, c.role)
		}
	}
	// Without that profession in the office, nobody absent is chosen: the seed office answers as before.
	seed := officeWith()
	r := localRoute(RouteRequest{Text: "¿cómo va el cronograma del proyecto?", Conversation: "office", Agents: seed, Locale: "es"})
	if r.Responders[0].AgentID != "assistant" {
		t.Fatalf("seed office: %+v", r.Responders)
	}
	// The seed roles keep their tuned rules.
	r = localRoute(RouteRequest{Text: "¿cuál es el margen del mes?", Conversation: "office", Agents: office, Locale: "es"})
	if r.Responders[0].AgentID != "accounting" {
		t.Fatalf("margin question: %+v", r.Responders)
	}
}
