package officetemplates

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
	"aiworkforce/backend/internal/roles"
	"aiworkforce/backend/internal/sanitize"
	"aiworkforce/backend/internal/skills"
)

var keyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// emailRe finds something with the shape of an e-mail address: a template never carries people.
var emailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalid, fmt.Sprintf(format, a...))
}

// Parse reads and validates a template file. Strict: unknown fields, trailing data, a newer format, sizes and counts
// over the limits, unknown roles, tones or workflow templates, broken references, invalid skills and texts that
// carry a secret, an e-mail address or read like instructions for the agents are refused with the place that failed.
// cat is the catalog of workflow templates the schedules point at (nil = the default catalog).
func Parse(data []byte, cat *catalog.Catalog) (Template, error) {
	if len(data) > MaxFileBytes {
		return Template{}, ErrTooLarge
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var t Template
	if err := dec.Decode(&t); err != nil {
		return Template{}, invalid("the file is not a valid template: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Template{}, invalid("the file has data after the template")
	}
	if err := Validate(&t, cat); err != nil {
		return Template{}, err
	}
	return t, nil
}

// Validate checks a template (and normalizes its texts in place). Export uses it too: what is exported can be imported.
func Validate(t *Template, cat *catalog.Catalog) error {
	if cat == nil {
		cat = catalog.Default()
	}
	if t.Format != Format {
		return invalid("format: this is not an office template (%q)", t.Format)
	}
	switch {
	case t.FormatVersion > FormatVersion:
		return invalid("format_version: the file was made by a newer version (%d); this one reads %d", t.FormatVersion, FormatVersion)
	case t.FormatVersion < 1:
		return invalid("format_version: missing or invalid")
	}
	if t.Kind != KindDepartment && t.Kind != KindCompany {
		return invalid("kind: must be department or company")
	}
	var err error
	if t.Name, err = text("name", t.Name, MaxName, true); err != nil {
		return err
	}
	if t.For, err = text("for", t.For, MaxText, false); err != nil {
		return err
	}
	if t.Solves, err = text("solves", t.Solves, MaxText, false); err != nil {
		return err
	}
	switch {
	case len(t.Departments) == 0:
		return invalid("departments: a template needs at least one department")
	case len(t.Departments) > MaxDepartments:
		return invalid("departments: at most %d", MaxDepartments)
	case t.Kind == KindDepartment && len(t.Departments) != 1:
		return invalid("departments: a department template has exactly one department")
	case len(t.Agents) == 0:
		return invalid("agents: a template needs at least one person")
	case len(t.Agents) > MaxAgents:
		return invalid("agents: at most %d people", MaxAgents)
	case len(t.Skills) > MaxSkills:
		return invalid("skills: at most %d", MaxSkills)
	case len(t.Workflows) > MaxWorkflows:
		return invalid("workflows: at most %d", MaxWorkflows)
	case len(t.Schedules) > MaxSchedules:
		return invalid("schedules: at most %d", MaxSchedules)
	case len(t.Goals) > MaxGoals:
		return invalid("goals: at most %d", MaxGoals)
	}
	depts, names := map[string]bool{}, map[string]bool{}
	for i := range t.Departments {
		d := &t.Departments[i]
		if !keyRe.MatchString(d.Key) || depts[d.Key] {
			return invalid("departments[%d].key: invalid or repeated", i)
		}
		if d.Name, err = text(fmt.Sprintf("departments[%d].name", i), d.Name, MaxName, true); err != nil {
			return err
		}
		if names[strings.ToLower(d.Name)] {
			return invalid("departments[%d].name: repeated", i)
		}
		depts[d.Key], names[strings.ToLower(d.Name)] = true, true
	}
	skillNames := map[string]bool{}
	for i := range t.Skills {
		if err := validSkill(i, t.Skills[i]); err != nil {
			return err
		}
		if skillNames[t.Skills[i].Name] {
			return invalid("skills[%d].name: repeated", i)
		}
		skillNames[t.Skills[i].Name] = true
	}
	for i := range t.Agents {
		if err := validAgent(i, &t.Agents[i], depts); err != nil {
			return err
		}
	}
	for i := range t.Workflows {
		if err := validWorkflow(i, &t.Workflows[i]); err != nil {
			return err
		}
	}
	for i := range t.Schedules {
		if err := validSchedule(i, &t.Schedules[i], cat); err != nil {
			return err
		}
	}
	for i := range t.Goals {
		if err := validGoal(i, &t.Goals[i], depts); err != nil {
			return err
		}
	}
	nonNilLists(t)
	return nil
}

func nonNilLists(t *Template) {
	if t.Skills == nil {
		t.Skills = []Skill{}
	}
	if t.Workflows == nil {
		t.Workflows = []Workflow{}
	}
	if t.Schedules == nil {
		t.Schedules = []Schedule{}
	}
	if t.Goals == nil {
		t.Goals = []Goal{}
	}
}

// clean drops control characters (keeping new lines and tabs) and trims.
func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// Unsafe says why a free text must not travel in a template ("" = it may): it carries something with the shape of a
// secret or of an e-mail address. Export drops such texts; import refuses them.
func Unsafe(s string) string {
	if sanitize.RedactSecrets(s, nil) != s {
		return "it looks like a secret or credential"
	}
	if emailRe.MatchString(s) {
		return "it contains an e-mail address"
	}
	return ""
}

// text validates one free text: bounded, without secrets or e-mail addresses, and not reading like instructions for
// the agents (the detector of the memory notes and goals, sanitize.ScoreInjection).
func text(field, s string, max int, required bool) (string, error) {
	s = clean(s)
	if s == "" {
		if required {
			return "", invalid("%s: required", field)
		}
		return "", nil
	}
	if len([]rune(s)) > max {
		return "", invalid("%s: longer than %d characters", field, max)
	}
	if why := Unsafe(s); why != "" {
		return "", invalid("%s: %s", field, why)
	}
	if score, _ := sanitize.ScoreInjection(s); score >= sanitize.InjectionThreshold {
		return "", invalid("%s: it reads like instructions to get around the rules", field)
	}
	return s, nil
}

func validSkill(i int, s Skill) error {
	if !skills.ValidName(s.Name) {
		return invalid("skills[%d].name: must match ^[a-z0-9-]{1,64}$", i)
	}
	files := make([]skills.File, 0, len(s.Files))
	for _, f := range s.Files {
		files = append(files, skills.File{Path: f.Path, Content: f.Content})
	}
	pkg, err := skills.Build(s.SkillMD, files) // the same validation as a skill import
	if err != nil {
		return invalid("skills[%d] (%s): %v", i, s.Name, err)
	}
	if pkg.Front.Name != s.Name {
		return invalid("skills[%d]: the SKILL.md is named %q, not %q", i, pkg.Front.Name, s.Name)
	}
	if sanitize.RedactSecrets(s.SkillMD, nil) != s.SkillMD {
		return invalid("skills[%d] (%s): it looks like it carries a secret or credential", i, s.Name)
	}
	for _, f := range s.Files {
		if sanitize.RedactSecrets(f.Content, nil) != f.Content {
			return invalid("skills[%d] (%s) %s: it looks like it carries a secret or credential", i, s.Name, f.Path)
		}
	}
	return nil
}

func validAgent(i int, a *Agent, depts map[string]bool) error {
	at := func(f string) string { return fmt.Sprintf("agents[%d].%s", i, f) }
	if !depts[a.Department] {
		return invalid("%s: unknown department %q", at("department"), a.Department)
	}
	if _, ok := roles.Get(a.Role); !ok {
		return invalid("%s: unknown role %q", at("role"), a.Role)
	}
	var err error
	if a.Does, err = text(at("does"), a.Does, MaxDoes, false); err != nil {
		return err
	}
	if a.Name, err = text(at("name"), a.Name, 60, false); err != nil {
		return err
	}
	if a.Instructions, err = application.CleanInstructions(a.Instructions); err != nil {
		return invalid("%s: %v", at("instructions"), err)
	}
	if a.Instructions != "" {
		if why := Unsafe(a.Instructions); why != "" {
			return invalid("%s: %s", at("instructions"), why)
		}
		if score, _ := sanitize.ScoreInjection(a.Instructions); score >= sanitize.InjectionThreshold {
			return invalid("%s: they read like instructions to get around the rules", at("instructions"))
		}
	}
	switch a.Creativity = strings.ToLower(strings.TrimSpace(a.Creativity)); a.Creativity {
	case "", application.CreativityLow, application.CreativityMedium, application.CreativityHigh:
	default:
		return invalid("%s: must be low, medium or high", at("creativity"))
	}
	if a.Tone = strings.ToLower(strings.TrimSpace(a.Tone)); a.Tone != "" {
		t, ok := catalog.NormalizeTone(a.Tone)
		if !ok {
			return invalid("%s: unknown tone %q", at("tone"), a.Tone)
		}
		a.Tone = t
	}
	if a.Tags, err = application.CleanSkills(a.Tags); err != nil {
		return invalid("%s: %v", at("tags"), err)
	}
	if len(a.Responsibilities) > MaxResponsibilities {
		return invalid("%s: at most %d", at("responsibilities"), MaxResponsibilities)
	}
	for j := range a.Responsibilities {
		if a.Responsibilities[j], err = text(fmt.Sprintf("%s[%d]", at("responsibilities"), j), a.Responsibilities[j], MaxResponsibility, true); err != nil {
			return err
		}
	}
	if len(a.Skills) > MaxSkills {
		return invalid("%s: at most %d", at("skills"), MaxSkills)
	}
	for _, sk := range a.Skills {
		if !skills.ValidName(sk) {
			return invalid("%s: invalid skill name %q", at("skills"), sk)
		}
	}
	return nil
}

func validWorkflow(i int, w *Workflow) error {
	at := fmt.Sprintf("workflows[%d]", i)
	var err error
	if w.Name, err = text(at+".name", w.Name, 120, true); err != nil {
		return err
	}
	if w.Description, err = text(at+".description", w.Description, 600, false); err != nil {
		return err
	}
	for oi := range w.Objectives {
		o := &w.Objectives[oi]
		if o.Title, err = text(fmt.Sprintf("%s.objectives[%d].title", at, oi), o.Title, 200, true); err != nil {
			return err
		}
		for bi := range o.Workflows {
			b := &o.Workflows[bi]
			if b.Title, err = text(fmt.Sprintf("%s.objectives[%d].workflows[%d].title", at, oi, bi), b.Title, 200, true); err != nil {
				return err
			}
			for ni := range b.Nodes {
				n := &b.Nodes[ni]
				p := fmt.Sprintf("%s.objectives[%d].workflows[%d].nodes[%d]", at, oi, bi, ni)
				if n.Title, err = text(p+".title", n.Title, 200, true); err != nil {
					return err
				}
				if n.Description, err = text(p+".description", n.Description, 600, false); err != nil {
					return err
				}
				for ai := range n.Acceptance {
					if n.Acceptance[ai], err = text(fmt.Sprintf("%s.acceptance[%d]", p, ai), n.Acceptance[ai], 200, true); err != nil {
						return err
					}
				}
				if n.Role != "" {
					if _, ok := roles.Get(n.Role); !ok {
						return invalid("%s.role: unknown role %q", p, n.Role)
					}
				}
				if n.Delegate != nil {
					if _, ok := roles.Get(n.Delegate.Role); !ok {
						return invalid("%s.delegate.role: unknown role %q", p, n.Delegate.Role)
					}
				}
			}
		}
	}
	for pi := range w.Params {
		if w.Params[pi].Default, err = text(fmt.Sprintf("%s.params[%d].default", at, pi), w.Params[pi].Default, MaxParamValue, false); err != nil {
			return err
		}
	}
	for ii := range w.Inputs {
		if w.Inputs[ii].Example, err = text(fmt.Sprintf("%s.inputs[%d].example", at, ii), w.Inputs[ii].Example, MaxParamValue, false); err != nil {
			return err
		}
	}
	// the structure is checked by the work templates themselves (cycles, dangling dependencies, sizes); roles stand in
	// for the agents, which are resolved at install time
	tpl := projects.Template{Name: w.Name, Description: w.Description}
	in := w.ToProjectInput(func(role string) string { return role })
	tpl.Params, tpl.Objectives = in.Params, in.Objectives
	if in.Inputs != nil {
		tpl.Inputs = *in.Inputs
	}
	for _, is := range projects.ValidateTemplate(tpl, nil, projects.Limits{}) {
		if is.Severity == "error" {
			return invalid("%s: the work template is not valid (%s %s)", at, is.Code, is.NodeID)
		}
	}
	return nil
}

func validSchedule(i int, s *Schedule, cat *catalog.Catalog) error {
	at := fmt.Sprintf("schedules[%d]", i)
	if _, ok := cat.Workflow(s.Template, "es"); !ok {
		return invalid("%s.template: unknown workflow template %q", at, s.Template)
	}
	if s.Hour < 0 || s.Hour > 23 || s.Minute < 0 || s.Minute > 59 {
		return invalid("%s: invalid time", at)
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil || s.Timezone == "" || strings.Contains(s.Timezone, "..") {
		return invalid("%s.timezone: unknown time zone", at)
	}
	for _, d := range s.Weekdays {
		if d < 0 || d > 6 {
			return invalid("%s.weekdays: 0 (Sunday) to 6 (Saturday)", at)
		}
	}
	if len(s.Params) > MaxScheduleParams {
		return invalid("%s.params: at most %d", at, MaxScheduleParams)
	}
	for k, v := range s.Params {
		if !keyRe.MatchString(k) {
			return invalid("%s.params: invalid key %q", at, k)
		}
		cv, err := text(at+".params."+k, v, MaxParamValue, false)
		if err != nil {
			return err
		}
		s.Params[k] = cv
	}
	return nil
}

func validGoal(i int, g *Goal, depts map[string]bool) error {
	at := fmt.Sprintf("goals[%d]", i)
	if g.Department != "" && !depts[g.Department] {
		return invalid("%s.department: unknown department %q", at, g.Department)
	}
	var err error
	if g.Title, err = text(at+".title", g.Title, 120, true); err != nil {
		return err
	}
	if g.Description, err = text(at+".description", g.Description, 280, false); err != nil {
		return err
	}
	if g.Period, err = text(at+".period", g.Period, 40, false); err != nil {
		return err
	}
	if g.DueDate = strings.TrimSpace(g.DueDate); g.DueDate != "" {
		if _, err := time.Parse(time.DateOnly, g.DueDate); err != nil {
			return invalid("%s.due_date: must be YYYY-MM-DD", at)
		}
	}
	return nil
}

// Fingerprint identifies the content of a template: importing the same file twice keeps one copy.
func Fingerprint(t Template) string {
	b, _ := json.Marshal(t)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}

// Marshal renders the file (indented, stable field order).
func Marshal(t Template) ([]byte, error) {
	nonNilLists(&t)
	return json.MarshalIndent(t, "", "  ")
}
