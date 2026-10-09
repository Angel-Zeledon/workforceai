package projects

import (
	"testing"

	"aiworkforce/backend/internal/application"
)

func TestCleanPhases(t *testing.T) {
	in := []application.PlanPhase{
		{Key: "alpha", Title: "A", Size: "xl", DependsOn: []string{"ghost", "alpha"}},
		{Key: "beta", Title: "B", Size: "huge", DependsOn: []string{"alpha"}},
		{Key: "alpha", Title: "dup"},
		{Key: "gamma", Title: " "},
	}
	out, err := cleanPhases(in, 8)
	if err != nil || len(out) != 2 {
		t.Fatalf("%v %+v", err, out)
	}
	if out[0].Key != "p1" || out[0].Size != "XL" || len(out[0].DependsOn) != 0 || out[1].Size != "M" || out[1].DependsOn[0] != "p1" {
		t.Fatalf("%+v", out)
	}
	cyc := []application.PlanPhase{{Key: "a", Title: "A", DependsOn: []string{"b"}}, {Key: "b", Title: "B", DependsOn: []string{"a"}}}
	if _, err := cleanPhases(cyc, 8); err == nil {
		t.Fatal("a phase cycle must be rejected")
	}
	if _, err := cleanPhases(nil, 8); err == nil {
		t.Fatal("no phases must be rejected")
	}
}

func TestCleanPhaseTasks(t *testing.T) {
	known := map[string]bool{"sales": true, "assistant": true}
	out, err := cleanPhaseTasks([]application.PhaseTask{
		{Key: "x", Title: "X", AgentID: "ghost", DependsOn: []string{"y"}, Complexity: "xl"},
		{Key: "y", Title: "Y", AgentID: "sales", DependsOn: []string{"x"}, Complexity: "??"},
		{Key: "z", Title: "Z", AgentID: "sales", DependsOn: []string{"y", "nope"}},
	}, known, "assistant", 10)
	if err != nil || len(out) != 3 {
		t.Fatalf("%v %+v", err, out)
	}
	if out[0].AgentID != "assistant" || out[0].Complexity != "XL" || out[1].Complexity != "M" {
		t.Fatalf("%+v", out)
	}
	// the x<->y cycle is broken by keeping only dependencies on earlier tasks
	if len(out[0].DependsOn) != 0 || len(out[1].DependsOn) != 1 || out[1].DependsOn[0] != "t1" || len(out[2].DependsOn) != 1 {
		t.Fatalf("cycle not broken: %+v", out)
	}
	if capped, _ := cleanPhaseTasks(out, known, "assistant", 2); len(capped) != 2 {
		t.Fatal("the cap must apply")
	}
}
