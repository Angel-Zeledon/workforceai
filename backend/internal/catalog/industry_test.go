package catalog

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

var industryKeys = []string{"accounting_firm", "ecommerce_store", "law_firm", "marketing_agency", "multinational_group"}

func TestIndustriesLoadAndResolveInAllLocales(t *testing.T) {
	c := Industries()
	if got := c.Keys(); strings.Join(got, ",") != strings.Join(industryKeys, ",") {
		t.Fatalf("keys = %v", got)
	}
	for _, loc := range []string{"es", "en", "pt"} {
		for _, k := range industryKeys {
			v, ok := c.Industry(k, loc)
			if !ok || v.Name == "" || v.Description == "" || v.Disclaimer == "" || v.Locale != loc {
				t.Fatalf("%s/%s: incomplete view %+v", k, loc, v)
			}
			if n := len(v.Agents); n < 6 || n > 10 {
				t.Errorf("%s: %d agents, want 6-10", k, n)
			}
			if n := len(v.Departments); n < 5 || n > 9 {
				t.Errorf("%s: %d departments, want 5-9", k, n)
			}
			if len(v.Projects) != 3 || v.DemoSet != k {
				t.Errorf("%s: projects=%d demo=%q", k, len(v.Projects), v.DemoSet)
			}
			ids := map[string]bool{}
			for _, d := range v.Departments {
				if d.Name == "" || d.CostCenter == "" || ids[d.CostCenter] {
					t.Errorf("%s: bad or duplicate department %+v", k, d)
				}
				ids[d.CostCenter] = true
			}
			agents := map[string]bool{}
			for _, a := range v.Agents {
				if a.Name == "" || a.Title == "" || len(a.Responsibilities) < 4 {
					t.Errorf("%s/%s: incomplete agent %s", k, loc, a.Key)
				}
				for _, r := range a.Responsibilities {
					if r == "" {
						t.Errorf("%s/%s: empty responsibility on %s", k, loc, a.Key)
					}
				}
				if a.BudgetUSD < 15 || a.BudgetUSD > 120 {
					t.Errorf("%s: budget %v of %s out of range", k, a.BudgetUSD, a.Key)
				}
				agents[a.Key] = true
			}
			for _, p := range v.Projects {
				if p.Name == "" || len(p.Steps) < 4 || len(p.Steps) > 8 {
					t.Errorf("%s: project %s has %d steps", k, p.Key, len(p.Steps))
				}
				parallel := false
				seen := map[int]int{}
				for _, s := range p.Steps {
					if !agents[s.Agent] || s.Title == "" || s.Description == "" {
						t.Errorf("%s/%s: bad step %s", k, p.Key, s.Key)
					}
					seen[s.Stage]++
					parallel = parallel || seen[s.Stage] > 1
				}
				if !parallel {
					t.Errorf("%s/%s: no parallel stage", k, p.Key)
				}
			}
			if len(v.Delegation) < 4 || len(v.Delegation) > 7 {
				t.Errorf("%s: %d delegation rules", k, len(v.Delegation))
			}
			var deny, approval bool
			for _, r := range v.Delegation {
				deny = deny || r.Effect == "deny"
				approval = approval || (r.Approval && r.Approver != "")
			}
			if !deny || !approval {
				t.Errorf("%s: needs a deny rule and an approval rule", k)
			}
		}
	}
}

func TestEcommerceCoversWeekendsAndNights(t *testing.T) {
	v, _ := Industries().Industry("ecommerce_store", "en")
	allDay := false
	overnight := false
	for _, a := range v.Agents {
		if a.Schedule.Start == "00:00" && a.Schedule.End == "24:00" && len(a.Schedule.Days) == 7 {
			allDay = true
		}
		if a.Schedule.End < a.Schedule.Start {
			overnight = true
		}
	}
	if !allDay || !overnight {
		t.Fatalf("all-day=%v overnight=%v", allDay, overnight)
	}
}

func TestMultinationalHasThreeSubsidiaries(t *testing.T) {
	v, ok := Industries().Industry("multinational_group", "pt")
	if !ok || v.Subsidiaries != 3 {
		t.Fatalf("subsidiaries = %d", v.Subsidiaries)
	}
	var hq string
	for _, d := range v.Departments {
		if d.Parent == "" {
			hq = d.Key
		}
	}
	subs := map[string]bool{}
	for _, d := range v.Departments {
		if strings.HasPrefix(d.Key, "sub_") {
			if d.Parent != hq {
				t.Errorf("%s parent = %q, want %q", d.Key, d.Parent, hq)
			}
			subs[d.Key] = true
		}
	}
	if len(subs) != 3 {
		t.Fatalf("subsidiary departments = %v", subs)
	}
	tz := map[string]bool{}
	for _, a := range v.Agents {
		if strings.HasPrefix(a.Department, "sub_") {
			tz[a.Schedule.Timezone] = true
		}
	}
	if len(tz) != 3 {
		t.Fatalf("subsidiary timezones = %v", tz)
	}
}

func TestNormalizeIndustryLocale(t *testing.T) {
	for in, want := range map[string]string{"pt-BR": "pt", "PT": "pt", "en_US": "en", "es-MX": "es", "": "es", "fr": "es"} {
		if got := NormalizeIndustryLocale(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

// ---- negative cases: a mutated copy of a valid template must fail validation ----

func mutated(t *testing.T, edit func(m map[string]any)) error {
	t.Helper()
	raw, err := industryFS.ReadFile("data/industries/accounting_firm.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	out, _ := json.Marshal(m)
	_, err = LoadIndustries(fstest.MapFS{"data/industries/accounting_firm.json": {Data: out}})
	return err
}

func list(m map[string]any, k string) []any { return m[k].([]any) }
func obj(v any) map[string]any              { return v.(map[string]any) }

func TestIndustryValidationRejects(t *testing.T) {
	if err := mutated(t, func(map[string]any) {}); err != nil {
		t.Fatalf("unmodified copy must load: %v", err)
	}
	cases := map[string]func(m map[string]any){
		"tool outside the role": func(m map[string]any) { obj(list(m, "agents")[1])["tools"] = []any{"fs", "crm"} },
		"department cycle": func(m map[string]any) {
			d := list(m, "departments")
			obj(d[0])["parent"] = obj(d[1])["key"] // practice -> tax, and tax -> practice
		},
		"reporting cycle": func(m map[string]any) {
			a := list(m, "agents")
			obj(a[0])["reports_to"] = obj(a[1])["key"] // manager -> tax_lead -> manager
		},
		"missing pt key": func(m map[string]any) {
			delete(obj(obj(m["i18n"])["pt"]), "name")
		},
		"autonomy above the role ceiling": func(m map[string]any) { obj(list(m, "agents")[1])["autonomy"] = "autonomous" },
		"unknown timezone": func(m map[string]any) {
			obj(obj(list(m, "agents")[0])["schedule"])["timezone"] = "Mars/Olympus"
		},
		"step dependency cycle": func(m map[string]any) {
			steps := list(obj(list(m, "projects")[0]), "steps")
			obj(steps[0])["depends_on"] = []any{obj(steps[1])["key"]} // step1 depends on step2 which depends on step1
		},
		"unknown role":       func(m map[string]any) { obj(list(m, "agents")[0])["role"] = "wizard" },
		"unsupported locale": func(m map[string]any) { obj(m["i18n"])["fr"] = map[string]any{} },
		"missing escalation": func(m map[string]any) { obj(list(m, "agents")[0])["escalation"] = "" },
	}
	for name, edit := range cases {
		if err := mutated(t, edit); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}
