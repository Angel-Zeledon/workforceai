// Package catalog holds the built-in templates of the product as DATA:
// workflow templates (gallery), onboarding packs per business type and
// country tax-template structures. Everything lives in embedded JSON files
// (data/...), every user-visible string is an i18n key resolved against the
// template's own catalog (es default, en), and the whole set is validated when
// it is loaded (unknown agents, unknown dependencies, cycles, missing
// translations, unknown placeholders...). Adding a template is adding a file.
package catalog

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
	"aiworkforce/backend/internal/roles"
)

//go:embed data/workflows/*.json data/packs/*.json data/tax/*.json
var dataFS embed.FS

const (
	SchemaWorkflow = "aiw.workflow_template/1"
	SchemaPack     = "aiw.onboarding_pack/1"
	SchemaTax      = "aiw.tax_template/1"

	// MaxDepth mirrors the orchestrator delegation depth limit.
	MaxDepth = 5
	// MaxParamLen bounds one template parameter value.
	MaxParamLen = 200
)

// Locales supported by template catalogs. Spanish is the default.
var Locales = []string{"es", "en"}

// NormalizeLocale maps "en", "en-US", "EN_us" to "en"; anything else to "es".
func NormalizeLocale(v string) string {
	s := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(v), "_", "-"))
	if s == "en" || strings.HasPrefix(s, "en-") {
		return "en"
	}
	return "es"
}

var placeholderRe = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_]*)\s*\}\}`)

// ---- raw (file) shapes ----

type rawParam struct {
	Key        string `json:"key"`
	LabelKey   string `json:"label_key"`
	Required   bool   `json:"required"`
	DefaultKey string `json:"default_key"`
}

type rawStep struct {
	Key            string   `json:"key"`
	TitleKey       string   `json:"title_key"`
	DescriptionKey string   `json:"description_key"`
	AgentRole      string   `json:"agent_role"`
	DependsOn      []string `json:"depends_on"`
}

type rawWorkflow struct {
	Schema         string                       `json:"schema"`
	Key            string                       `json:"key"`
	Version        int                          `json:"version"`
	Category       string                       `json:"category"`
	NameKey        string                       `json:"name_key"`
	DescriptionKey string                       `json:"description_key"`
	RequestKey     string                       `json:"request_key"`
	Params         []rawParam                   `json:"params"`
	Steps          []rawStep                    `json:"steps"`
	I18n           map[string]map[string]string `json:"i18n"`
}

type rawPackAgent struct {
	ID       string `json:"id"`
	Autonomy string `json:"autonomy"`
}

type rawPackMemory struct {
	AgentID  string `json:"agent_id"`
	Scope    string `json:"scope"`
	Key      string `json:"key"`
	ValueKey string `json:"value_key"`
}

// PackRules are the approval rules of the organization: the ones seeded by the
// onboarding pack (amount threshold and always-approve actions) and, optionally,
// the governance rules an owner configured afterwards. Packs never set
// Governance; applying a pack keeps the governance the organization already has.
type PackRules struct {
	ApprovalAmountUSD float64            `json:"approval_amount_usd"`
	AlwaysApprove     []string           `json:"always_approve"`
	Governance        *policy.Governance `json:"governance,omitempty"`
}

// PackBriefing is the suggested daily briefing time of a pack.
type PackBriefing struct {
	Hour   int `json:"hour"`
	Minute int `json:"minute"`
}

type rawPack struct {
	Schema         string                       `json:"schema"`
	Key            string                       `json:"key"`
	Version        int                          `json:"version"`
	NameKey        string                       `json:"name_key"`
	DescriptionKey string                       `json:"description_key"`
	Agents         []rawPackAgent               `json:"agents"`
	Memory         []rawPackMemory              `json:"memory"`
	Rules          PackRules                    `json:"rules"`
	Templates      []string                     `json:"templates"`
	Briefing       PackBriefing                 `json:"briefing"`
	I18n           map[string]map[string]string `json:"i18n"`
}

type rawTaxField struct {
	Key      string `json:"key"`
	LabelKey string `json:"label_key"`
	Kind     string `json:"kind"`
	Example  string `json:"example"`
}

type rawTaxCheck struct {
	Key     string `json:"key"`
	TextKey string `json:"text_key"`
}

type rawTax struct {
	Schema        string                       `json:"schema"`
	Key           string                       `json:"key"`
	Version       int                          `json:"version"`
	Country       string                       `json:"country"`
	Example       bool                         `json:"example"`
	NameKey       string                       `json:"name_key"`
	DisclaimerKey string                       `json:"disclaimer_key"`
	Fields        []rawTaxField                `json:"fields"`
	Checklist     []rawTaxCheck                `json:"checklist"`
	I18n          map[string]map[string]string `json:"i18n"`
}

// ---- public (resolved) views ----

type ParamView struct {
	Key      string `json:"key"`
	LabelKey string `json:"label_key"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
	Default  string `json:"default"`
}

type StepView struct {
	Key            string   `json:"key"`
	TitleKey       string   `json:"title_key"`
	Title          string   `json:"title"`
	DescriptionKey string   `json:"description_key"`
	Description    string   `json:"description"`
	AgentID        string   `json:"agent_id"`
	DependsOn      []string `json:"depends_on"`
	// Stage is the 1-based longest-path level: steps sharing a stage can run in parallel.
	Stage int `json:"stage"`
}

type WorkflowView struct {
	Key            string      `json:"key"`
	Version        int         `json:"version"`
	Category       string      `json:"category"`
	NameKey        string      `json:"name_key"`
	Name           string      `json:"name"`
	DescriptionKey string      `json:"description_key"`
	Description    string      `json:"description"`
	Params         []ParamView `json:"params"`
	Steps          []StepView  `json:"steps"`
	// ParallelSteps counts steps that share a stage with at least one other step.
	ParallelSteps int `json:"parallel_steps"`
}

type PackAgentView struct {
	ID       string `json:"id"`
	Autonomy string `json:"autonomy"`
}

type PackMemoryView struct {
	AgentID  string `json:"agent_id"`
	Scope    string `json:"scope"`
	Key      string `json:"key"`
	ValueKey string `json:"value_key"`
	Value    string `json:"value"`
}

type PackView struct {
	Key            string           `json:"key"`
	Version        int              `json:"version"`
	NameKey        string           `json:"name_key"`
	Name           string           `json:"name"`
	DescriptionKey string           `json:"description_key"`
	Description    string           `json:"description"`
	Agents         []PackAgentView  `json:"agents"`
	Memory         []PackMemoryView `json:"memory"`
	Rules          PackRules        `json:"rules"`
	Templates      []string         `json:"templates"`
	Briefing       PackBriefing     `json:"briefing"`
}

type TaxFieldView struct {
	Key      string `json:"key"`
	LabelKey string `json:"label_key"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	Example  string `json:"example"`
}

type TaxCheckView struct {
	Key     string `json:"key"`
	TextKey string `json:"text_key"`
	Text    string `json:"text"`
}

type TaxView struct {
	Key           string         `json:"key"`
	Version       int            `json:"version"`
	Country       string         `json:"country"`
	Example       bool           `json:"example"`
	NameKey       string         `json:"name_key"`
	Name          string         `json:"name"`
	DisclaimerKey string         `json:"disclaimer_key"`
	Disclaimer    string         `json:"disclaimer"`
	Fields        []TaxFieldView `json:"fields"`
	Checklist     []TaxCheckView `json:"checklist"`
}

// Task is one planned task produced by Instantiate (same shape as the plan
// the runtime planner would return).
type Task struct {
	Key         string
	Title       string
	Description string
	AgentID     string
	DependsOn   []string
}

// Plan is a template instantiated with parameters, ready to be executed.
type Plan struct {
	TemplateKey string
	Version     int
	Text        string
	Objectives  []string
	Tasks       []Task
}

// Catalog is the validated, immutable set of built-in templates.
type Catalog struct {
	workflows map[string]rawWorkflow
	packs     map[string]rawPack
	tax       map[string]rawTax
}

var (
	defOnce sync.Once
	defCat  *Catalog
	defErr  error
)

// Default returns the embedded catalog. It panics if the embedded data is
// invalid (a programming error caught by the package tests).
func Default() *Catalog {
	defOnce.Do(func() { defCat, defErr = Load(dataFS) })
	if defErr != nil {
		panic("catalog: invalid embedded data: " + defErr.Error())
	}
	return defCat
}

// Load parses and validates a catalog from an fs with data/workflows,
// data/packs and data/tax directories.
func Load(fsys fs.FS) (*Catalog, error) {
	c := &Catalog{workflows: map[string]rawWorkflow{}, packs: map[string]rawPack{}, tax: map[string]rawTax{}}
	seedIDs := map[string]bool{}
	for _, a := range roles.SeedAgents() {
		seedIDs[a.ID] = true
	}
	var errs []string
	load := func(dir string, each func(name string, raw []byte) error) {
		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			errs = append(errs, err.Error())
			return
		}
		for _, e := range entries {
			if e.IsDir() || path.Ext(e.Name()) != ".json" {
				continue
			}
			raw, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
			if err == nil {
				err = each(e.Name(), raw)
			}
			if err != nil {
				errs = append(errs, e.Name()+": "+err.Error())
			}
		}
	}
	load("data/workflows", func(name string, raw []byte) error {
		var w rawWorkflow
		if err := strictUnmarshal(raw, &w); err != nil {
			return err
		}
		if err := validateWorkflow(w, seedIDs); err != nil {
			return err
		}
		if _, dup := c.workflows[w.Key]; dup {
			return fmt.Errorf("duplicate workflow key %q", w.Key)
		}
		c.workflows[w.Key] = w
		return nil
	})
	load("data/packs", func(name string, raw []byte) error {
		var p rawPack
		if err := strictUnmarshal(raw, &p); err != nil {
			return err
		}
		if err := validatePack(p, seedIDs); err != nil {
			return err
		}
		if _, dup := c.packs[p.Key]; dup {
			return fmt.Errorf("duplicate pack key %q", p.Key)
		}
		c.packs[p.Key] = p
		return nil
	})
	load("data/tax", func(name string, raw []byte) error {
		var t rawTax
		if err := strictUnmarshal(raw, &t); err != nil {
			return err
		}
		if err := validateTax(t); err != nil {
			return err
		}
		if _, dup := c.tax[t.Key]; dup {
			return fmt.Errorf("duplicate tax template key %q", t.Key)
		}
		c.tax[t.Key] = t
		return nil
	})
	// Cross references: packs recommend existing workflow templates.
	for _, p := range c.packs {
		for _, tk := range p.Templates {
			if _, ok := c.workflows[tk]; !ok {
				errs = append(errs, fmt.Sprintf("pack %s recommends unknown template %q", p.Key, tk))
			}
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return c, nil
}

func strictUnmarshal(raw []byte, dst any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// ---- validation ----

func checkI18n(i18n map[string]map[string]string, keys []string, allowed map[string]bool) error {
	for _, loc := range Locales {
		cat, ok := i18n[loc]
		if !ok {
			return fmt.Errorf("missing i18n catalog %q", loc)
		}
		for _, k := range keys {
			v, ok := cat[k]
			if !ok || strings.TrimSpace(v) == "" {
				return fmt.Errorf("i18n %s: missing key %q", loc, k)
			}
			if allowed != nil {
				for _, m := range placeholderRe.FindAllStringSubmatch(v, -1) {
					if !allowed[m[1]] {
						return fmt.Errorf("i18n %s: key %q uses unknown placeholder {{%s}}", loc, k, m[1])
					}
				}
			}
		}
	}
	for loc := range i18n {
		if loc != "es" && loc != "en" {
			return fmt.Errorf("unsupported locale %q", loc)
		}
	}
	// Both catalogs must carry exactly the same keys.
	for k := range i18n["es"] {
		if _, ok := i18n["en"][k]; !ok {
			return fmt.Errorf("i18n: key %q missing in en", k)
		}
	}
	for k := range i18n["en"] {
		if _, ok := i18n["es"][k]; !ok {
			return fmt.Errorf("i18n: key %q missing in es", k)
		}
	}
	return nil
}

func validateWorkflow(w rawWorkflow, roles map[string]bool) error {
	if w.Schema != SchemaWorkflow {
		return fmt.Errorf("unsupported schema %q", w.Schema)
	}
	if !keyRe.MatchString(w.Key) || w.Version < 1 {
		return fmt.Errorf("invalid key/version")
	}
	if len(w.Steps) == 0 {
		return fmt.Errorf("a template needs at least one step")
	}
	params := map[string]bool{}
	keys := []string{w.NameKey, w.DescriptionKey, w.RequestKey}
	for _, p := range w.Params {
		if !keyRe.MatchString(p.Key) || params[p.Key] {
			return fmt.Errorf("invalid or duplicate param %q", p.Key)
		}
		params[p.Key] = true
		keys = append(keys, p.LabelKey)
		if p.DefaultKey != "" {
			keys = append(keys, p.DefaultKey)
		}
		if p.Required && p.DefaultKey != "" {
			return fmt.Errorf("param %q cannot be required and have a default", p.Key)
		}
	}
	// Optional params need a default so substitution never leaves holes.
	for _, p := range w.Params {
		if !p.Required && p.DefaultKey == "" {
			return fmt.Errorf("optional param %q needs default_key", p.Key)
		}
	}
	idx := map[string]int{}
	for i, s := range w.Steps {
		if !keyRe.MatchString(s.Key) {
			return fmt.Errorf("invalid step key %q", s.Key)
		}
		if _, dup := idx[s.Key]; dup {
			return fmt.Errorf("duplicate step key %q", s.Key)
		}
		if !roles[s.AgentRole] {
			return fmt.Errorf("step %q: unknown agent_role %q", s.Key, s.AgentRole)
		}
		idx[s.Key] = i
		keys = append(keys, s.TitleKey, s.DescriptionKey)
	}
	if err := checkI18n(w.I18n, keys, params); err != nil {
		return err
	}
	if _, err := stages(w.Steps); err != nil {
		return err
	}
	return nil
}

var keyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// stages computes the 1-based longest-path level of every step and rejects
// unknown dependencies, cycles and plans deeper than MaxDepth.
func stages(steps []rawStep) (map[string]int, error) {
	by := map[string]rawStep{}
	for _, s := range steps {
		by[s.Key] = s
	}
	depth := map[string]int{}
	visiting := map[string]bool{}
	var dfs func(string) (int, error)
	dfs = func(k string) (int, error) {
		if d, ok := depth[k]; ok {
			return d, nil
		}
		if visiting[k] {
			return 0, fmt.Errorf("circular dependency at %q", k)
		}
		visiting[k] = true
		d := 1
		for _, dep := range by[k].DependsOn {
			if _, ok := by[dep]; !ok {
				return 0, fmt.Errorf("step %q depends on unknown step %q", k, dep)
			}
			if dep == k {
				return 0, fmt.Errorf("step %q depends on itself", k)
			}
			dd, err := dfs(dep)
			if err != nil {
				return 0, err
			}
			d = max(d, dd+1)
		}
		visiting[k] = false
		depth[k] = d
		return d, nil
	}
	for _, s := range steps {
		d, err := dfs(s.Key)
		if err != nil {
			return nil, err
		}
		if d > MaxDepth {
			return nil, fmt.Errorf("step %q exceeds the maximum depth %d", s.Key, MaxDepth)
		}
	}
	return depth, nil
}

func validatePack(p rawPack, roles map[string]bool) error {
	if p.Schema != SchemaPack {
		return fmt.Errorf("unsupported schema %q", p.Schema)
	}
	if !keyRe.MatchString(p.Key) || p.Version < 1 {
		return fmt.Errorf("invalid key/version")
	}
	keys := []string{p.NameKey, p.DescriptionKey}
	seen := map[string]bool{}
	for _, a := range p.Agents {
		if !roles[a.ID] || seen[a.ID] {
			return fmt.Errorf("unknown or duplicate agent %q", a.ID)
		}
		seen[a.ID] = true
		if !ValidAutonomy(a.Autonomy) {
			return fmt.Errorf("agent %q: invalid autonomy %q", a.ID, a.Autonomy)
		}
	}
	mem := map[string]bool{}
	for _, m := range p.Memory {
		if !roles[m.AgentID] {
			return fmt.Errorf("memory: unknown agent %q", m.AgentID)
		}
		if m.Scope != "org" && m.Scope != "agent" {
			return fmt.Errorf("memory %q: invalid scope %q", m.Key, m.Scope)
		}
		id := m.AgentID + "/" + m.Scope + "/" + m.Key
		if m.Key == "" || mem[id] {
			return fmt.Errorf("memory: empty or duplicate key %q", id)
		}
		mem[id] = true
		keys = append(keys, m.ValueKey)
	}
	if p.Rules.ApprovalAmountUSD < 0 {
		return fmt.Errorf("rules: negative approval amount")
	}
	for _, a := range p.Rules.AlwaysApprove {
		if !keyRe.MatchString(a) {
			return fmt.Errorf("rules: invalid action %q", a)
		}
	}
	if p.Briefing.Hour < 0 || p.Briefing.Hour > 23 || p.Briefing.Minute < 0 || p.Briefing.Minute > 59 {
		return fmt.Errorf("briefing: invalid time")
	}
	return checkI18n(p.I18n, keys, map[string]bool{})
}

// DisclaimerMarkers must appear in every tax template disclaimer.
var DisclaimerMarkers = map[string]string{"es": "ejemplo, no asesoría fiscal", "en": "example, not tax advice"}

func validateTax(t rawTax) error {
	if t.Schema != SchemaTax {
		return fmt.Errorf("unsupported schema %q", t.Schema)
	}
	if !keyRe.MatchString(t.Key) || t.Version < 1 {
		return fmt.Errorf("invalid key/version")
	}
	if len(t.Country) != 2 || strings.ToUpper(t.Country) != t.Country {
		return fmt.Errorf("country must be an ISO 3166-1 alpha-2 code")
	}
	if !t.Example {
		return fmt.Errorf("tax templates must be flagged example:true until reviewed by a professional")
	}
	keys := []string{t.NameKey, t.DisclaimerKey}
	for _, f := range t.Fields {
		keys = append(keys, f.LabelKey)
	}
	for _, c := range t.Checklist {
		keys = append(keys, c.TextKey)
	}
	if err := checkI18n(t.I18n, keys, map[string]bool{}); err != nil {
		return err
	}
	for loc, marker := range DisclaimerMarkers {
		if !strings.Contains(strings.ToLower(t.I18n[loc][t.DisclaimerKey]), marker) {
			return fmt.Errorf("i18n %s: disclaimer must contain %q", loc, marker)
		}
	}
	return nil
}

// ---- queries ----

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Workflows returns all workflow templates resolved for locale, ordered by key.
func (c *Catalog) Workflows(locale string) []WorkflowView {
	out := []WorkflowView{}
	for _, k := range sortedKeys(c.workflows) {
		out = append(out, c.workflowView(c.workflows[k], locale))
	}
	return out
}

// Workflow returns one workflow template resolved for locale.
func (c *Catalog) Workflow(key, locale string) (WorkflowView, bool) {
	w, ok := c.workflows[key]
	if !ok {
		return WorkflowView{}, false
	}
	return c.workflowView(w, locale), true
}

func pub(tplKey, local string) string { return "workflow." + tplKey + "." + local }

func (c *Catalog) workflowView(w rawWorkflow, locale string) WorkflowView {
	cat := w.I18n[NormalizeLocale(locale)]
	st, _ := stages(w.Steps)
	v := WorkflowView{Key: w.Key, Version: w.Version, Category: w.Category,
		NameKey: pub(w.Key, w.NameKey), Name: cat[w.NameKey],
		DescriptionKey: pub(w.Key, w.DescriptionKey), Description: cat[w.DescriptionKey],
		Params: []ParamView{}, Steps: []StepView{}}
	for _, p := range w.Params {
		pv := ParamView{Key: p.Key, LabelKey: pub(w.Key, p.LabelKey), Label: cat[p.LabelKey], Required: p.Required}
		if p.DefaultKey != "" {
			pv.Default = cat[p.DefaultKey]
		}
		v.Params = append(v.Params, pv)
	}
	width := map[int]int{}
	for _, s := range w.Steps {
		width[st[s.Key]]++
	}
	for _, s := range w.Steps {
		dep := s.DependsOn
		if dep == nil {
			dep = []string{}
		}
		v.Steps = append(v.Steps, StepView{Key: s.Key, TitleKey: pub(w.Key, s.TitleKey), Title: cat[s.TitleKey],
			DescriptionKey: pub(w.Key, s.DescriptionKey), Description: cat[s.DescriptionKey],
			AgentID: s.AgentRole, DependsOn: dep, Stage: st[s.Key]})
		if width[st[s.Key]] > 1 {
			v.ParallelSteps++
		}
	}
	return v
}

// SanitizeParam trims a parameter value, drops control characters and
// placeholder markers and bounds its length. Values are inserted into task
// descriptions, so they are kept to a short single line of plain text.
func SanitizeParam(v string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(v) {
		if r < 0x20 || r == 0x7f {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	s := strings.Join(strings.Fields(b.String()), " ")
	s = strings.NewReplacer("{{", "(", "}}", ")").Replace(s)
	if r := []rune(s); len(r) > MaxParamLen {
		s = string(r[:MaxParamLen])
	}
	return s
}

func subst(s string, vars map[string]string) string {
	return placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		return vars[placeholderRe.FindStringSubmatch(m)[1]]
	})
}

// Instantiate resolves a workflow template with parameters into a plan.
// Unknown parameters are rejected; required ones must be non-empty.
func (c *Catalog) Instantiate(key string, params map[string]string, locale string) (Plan, error) {
	w, ok := c.workflows[key]
	if !ok {
		return Plan{}, fmt.Errorf("%w: unknown template %q", domain.ErrNotFound, key)
	}
	cat := w.I18n[NormalizeLocale(locale)]
	known := map[string]rawParam{}
	for _, p := range w.Params {
		known[p.Key] = p
	}
	for k := range params {
		if _, ok := known[k]; !ok {
			return Plan{}, fmt.Errorf("%w: unknown parameter %q", domain.ErrInvalid, k)
		}
	}
	vars := map[string]string{}
	for _, p := range w.Params {
		v := SanitizeParam(params[p.Key])
		if v == "" {
			if p.Required {
				return Plan{}, fmt.Errorf("%w: parameter %q is required", domain.ErrInvalid, p.Key)
			}
			v = cat[p.DefaultKey]
		}
		vars[p.Key] = v
	}
	plan := Plan{TemplateKey: w.Key, Version: w.Version, Text: subst(cat[w.RequestKey], vars)}
	plan.Objectives = []string{plan.Text}
	for _, s := range w.Steps {
		dep := append([]string(nil), s.DependsOn...)
		plan.Tasks = append(plan.Tasks, Task{Key: s.Key, Title: subst(cat[s.TitleKey], vars),
			Description: subst(cat[s.DescriptionKey], vars), AgentID: s.AgentRole, DependsOn: dep})
	}
	return plan, nil
}

// Packs returns all onboarding packs resolved for locale, ordered by key.
func (c *Catalog) Packs(locale string) []PackView {
	out := []PackView{}
	for _, k := range sortedKeys(c.packs) {
		out = append(out, c.packView(c.packs[k], locale))
	}
	return out
}

// Pack returns one onboarding pack resolved for locale.
func (c *Catalog) Pack(key, locale string) (PackView, bool) {
	p, ok := c.packs[key]
	if !ok {
		return PackView{}, false
	}
	return c.packView(p, locale), true
}

func (c *Catalog) packView(p rawPack, locale string) PackView {
	cat := p.I18n[NormalizeLocale(locale)]
	pp := func(local string) string { return "pack." + p.Key + "." + local }
	v := PackView{Key: p.Key, Version: p.Version, NameKey: pp(p.NameKey), Name: cat[p.NameKey],
		DescriptionKey: pp(p.DescriptionKey), Description: cat[p.DescriptionKey], Rules: p.Rules,
		Templates: append([]string{}, p.Templates...), Briefing: p.Briefing,
		Agents: []PackAgentView{}, Memory: []PackMemoryView{}}
	v.Rules.AlwaysApprove = append([]string{}, p.Rules.AlwaysApprove...)
	for _, a := range p.Agents {
		v.Agents = append(v.Agents, PackAgentView{ID: a.ID, Autonomy: a.Autonomy})
	}
	for _, m := range p.Memory {
		v.Memory = append(v.Memory, PackMemoryView{AgentID: m.AgentID, Scope: m.Scope, Key: m.Key,
			ValueKey: pp(m.ValueKey), Value: cat[m.ValueKey]})
	}
	return v
}

// TaxTemplates returns the example tax structures, optionally filtered by
// ISO country code (case-insensitive; "" = all).
func (c *Catalog) TaxTemplates(country, locale string) []TaxView {
	out := []TaxView{}
	country = strings.ToUpper(strings.TrimSpace(country))
	for _, k := range sortedKeys(c.tax) {
		t := c.tax[k]
		if country != "" && t.Country != country {
			continue
		}
		cat := t.I18n[NormalizeLocale(locale)]
		tp := func(local string) string { return "tax." + t.Key + "." + local }
		v := TaxView{Key: t.Key, Version: t.Version, Country: t.Country, Example: t.Example,
			NameKey: tp(t.NameKey), Name: cat[t.NameKey], DisclaimerKey: tp(t.DisclaimerKey), Disclaimer: cat[t.DisclaimerKey],
			Fields: []TaxFieldView{}, Checklist: []TaxCheckView{}}
		for _, f := range t.Fields {
			v.Fields = append(v.Fields, TaxFieldView{Key: f.Key, LabelKey: tp(f.LabelKey), Label: cat[f.LabelKey], Kind: f.Kind, Example: f.Example})
		}
		for _, ch := range t.Checklist {
			v.Checklist = append(v.Checklist, TaxCheckView{Key: ch.Key, TextKey: tp(ch.TextKey), Text: cat[ch.TextKey]})
		}
		out = append(out, v)
	}
	return out
}

// HasWorkflow reports whether a workflow template exists.
func (c *Catalog) HasWorkflow(key string) bool { _, ok := c.workflows[key]; return ok }

// ---- tone & autonomy ----

// Tones are the supported regional tones. "neutral" is the default and means
// no regional flavour (retro-compatible with runtimes that ignore the field).
var Tones = []string{"neutral", "mx", "co", "ar", "cl", "es"}

// NormalizeTone lower-cases and validates a tone code; "" maps to "neutral".
func NormalizeTone(v string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(v))
	if s == "" {
		return "neutral", true
	}
	for _, t := range Tones {
		if t == s {
			return s, true
		}
	}
	return "", false
}

// ValidAutonomy reports whether v is a known autonomy level.
func ValidAutonomy(v string) bool {
	switch v {
	case "suggest", "approve_each", "rules", "autonomous":
		return true
	}
	return false
}
