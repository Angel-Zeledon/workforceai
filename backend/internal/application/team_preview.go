package application

import (
	"fmt"
	"strings"

	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/roles"
)

// Ready-to-hire teams (F2, docs/architecture/team-catalog.md). The preview is computed for ONE organization: who of
// the team is new and who is already there with that role in the destination department, so hiring never duplicates.

// Status of a person of a team in the destination department.
const (
	TeamNew      = "new"
	TeamExisting = "existing"
)

// TeamExistingRef is the agent already in the destination department that fills a role of the team.
type TeamExistingRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Title string `json:"title"`
}

// TeamPreviewAgent is one person of the team as this organization would see it.
type TeamPreviewAgent struct {
	Role   string `json:"role"`
	Title  string `json:"title"`
	Does   string `json:"does"`
	Status string `json:"status"`
	// Name is the suggested name of a new agent ("" when the role is already filled).
	Name string `json:"name"`
	// Autonomy is where a new agent starts (the role's default; the ceiling stays the role's too).
	Autonomy string           `json:"autonomy"`
	Existing *TeamExistingRef `json:"existing,omitempty"`
	// Skills are the role's default skills (library names; they come with the role, no assignment row).
	Skills []string `json:"skills"`
}

// TeamPreview is a team resolved against one organization and one destination department.
type TeamPreview struct {
	Agents        []TeamPreviewAgent `json:"agents"`
	NewCount      int                `json:"new_count"`
	ExistingCount int                `json:"existing_count"`
	// Skills is the union of the default skills of the team's roles.
	Skills []string `json:"skills"`
}

// NewRoles returns, in team order, the roles that must be hired.
func (p TeamPreview) NewRoles() []string {
	out := []string{}
	for _, a := range p.Agents {
		if a.Status == TeamNew {
			out = append(out, a.Role)
		}
	}
	return out
}

// PreviewTeam resolves a team for an organization. officeOf says which department (office id) each agent belongs to
// (nil = every agent is in the destination); dest is the destination department. Existing agents are only the ones
// of the team's roles that already live in dest: the same role elsewhere does not count, the new one is hired here.
func PreviewTeam(t catalog.TeamView, agents []domain.Agent, officeOf func(agentID string) string, dest, loc string) TeamPreview {
	loc = roles.Locale(loc)
	ids, names := map[string]bool{}, map[string]bool{}
	for _, a := range agents {
		ids[a.ID], names[strings.ToLower(a.Name)] = true, true
	}
	here := func(a domain.Agent) bool { return officeOf == nil || officeOf(a.ID) == dest }
	p := TeamPreview{Agents: []TeamPreviewAgent{}, Skills: []string{}}
	seenSkill, taken := map[string]bool{}, map[string]bool{}
	for _, ta := range t.Agents {
		tpl, ok := roles.Get(ta.Role)
		if !ok {
			continue
		}
		pa := TeamPreviewAgent{Role: ta.Role, Title: tpl.Text(loc).Title, Does: ta.Does, Autonomy: tpl.Autonomy.Default,
			Skills: append([]string{}, tpl.DefaultSkills...)}
		for _, sk := range tpl.DefaultSkills {
			if !seenSkill[sk] {
				seenSkill[sk] = true
				p.Skills = append(p.Skills, sk)
			}
		}
		for _, a := range agents {
			// an agent fills one place: a team (an office template) with the same role twice needs two of them
			if a.Role == ta.Role && here(a) && !taken[a.ID] {
				pa.Status, pa.Existing = TeamExisting, &TeamExistingRef{ID: a.ID, Name: a.Name, Title: a.Title}
				taken[a.ID] = true
				break
			}
		}
		if pa.Status == "" {
			pa.Status = TeamNew
			pa.Name = suggestName(tpl, loc, ids, names)
			ids[freeID(tpl.ID, ids)], names[strings.ToLower(pa.Name)] = true, true
		}
		if pa.Status == TeamNew {
			p.NewCount++
		} else {
			p.ExistingCount++
		}
		p.Agents = append(p.Agents, pa)
	}
	return p
}

// freeID mirrors HireFromTemplate: the role id, or role_2, role_3... when it is taken.
func freeID(role string, ids map[string]bool) string {
	id := role
	for n := 2; ids[id]; n++ {
		id = fmt.Sprintf("%s_%d", role, n)
	}
	return id
}

// suggestName mirrors HireForRole: the seed identity of the role while its id and name are free, otherwise the first
// unused name of the role's pool.
func suggestName(t roles.Template, loc string, ids, names map[string]bool) string {
	if seeds := roles.SeedAgentsFor([]string{t.ID}, loc); len(seeds) > 0 {
		if s := seeds[0]; !ids[s.ID] && !names[strings.ToLower(s.Name)] {
			return s.Name
		}
	}
	return pickName(t, loc, names)
}
