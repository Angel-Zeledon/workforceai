package projects

import (
	"testing"

	"aiworkforce/backend/internal/demodata"
)

func TestBuiltinInputDefinitionsAreValidAndTranslated(t *testing.T) {
	n := 0
	for _, tp := range builtinTemplates() {
		if len(tp.Inputs) == 0 {
			continue
		}
		if issues := validateInputSpecs(tp.Inputs, tp.Params); len(issues) > 0 {
			t.Errorf("%s: %+v", tp.Key, issues)
		}
		if tp.DemoSet != "" && !demodata.Has(tp.DemoSet) {
			t.Errorf("%s: unknown demo set %s", tp.Key, tp.DemoSet)
		}
		for _, in := range tp.Inputs {
			n++
			for _, k := range []string{in.LabelKey, in.DescriptionKey} {
				es, en := tr("es", k, nil), tr("en", k, nil)
				if !hasKey(k) || es == "" || en == "" || es == en {
					t.Errorf("%s/%s: %s is not translated (%q / %q)", tp.Key, in.Key, k, es, en)
				}
			}
			if sp := resolveSpec(in, "en"); sp.Label == in.Label {
				t.Errorf("%s/%s: the English label equals the Spanish one", tp.Key, in.Key)
			}
		}
	}
	if n < 25 {
		t.Fatalf("only %d inputs declared", n)
	}
}

// Every country of the sample data is the one the multinational templates use at the same position.
func TestSampleCountriesFollowTheTemplates(t *testing.T) {
	if len(demodata.Countries) != len(countries) {
		t.Fatalf("%d vs %d countries", len(demodata.Countries), len(countries))
	}
	for i, c := range demodata.Countries {
		if c.ES != countries[i][0] || c.EN != countries[i][1] {
			t.Errorf("country %d: %s/%s vs %v", i, c.ES, c.EN, countries[i])
		}
	}
}

func TestParamCountResolvesNumbersAndLists(t *testing.T) {
	gc := buildGroupClose(8, "es")
	if n := gc.paramCount("subsidiaries", nil, "es"); n != 8 {
		t.Fatalf("default: %d", n)
	}
	if n := gc.paramCount("subsidiaries", map[string]string{"subsidiaries": "99"}, "es"); n != 20 {
		t.Fatalf("clamped: %d", n)
	}
	var ab Template
	for _, tp := range builtinTemplates() {
		if tp.Key == "annual_budget" {
			ab = tp
		}
	}
	if n := ab.paramCount("units", nil, "es"); n != 3 {
		t.Fatalf("list default: %d", n)
	}
	if n := ab.paramCount("units", map[string]string{"units": "A, B, ,C, D"}, "es"); n != 4 {
		t.Fatalf("list: %d", n)
	}
	if n := ab.paramCount("ghost", nil, "es"); n != 0 {
		t.Fatalf("unknown: %d", n)
	}
}
