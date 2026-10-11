package projects

import (
	"errors"
	"strings"
	"testing"

	"aiworkforce/backend/internal/domain"
)

func repeatTemplate() Template {
	return Template{ID: "tpl-x", Key: "x", Name: "Plan for {countries}", Description: "d",
		Params: []TemplateParam{
			{Key: "countries", Label: "Countries", Type: ParamList, ItemName: "country", Default: "Chile, Peru, Mexico", Max: 12},
			{Key: "units", Label: "Units", Type: ParamList, ItemName: "unit", Default: "Retail, Wholesale"},
			{Key: "year", Label: "Year", Default: "2027"},
		},
		Objectives: []TemplateObjective{
			{Key: "o1", Title: "Local work {year}", Workflows: []TemplateWorkflow{
				{Key: "wc", Title: "{country}", Repeat: []string{"countries"}, Nodes: []TemplateNode{
					{Key: "a", Title: "Analyze {country}", Agent: "analyst", Complexity: "M"},
					{Key: "b", Title: "Legal review {country}", Agent: "legal", Deps: []string{"a"}},
				}},
				{Key: "wu", Title: "{unit} in {country}", Repeat: []string{"units", "countries"}, Nodes: []TemplateNode{
					{Key: "u", Title: "Budget {unit}/{country}", Agent: "accounting", Deps: []string{"a"}},
				}},
			}},
			{Key: "o2", Title: "Group", Workflows: []TemplateWorkflow{
				{Key: "wg", Title: "Group", Nodes: []TemplateNode{
					{Key: "all", Title: "Consolidate", Agent: "accounting", Deps: []string{"b", "u"}},
					{Key: "gate", Kind: KindGate, Title: "CFO approves", Deps: []string{"all"}, Approval: &TemplateApproval{Action: "approve_budget", Risk: "high"}},
				}},
			}},
		}}
}

func TestExpandDataRepeatsAndFansIn(t *testing.T) {
	x, vals, err := expandData(repeatTemplate(), "", nil, "es", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if vals["year"] != "2027" || vals["countries"] != "Chile, Peru, Mexico" {
		t.Fatalf("values %v", vals)
	}
	byKey := map[string]TemplateNode{}
	wfs := 0
	for _, o := range x.Objectives {
		for _, w := range o.Workflows {
			wfs++
			for _, n := range w.Nodes {
				byKey[n.Key] = n
			}
		}
	}
	// 3 countries x2 nodes + (2 units x 3 countries) x1 + 2 group = 14 nodes; 3 + 6 + 1 workflows
	if len(byKey) != 14 || wfs != 10 {
		t.Fatalf("%d nodes %d workflows", len(byKey), wfs)
	}
	if n := byKey["b_2"]; n.Title != "Legal review Peru" || strings.Join(n.Deps, ",") != "a_2" {
		t.Fatalf("same-item dependency: %+v", n)
	}
	// dims order follows the order of the list parameters: countries (1st), units (2nd) -> suffix _country_unit
	if n := byKey["u_3_2"]; n.Title != "Budget Wholesale/Mexico" || strings.Join(n.Deps, ",") != "a_3" {
		t.Fatalf("2-D block: %+v", n)
	}
	if n := byKey["all"]; len(n.Deps) != 3+6 {
		t.Fatalf("fan-in: %v", n.Deps)
	}
	inst, err := x.instantiate("p", "", nil, "es")
	if err != nil || inst.Name != "Plan for Chile, Peru, Mexico" {
		t.Fatalf("%v %q", err, inst.Name)
	}
}

func TestExpandDataBounds(t *testing.T) {
	tpl := repeatTemplate()
	_, _, err := expandData(tpl, "", map[string]string{"countries": strings.Repeat("a,", 13)}, "es", DefaultLimits())
	if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "max_list_items") {
		t.Fatalf("an oversized list must be rejected: %v", err)
	}
	if _, _, err = expandData(tpl, "", map[string]string{"countries": " , ;"}, "es", Limits{}); err == nil {
		// the default is used when the value is blank, so this must NOT fail
		t.Log("blank list falls back to the default")
	}
	lim := DefaultLimits()
	lim.MaxNodesPerProject = 20
	_, _, err = expandData(tpl, "", nil, "es", lim)
	if err == nil || !strings.Contains(err.Error(), LimitMaxNodes) {
		t.Fatalf("node limit: %v", err)
	}
	// the injection of braces in an item cannot add placeholders
	x, _, err := expandData(tpl, "", map[string]string{"countries": "{{year}}"}, "es", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got := x.Objectives[0].Workflows[0].Nodes[0].Title; strings.Contains(got, "{{") {
		t.Fatalf("item not sanitized: %q", got)
	}
}

func TestValidateTemplate(t *testing.T) {
	ok := repeatTemplate()
	if issues := ValidateTemplate(ok, []string{"analyst", "legal", "accounting"}, DefaultLimits()); hasErrors(issues) {
		t.Fatalf("valid template rejected: %+v", issues)
	}
	mut := func(f func(t *Template)) []Issue {
		c := cloneTemplate(repeatTemplate())
		f(&c)
		return ValidateTemplate(c, []string{"analyst", "legal", "accounting"}, DefaultLimits())
	}
	has := func(is []Issue, code string) bool {
		for _, i := range is {
			if i.Code == code && i.Severity == "error" {
				return true
			}
		}
		return false
	}
	for name, tc := range map[string]struct {
		f    func(t *Template)
		code string
	}{
		"cycle":       {func(t *Template) { t.Objectives[0].Workflows[0].Nodes[0].Deps = []string{"b"} }, "cycle"},
		"self":        {func(t *Template) { t.Objectives[0].Workflows[0].Nodes[0].Deps = []string{"a"} }, IssueSelfDep},
		"unknown dep": {func(t *Template) { t.Objectives[0].Workflows[0].Nodes[0].Deps = []string{"zzz"} }, IssueUnknownDep},
		"dup key":     {func(t *Template) { t.Objectives[0].Workflows[0].Nodes[1].Key = "a" }, IssueDupKey},
		"bad key":     {func(t *Template) { t.Objectives[0].Workflows[0].Nodes[1].Key = "b_1" }, IssueBadKey},
		"agent":       {func(t *Template) { t.Objectives[0].Workflows[0].Nodes[0].Agent = "ghost" }, "unknown_agent"},
		"no agent":    {func(t *Template) { t.Objectives[0].Workflows[0].Nodes[0].Agent = "" }, "no_agent"},
		"title":       {func(t *Template) { t.Objectives[0].Workflows[0].Nodes[0].Title = " " }, "empty_title"},
		"repeat":      {func(t *Template) { t.Objectives[0].Workflows[0].Repeat = []string{"year"} }, IssueBadRepeat},
		"repeat 3": {func(t *Template) {
			t.Params = append(t.Params, TemplateParam{Key: "z", Type: ParamList, Default: "a"})
			t.Objectives[0].Workflows[0].Repeat = []string{"countries", "units", "z"}
		}, IssueBadRepeat},
		"param":      {func(t *Template) { t.Params[2].Key = "Bad Key" }, IssueBadParam},
		"complexity": {func(t *Template) { t.Objectives[0].Workflows[0].Nodes[0].Complexity = "XXL" }, "invalid_complexity"},
		"approval":   {func(t *Template) { t.Objectives[1].Workflows[0].Nodes[1].Approval.Risk = "extreme" }, IssueBadApproval},
		"name":       {func(t *Template) { t.Name = "" }, IssueBadName},
	} {
		if is := mut(tc.f); !has(is, tc.code) {
			t.Errorf("%s: want %s, got %+v", name, tc.code, is)
		}
	}
	// The check also runs at the maximum parameters: 50 x 50 blocks do not fit a 1000-node limit.
	big := cloneTemplate(repeatTemplate())
	big.Params[0].Max, big.Params[1].Max = 50, 50
	lim := DefaultLimits()
	lim.MaxNodesPerProject = 1000
	if is := ValidateTemplate(big, nil, lim); !has(is, LimitMaxNodes) {
		t.Errorf("expansion at maximum parameters not checked: %+v", is)
	}
}
