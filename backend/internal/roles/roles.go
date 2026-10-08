// Package roles holds the professions as data: role templates embedded from
// templates/*.json (schema aiw.role_template/1, docs/architecture/professions-catalog.md).
// A template describes persona, responsibilities, tools, autonomy, routing cues
// and display; it never grants more than the engine, the organization or RBAC do.
// The 7 agents of the demo org are templates with a `seed` block.
package roles

import (
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"aiworkforce/backend/internal/domain"
)

//go:embed templates/*.json
var files embed.FS

const Schema = "aiw.role_template/1"

type Text struct {
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	Persona          string   `json:"persona"`
	Responsibilities []string `json:"responsibilities"`
	Area             string   `json:"area"`
	Disclaimers      []string `json:"disclaimers"`
	OutOfScope       []string `json:"out_of_scope"`
}

type Appearance struct {
	Skin      string `json:"skin"`
	Hair      string `json:"hair"`
	HairStyle string `json:"hair_style"`
	Accessory string `json:"accessory"`
	Tie       bool   `json:"tie"`
	Female    bool   `json:"female"`
}

type Display struct {
	Color      string              `json:"color"`
	Appearance Appearance          `json:"appearance"`
	NamePool   map[string][]string `json:"name_pool"`
}

type Autonomy struct {
	Default string `json:"default"`
	Ceiling string `json:"ceiling"`
}

type Routing struct {
	Topic    string              `json:"topic"`
	Keywords map[string][]string `json:"keywords"`
	Related  []string            `json:"related"`
}

// Seed marks a template that is instantiated in every new organization with a fixed id and name.
type Seed struct {
	AgentID  string `json:"agent_id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

type Template struct {
	Schema             string          `json:"schema"`
	ID                 string          `json:"id"`
	Version            int             `json:"version"`
	Category           string          `json:"category"`
	RiskTier           string          `json:"risk_tier"`
	SensitiveData      []string        `json:"sensitive_data"`
	Seed               *Seed           `json:"seed,omitempty"`
	I18n               map[string]Text `json:"i18n"`
	Display            Display         `json:"display"`
	Tools              []string        `json:"tools"`
	Permissions        []string        `json:"permissions"`
	Autonomy           Autonomy        `json:"autonomy"`
	Routing            Routing         `json:"routing"`
	SuggestedArtifacts []string        `json:"suggested_artifacts"`
}

// KnownTools are the tool labels the engine knows. A template can only combine
// these; a new tool needs a connector or adapter first (catalog sec. 3.4).
var KnownTools = map[string]bool{
	"crm": true, "email": true, "proposal_builder": true, "ats": true, "contract_review": true, "docs": true,
	"ledger": true, "spreadsheet": true, "bi": true, "planner": true, "inventory": true, "calendar": true,
	"artifacts": true, "calculator": true, "repo_read": true,
}

var autonomyRank = map[string]int{"suggest": 1, "approve_each": 2, "rules": 3, "autonomous": 4}

// tierCeiling is the highest autonomy a risk tier allows (catalog sec. 2.1).
var tierCeiling = map[string]string{"green": "autonomous", "amber": "rules", "red": "approve_each"}

var (
	idRe    = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)
	colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	// a persona must not smuggle URLs, secrets or "ignore the rules" instructions
	personaBad = regexp.MustCompile(`(?i)(https?://|api[_-]?key|password|contraseña|ignor(e|a) (the |las |todas )?(rules|reglas|instrucciones|instructions|policies|políticas))`)
)

// Validate is the template lint: it rejects anything that could grant power or break the UI.
func Validate(t Template) error {
	switch {
	case t.Schema != Schema:
		return fmt.Errorf("%s: schema must be %s", t.ID, Schema)
	case !idRe.MatchString(t.ID):
		return fmt.Errorf("invalid template id %q", t.ID)
	case t.Version < 1:
		return fmt.Errorf("%s: version must be >= 1", t.ID)
	case tierCeiling[t.RiskTier] == "":
		return fmt.Errorf("%s: risk_tier must be green, amber or red", t.ID)
	case !colorRe.MatchString(t.Display.Color):
		return fmt.Errorf("%s: display.color must be #rrggbb", t.ID)
	case t.Routing.Topic == "":
		return fmt.Errorf("%s: routing.topic is required", t.ID)
	}
	def, ceil := autonomyRank[t.Autonomy.Default], autonomyRank[t.Autonomy.Ceiling]
	if def == 0 || ceil == 0 || def > ceil || ceil > autonomyRank[tierCeiling[t.RiskTier]] {
		return fmt.Errorf("%s: autonomy %s/%s exceeds the %s tier ceiling", t.ID, t.Autonomy.Default, t.Autonomy.Ceiling, t.RiskTier)
	}
	for _, tool := range t.Tools {
		if !KnownTools[tool] {
			return fmt.Errorf("%s: tool %q is not registered", t.ID, tool)
		}
	}
	for _, loc := range []string{"es", "en"} {
		x, ok := t.I18n[loc]
		if !ok || x.Title == "" || x.Description == "" || x.Persona == "" || x.Area == "" || len(x.Responsibilities) == 0 {
			return fmt.Errorf("%s: i18n.%s needs title, description, persona, area and responsibilities", t.ID, loc)
		}
		if personaBad.MatchString(x.Persona) {
			return fmt.Errorf("%s: persona (%s) contains a forbidden pattern", t.ID, loc)
		}
		if len(t.Routing.Keywords[loc]) == 0 {
			return fmt.Errorf("%s: routing.keywords.%s is empty", t.ID, loc)
		}
	}
	if t.RiskTier == "red" && (len(t.SensitiveData) == 0 || len(t.I18n["es"].Disclaimers) == 0) {
		return fmt.Errorf("%s: red tier needs sensitive_data and disclaimers", t.ID)
	}
	if t.Seed == nil && (len(t.Display.NamePool["es"]) == 0 || len(t.Display.NamePool["en"]) == 0) {
		return fmt.Errorf("%s: display.name_pool needs es and en names", t.ID)
	}
	return nil
}

var (
	loadOnce sync.Once
	all      []Template
	byID     map[string]Template
	loadErr  error
)

func load() {
	entries, err := files.ReadDir("templates")
	if err != nil {
		loadErr = err
		return
	}
	byID = map[string]Template{}
	for _, e := range entries {
		raw, err := files.ReadFile("templates/" + e.Name())
		if err != nil {
			loadErr = err
			return
		}
		var t Template
		if err := json.Unmarshal(raw, &t); err != nil {
			loadErr = fmt.Errorf("%s: %w", e.Name(), err)
			return
		}
		if t.ID+".json" != e.Name() {
			loadErr = fmt.Errorf("%s: id %q does not match the file name", e.Name(), t.ID)
			return
		}
		if err := Validate(t); err != nil {
			loadErr = err
			return
		}
		byID[t.ID] = t
		all = append(all, t)
	}
	// seeds first in their position, then the rest by id
	sort.SliceStable(all, func(i, j int) bool {
		si, sj := all[i].Seed != nil, all[j].Seed != nil
		if si != sj {
			return si
		}
		if si {
			return all[i].Seed.Position < all[j].Seed.Position
		}
		return all[i].ID < all[j].ID
	})
}

func ensure() {
	loadOnce.Do(load)
	if loadErr != nil {
		panic("roles: invalid builtin template: " + loadErr.Error()) // embedded data: a bad file is a build bug
	}
}

// All returns the builtin templates (seeds first).
func All() []Template { ensure(); return slices.Clone(all) }

// Get returns a template by id.
func Get(id string) (Template, bool) { ensure(); t, ok := byID[id]; return t, ok }

// Locale normalizes to "es" (default) or "en".
func Locale(loc string) string {
	if strings.HasPrefix(strings.ToLower(loc), "en") {
		return "en"
	}
	return "es"
}

// Text returns the texts of a template in a locale (Spanish as fallback).
func (t Template) Text(loc string) Text {
	if x, ok := t.I18n[Locale(loc)]; ok {
		return x
	}
	return t.I18n["es"]
}

// SeedAgents returns the agents of the demo organization, built from the seed
// templates (Spanish texts, as they have always been stored).
func SeedAgents() []domain.Agent {
	ensure()
	var out []domain.Agent
	for _, t := range all {
		if t.Seed == nil {
			continue
		}
		a := Instantiate(t, "es", t.Seed.AgentID, t.Seed.Name)
		a.Autonomy = t.Autonomy.Default
		out = append(out, a)
	}
	return out
}

// Instantiate builds a new agent from a template. It grants no connection:
// tools are labels and permissions the engine already checks.
func Instantiate(t Template, loc, id, name string) domain.Agent {
	x := t.Text(loc)
	return domain.Agent{
		ID: id, Name: name, Role: t.ID, Title: x.Title, Description: x.Description,
		State: domain.StateIdle, Activity: map[string]string{"es": "Disponible", "en": "Available"}[Locale(loc)],
		Tools: slices.Clone(t.Tools), Permissions: slices.Clone(t.Permissions), Autonomy: t.Autonomy.Default,
		Persona: x.Persona, Responsibilities: slices.Clone(x.Responsibilities),
	}
}

// Profile is what the runtime needs to route chat and plan work for a role.
type Profile struct {
	Topic    string
	Keywords []string // es + en: a user may write in either language
	Related  []string
	Area     string
}

// ProfileOf returns the routing profile of a role (false for unknown roles).
func ProfileOf(role, loc string) (Profile, bool) {
	t, ok := Get(role)
	if !ok {
		return Profile{}, false
	}
	kw := append(slices.Clone(t.Routing.Keywords["es"]), t.Routing.Keywords["en"]...)
	return Profile{Topic: t.Routing.Topic, Keywords: kw, Related: slices.Clone(t.Routing.Related), Area: t.Text(loc).Area}, true
}
