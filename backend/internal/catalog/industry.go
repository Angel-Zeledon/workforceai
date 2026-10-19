package catalog

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/roles"
)

// Industry templates (docs/architecture/organization.md): a whole company skeleton for a kind of
// business as DATA. Each one defines departments, a starting team of agents with their job
// descriptions (role, responsibilities, allowed tools, escalation contact, working schedule, budget),
// delegation rules, sample projects and the name of a demo dataset. They are additive to the
// workflow templates and onboarding packs above and have their own loader because they are
// published in three languages (es, en, pt) while the older catalog is es/en.
//
// A template never grants power: every agent is built from an existing role template, its tools
// must be a subset of that role's tools, its autonomy cannot exceed the role's ceiling, and
// applying the template creates agents that still need connections and approvals like any other.

//go:embed data/industries/*.json
var industryFS embed.FS

const SchemaIndustry = "aiw.industry_template/1"

// IndustryLocales are the languages every industry template must be complete in.
var IndustryLocales = []string{"es", "en", "pt"}

// NormalizeIndustryLocale maps pt*, en* and everything else (es) to one of IndustryLocales.
func NormalizeIndustryLocale(v string) string {
	s := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(v), "_", "-"))
	switch {
	case s == "en" || strings.HasPrefix(s, "en-"):
		return "en"
	case s == "pt" || strings.HasPrefix(s, "pt-"):
		return "pt"
	}
	return "es"
}

// Limits of one industry template.
const (
	MaxIndustryAgents      = 12
	MaxIndustryDepartments = 12
	MaxIndustryProjects    = 6
	MaxIndustrySteps       = 12
)

// IndustrySchedule is the working schedule of an agent as the templates write it.
type IndustrySchedule struct {
	Timezone string `json:"timezone"`
	Days     []int  `json:"days"`
	Start    string `json:"start"`
	End      string `json:"end"`
}

type rawIndDept struct {
	Key        string `json:"key"`
	NameKey    string `json:"name_key"`
	Parent     string `json:"parent"`
	CostCenter string `json:"cost_center"`
	Head       string `json:"head"` // agent key ("" = none; people are assigned by the owner)
}

type rawIndAgent struct {
	Key              string           `json:"key"`
	Role             string           `json:"role"`
	Department       string           `json:"department"`
	NameKey          string           `json:"name_key"`
	TitleKey         string           `json:"title_key"`
	Responsibilities []string         `json:"responsibility_keys"`
	Tools            []string         `json:"tools"`
	ReportsTo        string           `json:"reports_to"` // agent key, "owner" or ""
	Escalation       string           `json:"escalation"` // agent key or "owner"
	Schedule         IndustrySchedule `json:"schedule"`
	BudgetUSD        float64          `json:"budget_usd"`
	Autonomy         string           `json:"autonomy"`
}

type rawIndRule struct {
	From     string `json:"from"` // any | any_person | any_agent | owner | agent:<key> | dept:<key>
	To       string `json:"to"`
	Effect   string `json:"effect"`
	Approval bool   `json:"approval"`
	Approver string `json:"approver"` // agent key or "owner"
	NoteKey  string `json:"note_key"`
}

type rawIndStep struct {
	Key            string   `json:"key"`
	TitleKey       string   `json:"title_key"`
	DescriptionKey string   `json:"description_key"`
	Agent          string   `json:"agent"` // agent key
	DependsOn      []string `json:"depends_on"`
}

type rawIndProject struct {
	Key            string       `json:"key"`
	NameKey        string       `json:"name_key"`
	DescriptionKey string       `json:"description_key"`
	Steps          []rawIndStep `json:"steps"`
}

type rawIndustry struct {
	Schema         string                       `json:"schema"`
	Key            string                       `json:"key"`
	Version        int                          `json:"version"`
	Category       string                       `json:"category"`
	NameKey        string                       `json:"name_key"`
	DescriptionKey string                       `json:"description_key"`
	DisclaimerKey  string                       `json:"disclaimer_key"`
	Subsidiaries   int                          `json:"subsidiaries"`
	DemoSet        string                       `json:"demo_set"`
	Departments    []rawIndDept                 `json:"departments"`
	Agents         []rawIndAgent                `json:"agents"`
	Delegation     []rawIndRule                 `json:"delegation"`
	Projects       []rawIndProject              `json:"projects"`
	I18n           map[string]map[string]string `json:"i18n"`
}

// ---- resolved views ----

type IndustryDeptView struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	Parent     string `json:"parent"`
	CostCenter string `json:"cost_center"`
	Head       string `json:"head"`
}

type IndustryAgentView struct {
	Key              string           `json:"key"`
	Role             string           `json:"role"`
	Department       string           `json:"department"`
	Name             string           `json:"name"`
	Title            string           `json:"title"`
	Responsibilities []string         `json:"responsibilities"`
	Tools            []string         `json:"tools"`
	ReportsTo        string           `json:"reports_to"`
	Escalation       string           `json:"escalation"`
	Schedule         IndustrySchedule `json:"schedule"`
	BudgetUSD        float64          `json:"budget_usd"`
	Autonomy         string           `json:"autonomy"`
}

type IndustryRuleView struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Effect   string `json:"effect"`
	Approval bool   `json:"approval"`
	Approver string `json:"approver"`
	Note     string `json:"note"`
}

type IndustryStepView struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Agent       string   `json:"agent"`
	DependsOn   []string `json:"depends_on"`
	Stage       int      `json:"stage"`
}

type IndustryProjectView struct {
	Key         string             `json:"key"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Steps       []IndustryStepView `json:"steps"`
}

// IndustryView is an industry template resolved for one language.
type IndustryView struct {
	Key          string                `json:"key"`
	Version      int                   `json:"version"`
	Category     string                `json:"category"`
	Name         string                `json:"name"`
	Description  string                `json:"description"`
	Disclaimer   string                `json:"disclaimer"`
	Subsidiaries int                   `json:"subsidiaries"`
	DemoSet      string                `json:"demo_set"`
	Locale       string                `json:"locale"`
	Departments  []IndustryDeptView    `json:"departments"`
	Agents       []IndustryAgentView   `json:"agents"`
	Delegation   []IndustryRuleView    `json:"delegation"`
	Projects     []IndustryProjectView `json:"projects"`
}

// IndustryCatalog is the validated, immutable set of industry templates.
type IndustryCatalog struct{ items map[string]rawIndustry }

var (
	indOnce sync.Once
	indCat  *IndustryCatalog
	indErr  error
)

// Industries returns the embedded industry templates (panics on invalid embedded data; the tests catch it).
func Industries() *IndustryCatalog {
	indOnce.Do(func() { indCat, indErr = LoadIndustries(industryFS) })
	if indErr != nil {
		panic("catalog: invalid embedded industry templates: " + indErr.Error())
	}
	return indCat
}

// LoadIndustries parses and validates the templates of data/industries in fsys.
func LoadIndustries(fsys fs.FS) (*IndustryCatalog, error) {
	c := &IndustryCatalog{items: map[string]rawIndustry{}}
	entries, err := fs.ReadDir(fsys, "data/industries")
	if err != nil {
		return nil, err
	}
	var errs []string
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := fs.ReadFile(fsys, path.Join("data/industries", e.Name()))
		if err == nil {
			var t rawIndustry
			if err = strictUnmarshal(raw, &t); err == nil {
				if err = ValidateIndustry(t); err == nil {
					if _, dup := c.items[t.Key]; dup {
						err = fmt.Errorf("duplicate industry key %q", t.Key)
					} else if t.Key+".json" != e.Name() {
						err = fmt.Errorf("key %q does not match the file name", t.Key)
					} else {
						c.items[t.Key] = t
					}
				}
			}
		}
		if err != nil {
			errs = append(errs, e.Name()+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return c, nil
}

var (
	clockRe = regexp.MustCompile(`^([01][0-9]|2[0-4]):[0-5][0-9]$`)
	ccRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)
)

var autonomyOrder = map[string]int{"suggest": 1, "approve_each": 2, "rules": 3, "autonomous": 4}

// ValidateIndustry is the lint of one industry template: every reference exists, the structures are
// acyclic, nothing exceeds its role, and all three languages are complete.
func ValidateIndustry(t rawIndustry) error {
	if t.Schema != SchemaIndustry {
		return fmt.Errorf("unsupported schema %q", t.Schema)
	}
	if !keyRe.MatchString(t.Key) || t.Version < 1 {
		return fmt.Errorf("invalid key/version")
	}
	if t.Subsidiaries < 0 || t.Subsidiaries > 10 {
		return fmt.Errorf("subsidiaries must be between 0 and 10")
	}
	if len(t.Departments) < 2 || len(t.Departments) > MaxIndustryDepartments {
		return fmt.Errorf("a template needs 2-%d departments", MaxIndustryDepartments)
	}
	if len(t.Agents) < 2 || len(t.Agents) > MaxIndustryAgents {
		return fmt.Errorf("a template needs 2-%d agents", MaxIndustryAgents)
	}
	if len(t.Projects) < 1 || len(t.Projects) > MaxIndustryProjects {
		return fmt.Errorf("a template needs 1-%d sample projects", MaxIndustryProjects)
	}
	if t.DemoSet == "" {
		return fmt.Errorf("demo_set is required")
	}
	keys := []string{t.NameKey, t.DescriptionKey, t.DisclaimerKey}

	depts := map[string]rawIndDept{}
	for _, d := range t.Departments {
		if !keyRe.MatchString(d.Key) {
			return fmt.Errorf("invalid department key %q", d.Key)
		}
		if _, dup := depts[d.Key]; dup {
			return fmt.Errorf("duplicate department %q", d.Key)
		}
		if d.CostCenter == "" || !ccRe.MatchString(d.CostCenter) {
			return fmt.Errorf("department %q: invalid cost center", d.Key)
		}
		depts[d.Key] = d
		keys = append(keys, d.NameKey)
	}
	parent := map[string]string{}
	for _, d := range t.Departments {
		if d.Parent != "" {
			if _, ok := depts[d.Parent]; !ok {
				return fmt.Errorf("department %q: unknown parent %q", d.Key, d.Parent)
			}
		}
		parent[d.Key] = d.Parent
	}
	if k, ok := cycleIn(parent); ok {
		return fmt.Errorf("departments form a cycle at %q", k)
	}

	agents := map[string]rawIndAgent{}
	for _, a := range t.Agents {
		if !keyRe.MatchString(a.Key) {
			return fmt.Errorf("invalid agent key %q", a.Key)
		}
		if _, dup := agents[a.Key]; dup {
			return fmt.Errorf("duplicate agent %q", a.Key)
		}
		agents[a.Key] = a
	}
	reports := map[string]string{}
	for _, a := range t.Agents {
		role, ok := roles.Get(a.Role)
		if !ok {
			return fmt.Errorf("agent %q: unknown role %q", a.Key, a.Role)
		}
		if _, ok := depts[a.Department]; !ok {
			return fmt.Errorf("agent %q: unknown department %q", a.Key, a.Department)
		}
		if len(a.Tools) == 0 {
			return fmt.Errorf("agent %q: allowed tools are required", a.Key)
		}
		for _, tool := range a.Tools {
			if !roles.KnownTools[tool] || !slices.Contains(role.Tools, tool) {
				return fmt.Errorf("agent %q: tool %q is not one of the tools of role %q", a.Key, tool, a.Role)
			}
		}
		if !ValidAutonomy(a.Autonomy) || autonomyOrder[a.Autonomy] > autonomyOrder[role.Autonomy.Ceiling] {
			return fmt.Errorf("agent %q: autonomy %q exceeds the ceiling of role %q", a.Key, a.Autonomy, a.Role)
		}
		if a.BudgetUSD < 0 || a.BudgetUSD > 100000 {
			return fmt.Errorf("agent %q: invalid budget", a.Key)
		}
		if err := checkIndSchedule(a.Schedule); err != nil {
			return fmt.Errorf("agent %q: %w", a.Key, err)
		}
		if a.ReportsTo != "" && a.ReportsTo != "owner" {
			if _, ok := agents[a.ReportsTo]; !ok || a.ReportsTo == a.Key {
				return fmt.Errorf("agent %q: invalid reports_to %q", a.Key, a.ReportsTo)
			}
			reports[a.Key] = a.ReportsTo
		}
		if a.Escalation == "" {
			return fmt.Errorf("agent %q: an escalation contact is required", a.Key)
		}
		if a.Escalation != "owner" {
			if _, ok := agents[a.Escalation]; !ok || a.Escalation == a.Key {
				return fmt.Errorf("agent %q: invalid escalation %q", a.Key, a.Escalation)
			}
		}
		if len(a.Responsibilities) == 0 || len(a.Responsibilities) > 12 {
			return fmt.Errorf("agent %q: 1-12 responsibilities are required", a.Key)
		}
		keys = append(keys, a.NameKey, a.TitleKey)
		keys = append(keys, a.Responsibilities...)
	}
	if k, ok := cycleIn(reports); ok {
		return fmt.Errorf("reporting lines form a cycle at %q", k)
	}
	for _, d := range t.Departments {
		if d.Head != "" {
			if _, ok := agents[d.Head]; !ok {
				return fmt.Errorf("department %q: unknown head %q", d.Key, d.Head)
			}
		}
	}
	for _, r := range t.Delegation {
		if err := checkIndSelector(r.From, agents, depts); err != nil {
			return fmt.Errorf("delegation from: %w", err)
		}
		if err := checkIndSelector(r.To, agents, depts); err != nil {
			return fmt.Errorf("delegation to: %w", err)
		}
		if r.Effect != "allow" && r.Effect != "deny" {
			return fmt.Errorf("delegation: effect must be allow or deny")
		}
		if r.Effect == "deny" && (r.Approval || r.Approver != "") || (r.Approver != "" && !r.Approval) {
			return fmt.Errorf("delegation: approval fields do not fit the effect")
		}
		if r.Approver != "" && r.Approver != "owner" {
			if _, ok := agents[r.Approver]; !ok {
				return fmt.Errorf("delegation: unknown approver %q", r.Approver)
			}
		}
		if r.NoteKey != "" {
			keys = append(keys, r.NoteKey)
		}
	}
	projs := map[string]bool{}
	for _, p := range t.Projects {
		if !keyRe.MatchString(p.Key) || projs[p.Key] {
			return fmt.Errorf("invalid or duplicate project %q", p.Key)
		}
		projs[p.Key] = true
		if len(p.Steps) == 0 || len(p.Steps) > MaxIndustrySteps {
			return fmt.Errorf("project %q needs 1-%d steps", p.Key, MaxIndustrySteps)
		}
		keys = append(keys, p.NameKey, p.DescriptionKey)
		var steps []rawStep
		seen := map[string]bool{}
		for _, s := range p.Steps {
			if !keyRe.MatchString(s.Key) || seen[s.Key] {
				return fmt.Errorf("project %q: invalid or duplicate step %q", p.Key, s.Key)
			}
			seen[s.Key] = true
			if _, ok := agents[s.Agent]; !ok {
				return fmt.Errorf("project %q step %q: unknown agent %q", p.Key, s.Key, s.Agent)
			}
			keys = append(keys, s.TitleKey, s.DescriptionKey)
			steps = append(steps, rawStep{Key: s.Key, DependsOn: s.DependsOn})
		}
		if _, err := stages(steps); err != nil {
			return fmt.Errorf("project %q: %w", p.Key, err)
		}
	}
	return checkI18nLocales(t.I18n, keys, IndustryLocales)
}

func cycleIn(parent map[string]string) (string, bool) {
	state := map[string]int{}
	var visit func(string) (string, bool)
	visit = func(k string) (string, bool) {
		switch state[k] {
		case 1:
			return k, true
		case 2:
			return "", false
		}
		state[k] = 1
		if p := parent[k]; p != "" {
			if c, bad := visit(p); bad {
				return c, true
			}
		}
		state[k] = 2
		return "", false
	}
	ks := make([]string, 0, len(parent))
	for k := range parent {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		if c, bad := visit(k); bad {
			return c, true
		}
	}
	return "", false
}

func checkIndSchedule(s IndustrySchedule) error {
	if _, err := time.LoadLocation(s.Timezone); err != nil || s.Timezone == "" {
		return fmt.Errorf("unknown timezone %q", s.Timezone)
	}
	if len(s.Days) == 0 {
		return fmt.Errorf("schedule needs working days")
	}
	seen := map[int]bool{}
	for _, d := range s.Days {
		if d < 0 || d > 6 || seen[d] {
			return fmt.Errorf("schedule days must be unique 0-6")
		}
		seen[d] = true
	}
	if !clockRe.MatchString(s.Start) || !clockRe.MatchString(s.End) || s.Start == s.End {
		return fmt.Errorf("schedule start/end must be different HH:MM times")
	}
	if n, _ := strconv.Atoi(s.Start[:2]); n == 24 {
		return fmt.Errorf("schedule start cannot be 24:00")
	}
	return nil
}

func checkIndSelector(sel string, agents map[string]rawIndAgent, depts map[string]rawIndDept) error {
	switch sel {
	case "any", "any_person", "any_agent", "owner":
		return nil
	}
	kind, key, ok := strings.Cut(sel, ":")
	switch {
	case ok && kind == "agent":
		if _, found := agents[key]; found {
			return nil
		}
	case ok && kind == "dept":
		if _, found := depts[key]; found {
			return nil
		}
	}
	return fmt.Errorf("invalid selector %q", sel)
}

// checkI18nLocales requires the exact set of locales, the same keys in each and every referenced key.
func checkI18nLocales(i18n map[string]map[string]string, keys, locales []string) error {
	for loc := range i18n {
		if !slices.Contains(locales, loc) {
			return fmt.Errorf("unsupported locale %q", loc)
		}
	}
	for _, loc := range locales {
		cat, ok := i18n[loc]
		if !ok {
			return fmt.Errorf("missing i18n catalog %q", loc)
		}
		for _, k := range keys {
			if strings.TrimSpace(cat[k]) == "" {
				return fmt.Errorf("i18n %s: missing key %q", loc, k)
			}
		}
		for k := range cat {
			for _, other := range locales {
				if _, ok := i18n[other][k]; !ok {
					return fmt.Errorf("i18n: key %q missing in %s", k, other)
				}
			}
		}
	}
	return nil
}

// ---- queries ----

// Keys lists the industry template keys in order.
func (c *IndustryCatalog) Keys() []string { return sortedKeys(c.items) }

// Industries returns every template resolved for a locale, ordered by key.
func (c *IndustryCatalog) Industries(locale string) []IndustryView {
	out := []IndustryView{}
	for _, k := range sortedKeys(c.items) {
		out = append(out, c.view(c.items[k], locale))
	}
	return out
}

// Industry returns one template resolved for a locale.
func (c *IndustryCatalog) Industry(key, locale string) (IndustryView, bool) {
	t, ok := c.items[key]
	if !ok {
		return IndustryView{}, false
	}
	return c.view(t, locale), true
}

func (c *IndustryCatalog) view(t rawIndustry, locale string) IndustryView {
	loc := NormalizeIndustryLocale(locale)
	cat := t.I18n[loc]
	v := IndustryView{Key: t.Key, Version: t.Version, Category: t.Category, Name: cat[t.NameKey], Description: cat[t.DescriptionKey],
		Disclaimer: cat[t.DisclaimerKey], Subsidiaries: t.Subsidiaries, DemoSet: t.DemoSet, Locale: loc,
		Departments: []IndustryDeptView{}, Agents: []IndustryAgentView{}, Delegation: []IndustryRuleView{}, Projects: []IndustryProjectView{}}
	for _, d := range t.Departments {
		v.Departments = append(v.Departments, IndustryDeptView{Key: d.Key, Name: cat[d.NameKey], Parent: d.Parent, CostCenter: d.CostCenter, Head: d.Head})
	}
	for _, a := range t.Agents {
		av := IndustryAgentView{Key: a.Key, Role: a.Role, Department: a.Department, Name: cat[a.NameKey], Title: cat[a.TitleKey],
			Responsibilities: []string{}, Tools: slices.Clone(a.Tools), ReportsTo: a.ReportsTo, Escalation: a.Escalation,
			Schedule: a.Schedule, BudgetUSD: a.BudgetUSD, Autonomy: a.Autonomy}
		av.Schedule.Days = slices.Clone(a.Schedule.Days)
		for _, k := range a.Responsibilities {
			av.Responsibilities = append(av.Responsibilities, cat[k])
		}
		v.Agents = append(v.Agents, av)
	}
	for _, r := range t.Delegation {
		v.Delegation = append(v.Delegation, IndustryRuleView{From: r.From, To: r.To, Effect: r.Effect, Approval: r.Approval, Approver: r.Approver, Note: cat[r.NoteKey]})
	}
	for _, p := range t.Projects {
		pv := IndustryProjectView{Key: p.Key, Name: cat[p.NameKey], Description: cat[p.DescriptionKey], Steps: []IndustryStepView{}}
		var raw []rawStep
		for _, s := range p.Steps {
			raw = append(raw, rawStep{Key: s.Key, DependsOn: s.DependsOn})
		}
		st, _ := stages(raw)
		for _, s := range p.Steps {
			dep := slices.Clone(s.DependsOn)
			if dep == nil {
				dep = []string{}
			}
			pv.Steps = append(pv.Steps, IndustryStepView{Key: s.Key, Title: cat[s.TitleKey], Description: cat[s.DescriptionKey], Agent: s.Agent, DependsOn: dep, Stage: st[s.Key]})
		}
		v.Projects = append(v.Projects, pv)
	}
	return v
}
