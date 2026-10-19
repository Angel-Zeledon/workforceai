package orgchart

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/roles"
)

// Industries returns the industry templates resolved for a locale.
func (s *Service) Industries(locale string) []catalog.IndustryView {
	return catalog.Industries().Industries(locale)
}

// ApplyInput is the body to apply an industry template.
type ApplyInput struct {
	Locale string `json:"locale"`
	// SkipProjects leaves the sample projects out (they are created as organization templates).
	SkipProjects bool `json:"skip_projects"`
}

// ApplyResult says what applying a template did. Everything is idempotent: running it twice
// creates nothing twice.
type ApplyResult struct {
	Key              string   `json:"key"`
	Departments      []string `json:"departments"`       // ids created
	Agents           []string `json:"agents"`            // ids created
	AgentsExisting   []string `json:"agents_existing"`   // ids that were already there (kept as they are)
	Rules            int      `json:"rules"`             // delegation rules written
	ProjectTemplates []string `json:"project_templates"` // ids of the sample projects created
	AlreadyApplied   bool     `json:"already_applied"`
	DemoSet          string   `json:"demo_set"`
	Locale           string   `json:"locale"`
}

// agentPrefix is the id prefix of the agents of a template (the first word of its key).
func agentPrefix(key string) string { return strings.SplitN(key, "_", 2)[0] }

// AgentID is the deterministic id of a template agent in an organization.
func AgentID(industryKey, agentKey string) string { return agentPrefix(industryKey) + "_" + agentKey }

// DepartmentID is the deterministic id of a template department.
func DepartmentID(industryKey, deptKey string) string { return "ind_" + industryKey + "_" + deptKey }

// ScheduleOf converts the schedule of a template.
func ScheduleOf(s catalog.IndustrySchedule) Schedule {
	return Schedule{Enabled: true, Timezone: s.Timezone, Days: slices.Clone(s.Days), Start: s.Start, End: s.End}.Normalize()
}

// refOf resolves a template reference ("owner" or an agent key) to a Ref.
func refOf(key, industry string) Ref {
	switch key {
	case "":
		return Ref{}
	case "owner":
		return Ref{Kind: KindPerson, ID: OwnerID}
	}
	return Ref{Kind: KindAgent, ID: AgentID(industry, key)}
}

// SelectorOf resolves a template selector ("any", "owner", "agent:k", "dept:k"...).
func SelectorOf(sel, industry string) Selector {
	switch sel {
	case "any":
		return Selector{Type: SelAny}
	case "any_person":
		return Selector{Type: SelAnyPerson}
	case "any_agent":
		return Selector{Type: SelAnyAgent}
	case "owner":
		return Selector{Type: SelPerson, ID: OwnerID}
	}
	kind, key, _ := strings.Cut(sel, ":")
	if kind == "agent" {
		return Selector{Type: SelAgent, ID: AgentID(industry, key)}
	}
	return Selector{Type: SelDepartment, ID: DepartmentID(industry, key)}
}

// ProjectTemplateInput turns a sample project of an industry template into an organization project
// template (literal text, agents mapped to the real agent ids). Pure, so the existing template
// validator can be run over it.
func ProjectTemplateInput(industry string, p catalog.IndustryProjectView) projects.TemplateInput {
	idx := map[string]string{}
	for i, st := range p.Steps {
		idx[st.Key] = "n" + strconv.Itoa(i+1)
	}
	nodes := make([]projects.TemplateNode, 0, len(p.Steps))
	for i, st := range p.Steps {
		n := projects.TemplateNode{Key: "n" + strconv.Itoa(i+1), Title: st.Title, Description: st.Description,
			Agent: AgentID(industry, st.Agent), Complexity: "M"}
		for _, d := range st.DependsOn {
			n.Deps = append(n.Deps, idx[d])
		}
		nodes = append(nodes, n)
	}
	return projects.TemplateInput{Name: p.Name, Description: p.Description, Params: []projects.TemplateParam{},
		Objectives: []projects.TemplateObjective{{Key: "o1", Title: p.Name,
			Workflows: []projects.TemplateWorkflow{{Key: "w1", Title: p.Name, Nodes: nodes}}}}}
}

// uniqueName returns name, or name with a numeric suffix, not used (case-insensitive) in used.
func uniqueName(name string, used map[string]bool) string {
	n := name
	for i := 2; used[strings.ToLower(n)]; i++ {
		n = fmt.Sprintf("%s (%d)", name, i)
	}
	used[strings.ToLower(n)] = true
	return n
}

// ApplyIndustry sets up departments, the starting team with job descriptions, delegation rules and
// the sample projects of an industry template. It never overwrites what already exists.
func (s *Service) ApplyIndustry(ctx context.Context, key string, in ApplyInput) (ApplyResult, error) {
	view, ok := catalog.Industries().Industry(key, in.Locale)
	if !ok {
		return ApplyResult{}, fmt.Errorf("%w: unknown industry template %q", domain.ErrNotFound, key)
	}
	org := s.org(ctx)
	w, err := s.writer()
	if err != nil {
		return ApplyResult{}, err
	}
	agents, list, err := s.agentMap(ctx)
	if err != nil {
		return ApplyResult{}, err
	}
	res := ApplyResult{Key: key, DemoSet: view.DemoSet, Locale: view.Locale, Departments: []string{}, Agents: []string{}, AgentsExisting: []string{}, ProjectTemplates: []string{}}
	var fresh []catalog.IndustryAgentView
	for _, a := range view.Agents {
		if _, exists := agents[AgentID(key, a.Key)]; exists {
			res.AgentsExisting = append(res.AgentsExisting, AgentID(key, a.Key))
		} else {
			fresh = append(fresh, a)
		}
	}
	if len(list)+len(fresh) > MaxAgents {
		return ApplyResult{}, fmt.Errorf("%w: the office would have %d agents (max %d); remove some first", domain.ErrConflict, len(list)+len(fresh), MaxAgents)
	}
	prior, hadApplied, err := s.Store.GetApplied(ctx, org, key)
	if err != nil {
		return ApplyResult{}, err
	}
	res.AlreadyApplied = hadApplied

	// departments, parents first
	existing, err := s.Store.ListDepartments(ctx, org)
	if err != nil {
		return ApplyResult{}, err
	}
	have := map[string]Department{}
	used := map[string]bool{}
	for _, d := range existing {
		have[d.ID] = d
		used[strings.ToLower(d.Name)] = true
	}
	now := s.now()
	depth := func(k string) int {
		n := 0
		for p := parentOf(view, k); p != ""; p = parentOf(view, p) {
			n++
		}
		return n
	}
	order := slices.Clone(view.Departments)
	slices.SortStableFunc(order, func(a, b catalog.IndustryDeptView) int { return depth(a.Key) - depth(b.Key) })
	deptName := map[string]string{}
	for _, d := range order {
		id := DepartmentID(key, d.Key)
		if cur, ok := have[id]; ok {
			deptName[d.Key] = cur.Name
			continue
		}
		name := uniqueName(d.Name, used)
		dep := Department{ID: id, Name: name, CostCenter: d.CostCenter, CreatedAt: now, UpdatedAt: now}
		if d.Parent != "" {
			dep.ParentID = DepartmentID(key, d.Parent)
		}
		if err := s.Store.PutDepartment(ctx, org, dep); err != nil {
			return ApplyResult{}, err
		}
		have[id], deptName[d.Key] = dep, name
		res.Departments = append(res.Departments, id)
	}

	// agents and their job descriptions
	roleLoc := roles.Locale(view.Locale) // role personas exist in es and en; pt reads the Spanish one
	for _, a := range fresh {
		t, _ := roles.Get(a.Role)
		id := AgentID(key, a.Key)
		ag := roles.Instantiate(t, roleLoc, id, a.Name)
		ag.Title, ag.Responsibilities, ag.Tools, ag.Autonomy = a.Title, slices.Clone(a.Responsibilities), slices.Clone(a.Tools), a.Autonomy
		dn := deptName[a.Department]
		ag.Department = &dn
		if err := w.CreateAgent(ctx, org, ag); err != nil {
			return ApplyResult{}, err
		}
		if err := w.SetAgentDepartment(ctx, org, id, &dn); err != nil {
			return ApplyResult{}, err
		}
		budget := a.BudgetUSD
		j := Job{AgentID: id, DepartmentID: DepartmentID(key, a.Department), ReportsTo: refOf(a.ReportsTo, key), Escalation: refOf(a.Escalation, key),
			Responsibilities: slices.Clone(a.Responsibilities), AllowedTools: slices.Clone(a.Tools), Schedule: ScheduleOf(a.Schedule),
			BudgetUSD: &budget, UpdatedBy: s.actor(ctx), UpdatedAt: now}
		if err := s.Store.PutJob(ctx, org, j); err != nil {
			return ApplyResult{}, err
		}
		if s.Caps != nil && budget > 0 {
			if err := s.Caps.SetBudgetCap(ctx, org, domain.ScopeAgent, id, budget); err != nil {
				return ApplyResult{}, err
			}
		}
		res.Agents = append(res.Agents, id)
	}
	// department heads (agents exist now); only departments created by this run
	for _, d := range view.Departments {
		id := DepartmentID(key, d.Key)
		if d.Head == "" || !slices.Contains(res.Departments, id) {
			continue
		}
		dep := have[id]
		dep.Head = Ref{Kind: KindAgent, ID: AgentID(key, d.Head)}
		if err := s.Store.PutDepartment(ctx, org, dep); err != nil {
			return ApplyResult{}, err
		}
	}
	s.invalidate(org)

	// delegation rules (deterministic ids: re-applying rewrites the same rules)
	for i, r := range view.Delegation {
		rule := Rule{ID: fmt.Sprintf("ind_%s_%d", key, i+1), From: SelectorOf(r.From, key), To: SelectorOf(r.To, key), Effect: r.Effect,
			RequireApproval: r.Approval, Approver: refOf(r.Approver, key), Note: r.Note, CreatedBy: s.actor(ctx), CreatedAt: now}
		if err := ValidateRule(rule); err != nil {
			return ApplyResult{}, invalid("template rule %d: %v", i+1, err)
		}
		if err := s.Store.PutRule(ctx, org, rule); err != nil {
			return ApplyResult{}, err
		}
		res.Rules++
	}
	s.invalidate(org)

	// sample projects: once per organization and template
	if !in.SkipProjects && s.Templates != nil && !hadApplied {
		for _, p := range view.Projects {
			t, err := s.Templates.CreateTemplate(ctx, ProjectTemplateInput(key, p))
			if err != nil {
				return ApplyResult{}, fmt.Errorf("sample project %q: %w", p.Key, err)
			}
			res.ProjectTemplates = append(res.ProjectTemplates, t.ID)
		}
	}
	detail := map[string]any{"agents": res.Agents, "departments": res.Departments, "project_templates": res.ProjectTemplates}
	if hadApplied {
		for k, v := range prior.Detail {
			if _, ok := detail[k]; !ok {
				detail[k] = v
			}
		}
	}
	if err := s.Store.PutApplied(ctx, org, Applied{Key: key, Version: view.Version, Locale: view.Locale, AppliedBy: s.actor(ctx), AppliedAt: now, Detail: detail}); err != nil {
		return ApplyResult{}, err
	}
	s.audit(ctx, "industry.applied", "org", key, map[string]any{"version": view.Version, "locale": view.Locale, "agents": res.Agents,
		"departments": res.Departments, "rules": res.Rules, "project_templates": len(res.ProjectTemplates)})
	return res, nil
}

func parentOf(v catalog.IndustryView, key string) string {
	for _, d := range v.Departments {
		if d.Key == key {
			return d.Parent
		}
	}
	return ""
}
