package officetemplates

import (
	"fmt"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/sanitize"
)

// Scrub makes a template drafted from an organization safe to leave it, before Validate: a free text with the shape
// of a secret or an e-mail address, or that reads like instructions to get around the rules, is left out (an
// optional field is emptied, an item whose required text fails is dropped); a skill that carries a secret is left
// out and the e-mail addresses inside the others are blanked; a work template whose steps lost their role is left
// out; the lists are cut to their limits. It returns what was left out, in plain words, for the person exporting.
func Scrub(t *Template, cat *catalog.Catalog) []string {
	if cat == nil {
		cat = catalog.Default()
	}
	notes := []string{}
	note := func(f string, a ...any) { notes = append(notes, fmt.Sprintf(f, a...)) }
	opt := func(s string, max int) string {
		v, err := text("", s, max, false)
		if err != nil {
			return ""
		}
		return v
	}
	t.Format, t.FormatVersion = Format, FormatVersion
	if t.Name = opt(t.Name, MaxName); t.Name == "" {
		t.Name = "Plantilla"
	}
	t.For, t.Solves = opt(t.For, MaxText), opt(t.Solves, MaxText)
	for i := range t.Departments {
		if t.Departments[i].Name = opt(t.Departments[i].Name, MaxName); t.Departments[i].Name == "" {
			t.Departments[i].Name = fmt.Sprintf("Departamento %d", i+1)
		}
	}
	// skills first: an agent only keeps the skills that travel
	kept := []Skill{}
	travels := map[string]bool{}
	for _, s := range t.Skills {
		if len(kept) >= MaxSkills {
			note("skill %s: over the limit of %d skills", s.Name, MaxSkills)
			continue
		}
		s.SkillMD = emailRe.ReplaceAllString(s.SkillMD, "[correo]")
		for j := range s.Files {
			s.Files[j].Content = emailRe.ReplaceAllString(s.Files[j].Content, "[correo]")
		}
		if err := validSkill(0, s); err != nil {
			note("skill %s: left out (%s)", s.Name, plain(err))
			continue
		}
		kept, travels[s.Name] = append(kept, s), true
	}
	t.Skills = kept
	agents := []Agent{}
	for _, a := range t.Agents {
		if len(agents) >= MaxAgents {
			note("%s: over the limit of %d people", a.Role, MaxAgents)
			continue
		}
		a.Does, a.Name = opt(a.Does, MaxDoes), opt(a.Name, 60)
		if ins, err := application.CleanInstructions(a.Instructions); err != nil || Unsafe(ins) != "" || injection(ins) {
			if strings.TrimSpace(a.Instructions) != "" {
				note("%s: the extra instructions were left out", first(a.Name, a.Role))
			}
			a.Instructions = ""
		} else {
			a.Instructions = ins
		}
		if tags, err := application.CleanSkills(a.Tags); err == nil {
			a.Tags = tags
		} else {
			a.Tags = nil
		}
		resp := []string{}
		for _, r := range a.Responsibilities {
			if v := opt(r, MaxResponsibility); v != "" && len(resp) < MaxResponsibilities {
				resp = append(resp, v)
			}
		}
		a.Responsibilities = resp
		sks := []string{}
		for _, s := range a.Skills {
			if travels[s] {
				sks = append(sks, s)
			}
		}
		a.Skills = sks
		agents = append(agents, a)
	}
	t.Agents = agents
	wfs := []Workflow{}
	for _, w := range t.Workflows {
		if len(wfs) >= MaxWorkflows {
			note("work template %s: over the limit of %d", w.Name, MaxWorkflows)
			continue
		}
		if err := validWorkflow(0, &w); err != nil {
			note("work template %s: left out (%s)", w.Name, plain(err))
			continue
		}
		wfs = append(wfs, w)
	}
	t.Workflows = wfs
	scs := []Schedule{}
	for _, s := range t.Schedules {
		params := map[string]string{}
		for k, v := range s.Params {
			if cv := opt(v, MaxParamValue); cv != "" && keyRe.MatchString(k) && len(params) < MaxScheduleParams {
				params[k] = cv
			}
		}
		s.Params = params
		if len(scs) >= MaxSchedules || validSchedule(0, &s, cat) != nil {
			note("a schedule of %s was left out", s.Template)
			continue
		}
		scs = append(scs, s)
	}
	t.Schedules = scs
	gs := []Goal{}
	for _, g := range t.Goals {
		g.Description, g.Period = opt(g.Description, 280), opt(g.Period, 40)
		title := opt(g.Title, 120)
		if title == "" || len(gs) >= MaxGoals {
			note("a goal was left out")
			continue
		}
		g.Title = title
		gs = append(gs, g)
	}
	t.Goals = gs
	nonNilLists(t)
	return notes
}

func injection(s string) bool {
	if s == "" {
		return false
	}
	score, _ := sanitize.ScoreInjection(s)
	return score >= sanitize.InjectionThreshold
}

// plain is the reason of a validation error without the "invalid input:" prefix.
func plain(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ": "); i >= 0 {
		return msg[i+2:]
	}
	return msg
}
