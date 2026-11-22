package officetemplates

import (
	"strings"

	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/projects"
)

// FromProjectTemplate copies an organization work template field by field (allow-list). roleOf turns the agent id of
// a step into its role; a step whose agent is unknown keeps an empty role (the importing side reports it).
func FromProjectTemplate(t projects.Template, roleOf func(agentID string) string) Workflow {
	w := Workflow{Name: t.Name, Description: t.Description, Category: t.Category, Objectives: []Objective{}}
	for _, p := range t.Params {
		w.Params = append(w.Params, Param{Key: p.Key, Label: first(p.Label, p.Key), Default: p.Default, Type: p.Type,
			ItemName: p.ItemName, Min: p.Min, Max: p.Max})
	}
	for _, in := range t.Inputs {
		w.Inputs = append(w.Inputs, Input{Key: in.Key, Label: first(in.Label, in.Key), Description: in.Description, Kind: in.Kind,
			Accept: append([]string(nil), in.Accept...), Required: in.Required, Example: in.Example,
			Options: append([]string(nil), in.Options...), MinFiles: in.MinFiles, PerParam: in.PerParam})
	}
	for _, o := range t.Objectives {
		ob := Objective{Key: o.Key, Title: first(o.Title, o.TitleKey), Workflows: []Block{}}
		for _, b := range o.Workflows {
			bl := Block{Key: b.Key, Title: first(b.Title, b.TitleKey), Repeat: append([]string(nil), b.Repeat...), Nodes: []Node{}}
			for _, n := range b.Nodes {
				nd := Node{Key: n.Key, Kind: n.Kind, Title: first(n.Title, n.TitleKey), Description: n.Description,
					Complexity: n.Complexity, Acceptance: append([]string(nil), n.Acceptance...), Deps: append([]string(nil), n.Deps...), Secs: n.Secs}
				if n.Agent != "" {
					nd.Role = roleOf(n.Agent)
				}
				if n.Approval != nil {
					nd.Approval = &Approval{Action: n.Approval.Action, Risk: n.Approval.Risk}
				}
				if n.Delegate != nil {
					nd.Delegate = &Delegate{Role: roleOf(n.Delegate.Agent), Title: first(n.Delegate.Title, n.Delegate.TitleKey), Complexity: n.Delegate.Complexity}
				}
				bl.Nodes = append(bl.Nodes, nd)
			}
			ob.Workflows = append(ob.Workflows, bl)
		}
		w.Objectives = append(w.Objectives, ob)
	}
	return w
}

// ToProjectInput is the work template to create in the importing organization. agentOf turns the role of a step into
// the id of the agent that will do it there ("" when there is none: the template then fails its validation).
func (w Workflow) ToProjectInput(agentOf func(role string) string) projects.TemplateInput {
	in := projects.TemplateInput{Name: w.Name, Description: w.Description, Category: w.Category,
		Params: []projects.TemplateParam{}, Objectives: []projects.TemplateObjective{}}
	shared := true
	in.Shared = &shared
	for _, p := range w.Params {
		in.Params = append(in.Params, projects.TemplateParam{Key: p.Key, Label: p.Label, Default: p.Default, Type: p.Type,
			ItemName: p.ItemName, Min: p.Min, Max: p.Max})
	}
	inputs := []projects.InputSpec{}
	for _, x := range w.Inputs {
		inputs = append(inputs, projects.InputSpec{Key: x.Key, Label: x.Label, Description: x.Description, Kind: x.Kind,
			Accept: append([]string(nil), x.Accept...), Required: x.Required, Example: x.Example,
			Options: append([]string(nil), x.Options...), MinFiles: x.MinFiles, PerParam: x.PerParam})
	}
	in.Inputs = &inputs
	for _, o := range w.Objectives {
		ob := projects.TemplateObjective{Key: o.Key, Title: o.Title, Workflows: []projects.TemplateWorkflow{}}
		for _, b := range o.Workflows {
			bl := projects.TemplateWorkflow{Key: b.Key, Title: b.Title, Repeat: append([]string(nil), b.Repeat...), Nodes: []projects.TemplateNode{}}
			for _, n := range b.Nodes {
				nd := projects.TemplateNode{Key: n.Key, Kind: n.Kind, Title: n.Title, Description: n.Description, Complexity: n.Complexity,
					Acceptance: append([]string(nil), n.Acceptance...), Deps: append([]string(nil), n.Deps...), Secs: n.Secs}
				if n.Role != "" {
					nd.Agent = agentOf(n.Role)
				}
				if n.Approval != nil {
					nd.Approval = &projects.TemplateApproval{Action: n.Approval.Action, Risk: n.Approval.Risk}
				}
				if n.Delegate != nil {
					nd.Delegate = &projects.TemplateDelegate{Agent: agentOf(n.Delegate.Role), Title: n.Delegate.Title, Complexity: n.Delegate.Complexity}
				}
				bl.Nodes = append(bl.Nodes, nd)
			}
			ob.Workflows = append(ob.Workflows, bl)
		}
		in.Objectives = append(in.Objectives, ob)
	}
	return in
}

// Roles are the roles the steps of a work template use, in order of appearance.
func (w Workflow) Roles() []string {
	seen, out := map[string]bool{}, []string{}
	add := func(r string) {
		if r != "" && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, o := range w.Objectives {
		for _, b := range o.Workflows {
			for _, n := range b.Nodes {
				add(n.Role)
				if n.Delegate != nil {
					add(n.Delegate.Role)
				}
			}
		}
	}
	return out
}

func first(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// TeamFor is one department of the template as a catalog team (F2), so the preview and the install of a ready-to-hire
// team serve it unchanged: the people of that department with their role and what they do.
func (t Template) TeamFor(deptKey string) catalog.TeamView {
	v := catalog.TeamView{Key: deptKey, Version: t.FormatVersion, Name: t.Name, For: t.For, Solves: t.Solves,
		Packs: []string{}, Agents: []catalog.TeamAgentView{}, Week: []catalog.TeamWeekView{}}
	for _, d := range t.Departments {
		if d.Key == deptKey {
			v.Department = d.Name
		}
	}
	for _, a := range t.Agents {
		if a.Department == deptKey {
			v.Agents = append(v.Agents, catalog.TeamAgentView{Role: a.Role, Does: a.Does})
		}
	}
	return v
}

// AgentsOf are the people of one department of the template, in file order.
func (t Template) AgentsOf(deptKey string) []Agent {
	out := []Agent{}
	for _, a := range t.Agents {
		if a.Department == deptKey {
			out = append(out, a)
		}
	}
	return out
}
