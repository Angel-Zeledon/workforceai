package orgchart

import (
	"reflect"
	"strings"
	"testing"
)

func TestDetectCycle(t *testing.T) {
	cases := []struct {
		name   string
		parent map[string]string
		want   []string
	}{
		{"empty", map[string]string{}, nil},
		{"roots only", map[string]string{"a": "", "b": ""}, nil},
		{"chain", map[string]string{"a": "b", "b": "c", "c": ""}, nil},
		{"self loop", map[string]string{"a": "a"}, []string{"a", "a"}},
		{"two nodes", map[string]string{"a": "b", "b": "a"}, []string{"a", "b", "a"}},
		{"tail into cycle", map[string]string{"x": "a", "a": "b", "b": "c", "c": "a"}, []string{"a", "b", "c", "a"}},
		{"diamond is not a cycle", map[string]string{"a": "r", "b": "r", "c": "a", "d": "a"}, nil},
		{"unknown parent ends the walk", map[string]string{"a": "ghost"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DetectCycle(c.parent)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func dep(id, name, parent string) Department { return Department{ID: id, Name: name, ParentID: parent} }

func TestValidateDepartments(t *testing.T) {
	ok := []Department{dep("hq", "HQ", ""), dep("fin", "Finance", "hq"), dep("tax", "Tax", "fin")}
	if err := ValidateDepartments(ok); err != nil {
		t.Fatal(err)
	}
	bad := []struct {
		name string
		deps []Department
		want string
	}{
		{"duplicate id", []Department{dep("a", "A", ""), dep("a", "B", "")}, "duplicate department id"},
		{"duplicate name case-insensitive", []Department{dep("a", "Sales", ""), dep("b", " sales ", "")}, "already used"},
		{"missing parent", []Department{dep("a", "A", "zzz")}, "does not exist"},
		{"own parent", []Department{dep("a", "A", "a")}, "own parent"},
		{"cycle", []Department{dep("a", "A", "c"), dep("b", "B", "a"), dep("c", "C", "b")}, "cycle"},
		{"empty name", []Department{dep("a", "  ", "")}, "name is required"},
		{"bad cost center", []Department{{ID: "a", Name: "A", CostCenter: "no spaces!"}}, "cost center"},
		{"bad head", []Department{{ID: "a", Name: "A", Head: Ref{Kind: "robot", ID: "x"}}}, "head"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateDepartments(c.deps)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want error containing %q", err, c.want)
			}
		})
	}
}

func TestValidateDepartmentsDepth(t *testing.T) {
	var deps []Department
	parent := ""
	for i := 0; i < MaxDepth; i++ {
		id := string(rune('a' + i))
		deps = append(deps, dep(id, "D"+id, parent))
		parent = id
	}
	if err := ValidateDepartments(deps); err != nil {
		t.Fatalf("%d levels must be accepted: %v", MaxDepth, err)
	}
	deps = append(deps, dep("zz", "Too deep", parent))
	if err := ValidateDepartments(deps); err == nil || !strings.Contains(err.Error(), "too deep") {
		t.Fatalf("got %v", err)
	}
}

func TestWouldCycleAndDescendants(t *testing.T) {
	deps := []Department{dep("hq", "HQ", ""), dep("fin", "Finance", "hq"), dep("tax", "Tax", "fin"), dep("ops", "Ops", "hq")}
	if !WouldCycle(deps, "hq", "tax") {
		t.Fatal("moving HQ under its own grandchild must cycle")
	}
	if !WouldCycle(deps, "fin", "fin") {
		t.Fatal("self parent must cycle")
	}
	if WouldCycle(deps, "tax", "ops") {
		t.Fatal("moving Tax under Ops is fine")
	}
	d := Descendants(deps, "fin")
	if !d["fin"] || !d["tax"] || d["ops"] || d["hq"] {
		t.Fatalf("descendants of fin: %v", d)
	}
}

func TestReportingCycle(t *testing.T) {
	edges := map[string]string{"agent:a": "agent:b", "agent:b": "person:owner"}
	if c := ReportingCycle(edges); c != nil {
		t.Fatal(c)
	}
	edges["person:owner"] = "agent:a"
	if c := ReportingCycle(edges); c == nil {
		t.Fatal("a person reporting to the agent that reports to them is a cycle")
	}
}
