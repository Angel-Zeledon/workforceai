package projects

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

var demoAgents = []string{"accounting", "analyst", "legal", "sales", "hr", "operations", "assistant"}

func TestGiantTemplatesAreValidAndLarge(t *testing.T) {
	want := map[string][2]int{ // key -> range of tasks at the maximum parameters
		"annual_budget": {200, 300}, "erp_migration": {200, 300}, "compliance_program": {200, 300},
		"product_launch": {150, 300}, "post_merger_integration": {200, 300}, "year_end_audit": {200, 300},
	}
	for _, tpl := range giantTemplates() {
		t.Run(tpl.Key, func(t *testing.T) {
			if issues := ValidateTemplate(tpl, demoAgents, DefaultLimits()); hasErrors(issues) {
				t.Fatalf("%+v", issues)
			}
			st := statsOf(tpl, "es", DefaultLimits())
			r, ok := want[tpl.Key]
			if !ok {
				t.Fatalf("unexpected template %s", tpl.Key)
			}
			if st.TasksMax < r[0] || st.TasksMax > r[1] || st.Gates < 1 || st.Tasks < 30 || st.Tasks >= st.TasksMax {
				t.Fatalf("stats %+v, want max in %v", st, r)
			}
			t.Logf("%s: default %d tasks, max %d, gates %d", tpl.Key, st.Tasks, st.TasksMax, st.Gates)
			if tpl.Category == "" && categoryOfBuiltin(tpl.Key) == "general" {
				t.Fatalf("%s has no category", tpl.Key)
			}
		})
	}
}

func TestGiantTextsExistInBothLanguages(t *testing.T) {
	for _, loc := range []string{"es", "en"} {
		for _, tpl := range giantTemplates() {
			check := func(key string) {
				if key != "" {
					if _, ok := i18n[loc][key]; !ok {
						t.Errorf("%s: missing %s text %q", tpl.Key, loc, key)
					}
				}
			}
			check(tpl.NameKey)
			check(tpl.DescriptionKey)
			for _, p := range tpl.Params {
				check(p.LabelKey)
				check(p.DefaultKey)
			}
			for _, o := range tpl.Objectives {
				check(o.TitleKey)
				for _, w := range o.Workflows {
					check(w.TitleKey)
					for _, n := range w.Nodes {
						check(n.TitleKey)
						if n.Approval != nil {
							check("pv.action." + n.Approval.Action)
						}
					}
				}
			}
		}
	}
}

// The frontend catalogs get these texts from giant_i18n.json (frontend/scripts/merge-giant-i18n.mjs).
// UPDATE_GIANT_I18N=1 go test ./internal/projects -run TestGiantI18NFile rewrites it.
func TestGiantI18NFile(t *testing.T) {
	got := giantI18N()
	keys := func(m map[string]string) []string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	if !reflect.DeepEqual(keys(got["es"]), keys(got["en"])) {
		t.Fatal("es and en do not have the same keys")
	}
	b, _ := json.MarshalIndent(got, "", "  ")
	b = append(b, '\n')
	if os.Getenv("UPDATE_GIANT_I18N") == "1" {
		if err := os.WriteFile("giant_i18n.json", b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	cur, err := os.ReadFile("giant_i18n.json")
	if err != nil || string(cur) != string(b) {
		t.Fatal("giant_i18n.json is out of date: run UPDATE_GIANT_I18N=1 go test ./internal/projects -run TestGiantI18NFile")
	}
}
