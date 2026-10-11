package projects

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// Property tests of the template expansion: whatever the (bounded) parameters,
// a template yields a valid plan - acyclic, with known agents, unique ids and
// within the limits - and anything that breaks those properties is rejected by
// the validator before it can be saved.

func randomParams(r *rand.Rand, t Template) map[string]string {
	out := map[string]string{}
	for _, p := range t.Params {
		switch p.kind() {
		case ParamNumber:
			hi := p.Max
			if hi <= 0 {
				hi = 10
			}
			out[p.Key] = strconv.Itoa(p.Min + r.Intn(hi-p.Min+1))
		case ParamList:
			n := 1 + r.Intn(p.maxItems())
			items := make([]string, n)
			for i := range items {
				items[i] = fmt.Sprintf("Item%d-%d", r.Intn(1000), i)
			}
			out[p.Key] = strings.Join(items, ", ")
		default:
			if r.Intn(2) == 0 {
				out[p.Key] = "valor " + strconv.Itoa(r.Intn(100))
			}
		}
	}
	return out
}

// checkPlan asserts the structural properties of an instantiated plan.
func checkPlan(t *testing.T, name string, inst instance, lim Limits, agents []string) {
	t.Helper()
	ids := map[string]NodeDef{}
	for _, n := range inst.Nodes {
		if _, dup := ids[n.ID]; dup {
			t.Fatalf("%s: duplicate node id %s", name, n.ID)
		}
		ids[n.ID] = n
	}
	if err := lim.checkSize(inst.Nodes); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if !computeLevels(append([]NodeDef(nil), inst.Nodes...)) {
		t.Fatalf("%s: the expanded plan has a cycle", name)
	}
	gates := 0
	for _, n := range inst.Nodes {
		for _, d := range n.DependsOn {
			if _, ok := ids[d]; !ok {
				t.Fatalf("%s: %s depends on missing %s", name, n.ID, d)
			}
		}
		if n.isGroup() {
			continue
		}
		if n.Kind == KindGate {
			gates++
			if n.ApprovalAction == "" {
				t.Fatalf("%s: gate %s has no approval action", name, n.ID)
			}
		}
		if strings.TrimSpace(n.Title) == "" || strings.Contains(n.Title, "{") {
			t.Fatalf("%s: node %s has an unresolved title %q", name, n.ID, n.Title)
		}
		if !n.human() && n.Kind != KindSubtask || n.Kind == KindSubtask {
			if n.AgentID == nil || (len(agents) > 0 && !contains(agents, *n.AgentID)) {
				t.Fatalf("%s: node %s has no valid agent", name, n.ID)
			}
		}
	}
	if issues := validatePlan(inst.Nodes, agents); hasErrors(issues) {
		t.Fatalf("%s: plan issues %+v", name, issues)
	}
	_ = gates
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestExpansionOfEveryBuiltinWithRandomParams(t *testing.T) {
	r := rand.New(rand.NewSource(20260711))
	lim := DefaultLimits()
	for _, tpl := range builtinTemplates() {
		for i := 0; i < 40; i++ {
			params := randomParams(r, tpl)
			loc := []string{"es", "en"}[r.Intn(2)]
			inst, err := tpl.instantiate("prj-x", "", params, loc)
			if err != nil {
				t.Fatalf("%s %v: %v", tpl.Key, params, err)
			}
			checkPlan(t, tpl.Key, inst, lim, append(append([]string{}, demoAgents...), "finance_treasury"))
		}
	}
}

// randomTemplate builds a random valid data template: a layered DAG whose
// blocks repeat over zero, one or two of the list parameters.
func randomTemplate(r *rand.Rand) Template {
	t := Template{ID: "tpl-r", Key: "r", Name: "Random {l1}", Params: []TemplateParam{
		{Key: "l1", Type: ParamList, ItemName: "a", Default: "x, y", Max: 3 + r.Intn(5)},
		{Key: "l2", Type: ParamList, ItemName: "b", Default: "p, q, r", Max: 3 + r.Intn(5)},
		{Key: "n", Type: ParamNumber, Min: 1, Max: 9, Default: "3"},
	}}
	lists := []string{"l1", "l2"}
	var prev []string // node keys of the earlier layers
	nodeSeq := 0
	for oi := 0; oi < 1+r.Intn(4); oi++ {
		o := TemplateObjective{Key: "o" + strconv.Itoa(oi), Title: "Phase {n}"}
		for wi := 0; wi < 1+r.Intn(3); wi++ {
			w := TemplateWorkflow{Key: fmt.Sprintf("w%d%d", oi, wi), Title: "Block {a}/{b}"}
			switch r.Intn(4) {
			case 1:
				w.Repeat = []string{lists[r.Intn(2)]}
			case 2:
				w.Repeat = []string{"l1", "l2"}
			}
			w.Title = "Block"
			for _, k := range w.Repeat {
				w.Title += " {" + map[string]string{"l1": "a", "l2": "b"}[k] + "}"
			}
			var local []string
			for ni := 0; ni < 1+r.Intn(5); ni++ {
				nodeSeq++
				n := TemplateNode{Key: "n" + strconv.Itoa(nodeSeq), Title: "Task " + strconv.Itoa(nodeSeq), Agent: demoAgents[r.Intn(len(demoAgents))], Complexity: []string{"S", "M", "L", "XL"}[r.Intn(4)]}
				pool := append(append([]string{}, prev...), local...)
				for _, d := range pool {
					if r.Intn(4) == 0 && len(n.Deps) < 5 {
						n.Deps = append(n.Deps, d)
					}
				}
				if r.Intn(9) == 0 {
					n.Kind, n.Agent, n.Title = KindGate, "", "Approve "+n.Key
					n.Approval = &TemplateApproval{Action: "approve_step", Risk: "medium"}
				}
				local = append(local, n.Key)
				w.Nodes = append(w.Nodes, n)
			}
			prev = append(prev, local...)
			o.Workflows = append(o.Workflows, w)
		}
		t.Objectives = append(t.Objectives, o)
	}
	return t
}

func TestExpansionOfRandomTemplatesIsAlwaysAValidDAG(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	lim := DefaultLimits()
	for i := 0; i < 300; i++ {
		tpl := randomTemplate(r)
		if issues := ValidateTemplate(tpl, demoAgents, lim); hasErrors(issues) {
			t.Fatalf("random template %d rejected: %+v", i, issues)
		}
		tpl.CreatedBy = "u" // an organization template
		for j := 0; j < 5; j++ {
			params := randomParams(r, tpl)
			inst, err := tpl.instantiate("prj-r", "goal", params, "es")
			if err != nil {
				t.Fatalf("template %d %v: %v", i, params, err)
			}
			checkPlan(t, "random", inst, lim, demoAgents)
			// the number of nodes is exactly the sum over blocks of instances x (1 group + nodes)
			vals, lists, _ := resolveParams(tpl, params, "es")
			_ = vals
			want := 0
			for _, o := range tpl.Objectives {
				for _, w := range o.Workflows {
					mult := 1
					for _, k := range w.Repeat {
						mult *= len(lists[k])
					}
					want += mult * (1 + len(w.Nodes))
				}
			}
			if len(inst.Nodes) != want {
				t.Fatalf("template %d: %d nodes, formula says %d", i, len(inst.Nodes), want)
			}
		}
		// a back edge makes the template invalid, and the validator says so
		var first, last *TemplateNode
		for oi := range tpl.Objectives {
			for wi := range tpl.Objectives[oi].Workflows {
				ns := tpl.Objectives[oi].Workflows[wi].Nodes
				for ni := range ns {
					if first == nil {
						first = &ns[ni]
					}
					last = &ns[ni]
				}
			}
		}
		if first != last {
			// make last depend (transitively) on first and first on last
			last.Deps = append(last.Deps, first.Key)
			first.Deps = append(first.Deps, last.Key)
			if is := ValidateTemplate(tpl, demoAgents, lim); !hasErrors(is) {
				t.Fatalf("template %d: a cycle was not detected", i)
			}
		}
	}
}

func TestExpansionNeverExceedsTheLimits(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	lim := DefaultLimits()
	lim.MaxNodesPerProject = 300
	for i := 0; i < 200; i++ {
		tpl := randomTemplate(r)
		tpl.CreatedBy = "u"
		for j := 0; j < 5; j++ {
			inst, err := tpl.instantiateWith(lim, "prj-r", "", randomParams(r, tpl), "es")
			if err != nil {
				if !strings.Contains(err.Error(), LimitMaxNodes) {
					t.Fatalf("unexpected error: %v", err)
				}
				continue
			}
			if len(inst.Nodes) > lim.MaxNodesPerProject {
				t.Fatalf("%d nodes over the limit %d", len(inst.Nodes), lim.MaxNodesPerProject)
			}
		}
	}
}

func TestPreviousItemDependencyChainsTheItems(t *testing.T) {
	tpl := Template{ID: "tpl-c", Key: "c", Name: "Chain", Params: []TemplateParam{{Key: "q", Type: ParamList, ItemName: "quarter", Default: "Q1, Q2, Q3", Max: 8}},
		Objectives: []TemplateObjective{{Key: "o", Title: "O", Workflows: []TemplateWorkflow{
			{Key: "w", Title: "{quarter}", Repeat: []string{"q"}, Nodes: []TemplateNode{{Key: "a", Title: "Close {quarter}", Agent: "accounting", Deps: []string{"a^q"}}}},
		}}}}
	if is := ValidateTemplate(tpl, demoAgents, DefaultLimits()); hasErrors(is) {
		t.Fatalf("%+v", is)
	}
	x, _, err := expandData(tpl, "", nil, "es", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	var deps []string
	for _, w := range x.Objectives[0].Workflows {
		deps = append(deps, strings.Join(w.Nodes[0].Deps, "+"))
	}
	if strings.Join(deps, "|") != "|a_1|a_2" {
		t.Fatalf("chain = %v", deps)
	}
	bad := cloneTemplate(tpl)
	bad.Objectives[0].Workflows[0].Nodes[0].Deps = []string{"a^nope"}
	if is := ValidateTemplate(bad, demoAgents, DefaultLimits()); !hasErrors(is) {
		t.Fatal("a previous-item dependency on a non-repeat dimension must be rejected")
	}
}
