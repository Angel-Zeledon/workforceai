package application

import (
	"context"
	"fmt"
	"strings"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/roles"
)

// HireInput adds an agent from a role template. Name and Locale are optional:
// the name comes from the template's name pool, the locale from the org settings.
type HireInput struct {
	TemplateID string `json:"template_id"`
	Name       string `json:"name"`
	Locale     string `json:"locale"`
}

// HireFromTemplate creates an agent from a builtin role template. It grants no
// connection and starts at the template's default autonomy (approve_each for
// every new profession); raising it stays a separate, audited decision.
func (o *Orchestrator) HireFromTemplate(ctx context.Context, in HireInput) (domain.Agent, error) {
	t, ok := roles.Get(strings.TrimSpace(in.TemplateID))
	if !ok {
		return domain.Agent{}, fmt.Errorf("%w: unknown role template %q", domain.ErrNotFound, in.TemplateID)
	}
	w, ok := o.store.(AgentWriter)
	if !ok {
		return domain.Agent{}, fmt.Errorf("%w: this store cannot add agents", domain.ErrInvalid)
	}
	name := strings.TrimSpace(in.Name)
	if len([]rune(name)) > 60 {
		return domain.Agent{}, fmt.Errorf("%w: name is too long", domain.ErrInvalid)
	}
	loc := in.Locale
	if loc == "" {
		loc = o.loadStyle(ctx).Locale
	}
	loc = roles.Locale(loc)
	existing, err := o.store.ListAgents(ctx, o.org(ctx))
	if err != nil {
		return domain.Agent{}, err
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for _, a := range existing {
		ids[a.ID], names[strings.ToLower(a.Name)] = true, true
	}
	id := t.ID
	for n := 2; ids[id]; n++ {
		id = fmt.Sprintf("%s_%d", t.ID, n)
	}
	if name == "" {
		name = pickName(t, loc, names)
	}
	a := roles.Instantiate(t, loc, id, name)
	if err := w.CreateAgent(ctx, o.org(ctx), a); err != nil {
		return domain.Agent{}, err
	}
	o.rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "user"), Action: "agent.created_from_template", Entity: "agent", EntityID: a.ID,
		Details: map[string]any{"template_id": t.ID, "template_version": t.Version, "risk_tier": t.RiskTier, "autonomy": a.Autonomy, "tools": a.Tools}})
	text := map[string]string{"es": "Se incorporó al equipo: ", "en": "Joined the team: "}[loc] + a.Name + " (" + a.Title + ")"
	o.rec.Emit(ctx, Action{Type: domain.EvAgentCreated, AgentID: a.ID, Entity: "agent", EntityID: a.ID, SkipAudit: true,
		Payload: map[string]any{"agent": a, "template_id": t.ID}, Text: text})
	return a, nil
}

// pickName returns the first name of the pool not already used in the office.
func pickName(t roles.Template, loc string, used map[string]bool) string {
	pool := append(append([]string{}, t.Display.NamePool[loc]...), t.Display.NamePool[map[string]string{"es": "en", "en": "es"}[loc]]...)
	for _, n := range pool {
		if !used[strings.ToLower(n)] {
			return n
		}
	}
	base := t.Text(loc).Title
	if len(pool) > 0 {
		base = pool[0]
	}
	for n := 2; ; n++ {
		if c := fmt.Sprintf("%s %d", base, n); !used[strings.ToLower(c)] {
			return c
		}
	}
}

// profileOf returns the routing profile of each agent's role template, so the
// runtime routes and plans with data instead of a fixed list of roles.
func profileOf(role, loc string) (topic string, keywords, related []string, area string) {
	if p, ok := roles.ProfileOf(role, loc); ok {
		return p.Topic, p.Keywords, p.Related, p.Area
	}
	return "", nil, nil, ""
}

// NewPlanAgent is the planner's view of an agent, with its role profile.
func NewPlanAgent(a domain.Agent, loc string) PlanAgent {
	p := PlanAgent{ID: a.ID, Role: a.Role, Title: a.Title, Responsibilities: a.Responsibilities}
	p.Topic, p.Keywords, _, p.Area = profileOf(a.Role, loc)
	return p
}

func chatAgent(a domain.Agent, loc string) ChatAgent {
	return ChatAgent{ID: a.ID, Role: a.Role, Title: a.Title, Name: a.Name, Persona: a.Persona, Area: areaOf(a.Role, loc)}
}

func areaOf(role, loc string) string {
	_, _, _, area := profileOf(role, loc)
	return area
}
