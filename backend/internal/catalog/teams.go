package catalog

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	"aiworkforce/backend/internal/roles"
)

// Ready-to-hire teams (F2): a business team is DATA that only points at what already exists. Its agents are roles of
// the role catalog (the role decides tools, autonomy, ceilings and default skills), its first week are workflow
// templates, and the packs say which kind of business it suits. Nothing of a role or a template is repeated here.

// SchemaTeam is the schema of data/teams/*.json.
const SchemaTeam = "aiw.team/1"

type rawTeamAgent struct {
	Role    string `json:"role"`
	DoesKey string `json:"does_key"`
}

type rawTeamWeek struct {
	Template string `json:"template"`
	Day      int    `json:"day"`
	WhyKey   string `json:"why_key"`
}

type rawTeam struct {
	Schema        string                       `json:"schema"`
	Key           string                       `json:"key"`
	Version       int                          `json:"version"`
	Packs         []string                     `json:"packs"`
	NameKey       string                       `json:"name_key"`
	ForKey        string                       `json:"for_key"`
	SolvesKey     string                       `json:"solves_key"`
	DepartmentKey string                       `json:"department_key"`
	Agents        []rawTeamAgent               `json:"agents"`
	Week          []rawTeamWeek                `json:"week"`
	I18n          map[string]map[string]string `json:"i18n"`
}

// TeamAgentView is one person of a team: the role and what they will do in this team.
type TeamAgentView struct {
	Role string `json:"role"`
	Does string `json:"does"`
}

// TeamWeekView is one flow template of the first week, with the day and why it comes then.
type TeamWeekView struct {
	Template string `json:"template"`
	Day      int    `json:"day"`
	Why      string `json:"why"`
}

// TeamView is a team resolved for a locale.
type TeamView struct {
	Key        string          `json:"key"`
	Version    int             `json:"version"`
	Name       string          `json:"name"`
	For        string          `json:"for"`
	Solves     string          `json:"solves"`
	Department string          `json:"department"`
	Packs      []string        `json:"packs"`
	Agents     []TeamAgentView `json:"agents"`
	Week       []TeamWeekView  `json:"week"`
}

func validateTeam(t rawTeam) error {
	if t.Schema != SchemaTeam {
		return fmt.Errorf("unsupported schema %q", t.Schema)
	}
	if !keyRe.MatchString(t.Key) || t.Version < 1 {
		return fmt.Errorf("invalid key/version")
	}
	if len(t.Agents) == 0 {
		return fmt.Errorf("a team needs agents")
	}
	keys := []string{t.NameKey, t.ForKey, t.SolvesKey, t.DepartmentKey}
	seen := map[string]bool{}
	for _, a := range t.Agents {
		if _, ok := roles.Get(a.Role); !ok || seen[a.Role] {
			return fmt.Errorf("unknown or duplicate role %q", a.Role)
		}
		seen[a.Role] = true
		keys = append(keys, a.DoesKey)
	}
	days := map[string]bool{}
	for _, w := range t.Week {
		if w.Day < 1 || w.Day > 5 {
			return fmt.Errorf("week: day %d is outside the first week (1-5)", w.Day)
		}
		if days[w.Template] {
			return fmt.Errorf("week: duplicate template %q", w.Template)
		}
		days[w.Template] = true
		keys = append(keys, w.WhyKey)
	}
	return checkI18n(t.I18n, keys, map[string]bool{})
}

// loadTeams reads data/teams (optional: a catalog without the directory has no teams).
func (c *Catalog) loadTeams(fsys fs.FS, errs *[]string) {
	entries, err := fs.ReadDir(fsys, "data/teams")
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := fs.ReadFile(fsys, path.Join("data/teams", e.Name()))
		var t rawTeam
		if err == nil {
			if err = strictUnmarshal(raw, &t); err == nil {
				err = validateTeam(t)
			}
		}
		if err == nil {
			if _, dup := c.teams[t.Key]; dup {
				err = fmt.Errorf("duplicate team key %q", t.Key)
			}
		}
		if err != nil {
			*errs = append(*errs, e.Name()+": "+err.Error())
			continue
		}
		c.teams[t.Key] = t
	}
	for _, t := range c.teams {
		for _, w := range t.Week {
			if _, ok := c.workflows[w.Template]; !ok {
				*errs = append(*errs, fmt.Sprintf("team %s uses unknown template %q", t.Key, w.Template))
			}
		}
		for _, p := range t.Packs {
			if _, ok := c.packs[p]; !ok {
				*errs = append(*errs, fmt.Sprintf("team %s suits unknown pack %q", t.Key, p))
			}
		}
	}
}

// Teams returns the ready-to-hire teams resolved for locale, ordered by key. A non-empty pack keeps only the teams that
// suit that onboarding pack.
func (c *Catalog) Teams(locale, pack string) []TeamView {
	out := []TeamView{}
	for _, k := range sortedKeys(c.teams) {
		t := c.teams[k]
		if pack = strings.TrimSpace(pack); pack != "" && !contains(t.Packs, pack) {
			continue
		}
		out = append(out, teamView(t, locale))
	}
	return out
}

// Team returns one team resolved for locale.
func (c *Catalog) Team(key, locale string) (TeamView, bool) {
	t, ok := c.teams[key]
	if !ok {
		return TeamView{}, false
	}
	return teamView(t, locale), true
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func teamView(t rawTeam, locale string) TeamView {
	cat := t.I18n[NormalizeLocale(locale)]
	v := TeamView{Key: t.Key, Version: t.Version, Name: cat[t.NameKey], For: cat[t.ForKey], Solves: cat[t.SolvesKey],
		Department: cat[t.DepartmentKey], Packs: append([]string{}, t.Packs...), Agents: []TeamAgentView{}, Week: []TeamWeekView{}}
	for _, a := range t.Agents {
		v.Agents = append(v.Agents, TeamAgentView{Role: a.Role, Does: cat[a.DoesKey]})
	}
	for _, w := range t.Week {
		v.Week = append(v.Week, TeamWeekView{Template: w.Template, Day: w.Day, Why: cat[w.WhyKey]})
	}
	return v
}
