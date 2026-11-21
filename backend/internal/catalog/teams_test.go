package catalog

import (
	"strings"
	"testing"
	"testing/fstest"

	"aiworkforce/backend/internal/roles"
)

func TestTeamCatalogHasTheFiveBusinessTeams(t *testing.T) {
	c := Default()
	want := []string{"agency", "collections", "month_close", "online_store", "proposal"}
	got := c.Teams("es", "")
	if len(got) != len(want) {
		t.Fatalf("teams = %d, want %d", len(got), len(want))
	}
	for i, k := range want {
		if got[i].Key != k {
			t.Fatalf("team %d = %q, want %q", i, got[i].Key, k)
		}
	}
}

func TestTeamsAreComplete(t *testing.T) {
	c := Default()
	for _, loc := range []string{"es", "en"} {
		for _, tm := range c.Teams(loc, "") {
			if tm.Name == "" || tm.For == "" || tm.Solves == "" || tm.Department == "" {
				t.Fatalf("%s/%s: empty texts: %+v", tm.Key, loc, tm)
			}
			if len(tm.Agents) < 2 || len(tm.Week) < 3 {
				t.Fatalf("%s: needs agents and a first week: %+v", tm.Key, tm)
			}
			for _, a := range tm.Agents {
				if _, ok := roles.Get(a.Role); !ok || a.Does == "" {
					t.Fatalf("%s: agent %+v", tm.Key, a)
				}
			}
			for _, w := range tm.Week {
				if _, ok := c.Workflow(w.Template, loc); !ok || w.Why == "" || w.Day < 1 || w.Day > 5 {
					t.Fatalf("%s: week item %+v", tm.Key, w)
				}
			}
		}
	}
}

func TestTeamsFilterByPackAndLookup(t *testing.T) {
	c := Default()
	for _, tm := range c.Teams("es", "agency") {
		if tm.Key != "agency" {
			t.Fatalf("pack agency suggests %q", tm.Key)
		}
	}
	if len(c.Teams("es", "agency")) != 1 {
		t.Fatal("agency pack must suggest the agency team")
	}
	if _, ok := c.Team("nope", "es"); ok {
		t.Fatal("unknown team found")
	}
	tm, ok := c.Team("collections", "en")
	if !ok || tm.Name != "Collections" {
		t.Fatalf("team = %+v ok=%v", tm, ok)
	}
}

func teamFS(team string) fstest.MapFS {
	return fstest.MapFS{
		"data/workflows/.keep": {}, "data/packs/.keep": {}, "data/tax/.keep": {},
		"data/teams/t.json": {Data: []byte(team)},
	}
}

const teamBody = `{"schema":"aiw.team/1","key":"t","version":1,"packs":[],"name_key":"n","for_key":"f","solves_key":"s","department_key":"d",
"agents":[{"role":"sales","does_key":"a"}],"week":[],
"i18n":{"es":{"n":"N","f":"F","s":"S","d":"D","a":"A"},"en":{"n":"N","f":"F","s":"S","d":"D","a":"A"}}}`

func TestLoadRejectsInvalidTeams(t *testing.T) {
	cases := map[string]string{
		"unknown role":     strings.Replace(teamBody, `"role":"sales"`, `"role":"ghost"`, 1),
		"duplicate role":   strings.Replace(teamBody, `"agents":[{"role":"sales","does_key":"a"}]`, `"agents":[{"role":"sales","does_key":"a"},{"role":"sales","does_key":"a"}]`, 1),
		"no agents":        strings.Replace(teamBody, `"agents":[{"role":"sales","does_key":"a"}]`, `"agents":[]`, 1),
		"unknown workflow": strings.Replace(teamBody, `"week":[]`, `"week":[{"template":"zzz","day":1,"why_key":"a"}]`, 1),
		"unknown pack":     strings.Replace(teamBody, `"packs":[]`, `"packs":["zzz"]`, 1),
		"missing i18n":     strings.Replace(teamBody, `"does_key":"a"`, `"does_key":"zz"`, 1),
		"unknown field":    strings.Replace(teamBody, `"packs":[]`, `"packs":[],"x":1`, 1),
	}
	for name, body := range cases {
		if _, err := Load(teamFS(body)); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
	if _, err := Load(teamFS(teamBody)); err != nil {
		t.Fatalf("valid team rejected: %v", err)
	}
}
