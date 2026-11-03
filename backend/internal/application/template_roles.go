package application

import (
	"context"
	"slices"

	"aiworkforce/backend/internal/domain"
)

// Workflow templates name the role of each step (the 7 classic roles). Since E1 a new organization only has the agents
// of its people's work, so a step is given to an agent of its role whatever its id (an industry agent "law_lawyer" does
// legal steps), and, when the office has no agent of that role, to the assistant: the step still runs, the
// reassignment is audited, and the gallery tells the person which role to hire (missing_roles).

// RoleIndex maps roles to the agents of an office.
type RoleIndex struct {
	byID      map[string]bool
	byRole    map[string]string
	assistant string
}

// RoleAgents indexes the agents of an office by id and role.
func RoleAgents(agents []domain.Agent) RoleIndex {
	ix := RoleIndex{byID: map[string]bool{}, byRole: map[string]string{}, assistant: assistantID}
	for _, a := range agents {
		ix.byID[a.ID] = true
		if _, ok := ix.byRole[a.Role]; !ok || a.ID == a.Role { // prefer the agent whose id is the role (the seed one)
			ix.byRole[a.Role] = a.ID
		}
	}
	if id, ok := ix.byRole["assistant"]; ok && !ix.byID[assistantID] {
		ix.assistant = id
	}
	return ix
}

// Has reports whether the office has an agent of the role.
func (ix RoleIndex) Has(role string) bool { return ix.byID[role] || ix.byRole[role] != "" }

// AgentFor is the agent that takes a step of the role: the agent with that id, else one with that role, else the assistant.
func (ix RoleIndex) AgentFor(role string) string {
	if ix.byID[role] {
		return role
	}
	if id := ix.byRole[role]; id != "" {
		return id
	}
	return ix.assistant
}

// Missing lists, once each and in order, the roles the office has no agent for.
func (ix RoleIndex) Missing(roles []string) []string {
	out := []string{}
	for _, r := range roles {
		if !ix.Has(r) && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// MissingRoles is RoleAgents(...).Missing for the organization of ctx.
func (s *OrgConfig) MissingRoles(ctx context.Context, roles []string) ([]string, error) {
	agents, err := s.Core.ListAgents(ctx, s.org(ctx))
	if err != nil {
		return nil, err
	}
	return RoleAgents(agents).Missing(roles), nil
}
