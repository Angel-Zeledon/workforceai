package application

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
)

func doneTask(id, agent, summary string, findingBytes int) domain.Task {
	o := domain.StructuredOutput{Summary: summary, Confidence: 0.8}
	if findingBytes > 0 {
		o.Findings = []string{strings.Repeat("x", findingBytes)}
	}
	o.Normalize()
	fin := time.Unix(1_700_000_000, 0)
	return domain.Task{ID: id, Title: "Task " + id, AgentID: agent, Status: domain.TaskDone, Output: &o, FinishedAt: &fin}
}

func sumTokens(deps []DependencyOutput) int {
	n := 0
	for _, d := range deps {
		n += outputTokens(d.Output)
	}
	return n
}

func TestFitDependenciesSmallIsUntouched(t *testing.T) {
	deps := []domain.Task{doneTask("a", "analyst", "short", 40), doneTask("b", "hr", "other", 40)}
	out, omitted := fitDependencies(deps, "analyst", 8000)
	if omitted != 0 || len(out) != 2 {
		t.Fatalf("omitted=%d len=%d", omitted, len(out))
	}
	for i, d := range out {
		if d.Truncated || !reflect.DeepEqual(d.Output, *deps[i].Output) || d.Ref != "task:"+deps[i].ID {
			t.Errorf("dep %d changed: %+v", i, d)
		}
	}
}

func TestFitDependenciesBudgetWithFanIn100(t *testing.T) {
	const budget = 8000
	var deps []domain.Task
	for i := 0; i < 100; i++ {
		agent := "analyst"
		if i%2 == 0 {
			agent = "hr"
		}
		deps = append(deps, doneTask(fmt.Sprintf("t%03d", i), agent, fmt.Sprintf("Summary of task %d. %s", i, strings.Repeat("z", 600)), 20000))
	}
	out, omitted := fitDependencies(deps, "analyst", budget)
	if got := sumTokens(out); got > budget {
		t.Fatalf("context = %d tokens, budget %d", got, budget)
	}
	if len(out)+omitted != 100 {
		t.Fatalf("len=%d omitted=%d", len(out), omitted)
	}
	full := 0
	for _, d := range out {
		if d.Ref == "" || d.Title == "" {
			t.Fatalf("missing ref/title: %+v", d)
		}
		if !d.Truncated {
			full++
		} else if len(d.Output.Findings) != 0 || d.Output.Summary == "" {
			t.Fatalf("a truncated dep must keep only its summary: %+v", d.Output)
		}
	}
	// 20 KB findings (5000 tokens) cannot fit in full next to 100 summaries.
	if full != 0 {
		t.Errorf("full deps = %d, want 0 (each is larger than the remaining budget)", full)
	}
	again, om2 := fitDependencies(deps, "analyst", budget)
	if !reflect.DeepEqual(out, again) || om2 != omitted {
		t.Error("not deterministic")
	}
}

func TestFitDependenciesRestoresMostRelevantFirst(t *testing.T) {
	// 4 deps of ~700 tokens each, budget 2000: summaries for all, the full text
	// of the same-agent dependency (then higher confidence) first.
	var deps []domain.Task
	for i, agent := range []string{"hr", "analyst", "hr", "analyst"} {
		d := doneTask(fmt.Sprintf("t%d", i), agent, "sum", 2800)
		deps = append(deps, d)
	}
	out, omitted := fitDependencies(deps, "analyst", 2000)
	if omitted != 0 {
		t.Fatal(omitted)
	}
	var fullIDs []string
	for _, d := range out {
		if !d.Truncated {
			fullIDs = append(fullIDs, d.TaskID)
		}
	}
	if !reflect.DeepEqual(fullIDs, []string{"t1", "t3"}) {
		t.Errorf("full = %v, want [t1 t3]", fullIDs)
	}
	if got := sumTokens(out); got > 2000 {
		t.Errorf("tokens = %d", got)
	}
}

func TestFitDependenciesOmitsWhenSummariesDoNotFit(t *testing.T) {
	var deps []domain.Task
	for i := 0; i < 1000; i++ {
		deps = append(deps, doneTask(fmt.Sprintf("t%04d", i), "analyst", strings.Repeat("s", 500), 0))
	}
	out, omitted := fitDependencies(deps, "analyst", 1000)
	if omitted == 0 || sumTokens(out) > 1000 || len(out)+omitted != 1000 {
		t.Fatalf("len=%d omitted=%d tokens=%d", len(out), omitted, sumTokens(out))
	}
}

func TestSummaryFallbackIsDeterministicTruncation(t *testing.T) {
	o := domain.StructuredOutput{Findings: []string{"", strings.Repeat("ñ", 400)}}
	s := summaryOf(o, 100)
	if len(s) > 100 || !strings.HasSuffix(s, "...") {
		t.Fatalf("summary %q (%d bytes)", s, len(s))
	}
	if !strings.HasPrefix(strings.TrimSuffix(s, "..."), "ñ") || s != summaryOf(o, 100) {
		t.Fatal("rune boundary or determinism broken")
	}
}

func TestProjectIndexIsBounded(t *testing.T) {
	var done []domain.Task
	for i := 0; i < 500; i++ {
		d := doneTask(fmt.Sprintf("t%03d", i), "analyst", strings.Repeat("resumen ", 100), 0)
		fin := time.Unix(1_700_000_000+int64(i), 0)
		d.FinishedAt = &fin
		done = append(done, d)
	}
	idx, omitted := buildProjectIndex(done, 1500)
	if len(idx) == 0 || omitted == 0 || len(idx)+omitted != 500 {
		t.Fatalf("len=%d omitted=%d", len(idx), omitted)
	}
	n := 0
	for _, e := range idx {
		n += estimateTextTokens(e.Title+e.Summary+e.AgentID+e.Ref) + 4
		if len(e.Summary) > maxIndexSummaryChars {
			t.Fatalf("summary too long: %d", len(e.Summary))
		}
	}
	if n > 1500 {
		t.Fatalf("index = %d tokens", n)
	}
	if idx[0].TaskID != "t499" { // most recent first
		t.Fatalf("first = %s", idx[0].TaskID)
	}
}

func TestPlanSynthGroups(t *testing.T) {
	mk := func(n, bytes int) ([]SynthOutput, []string) {
		var outs []SynthOutput
		var keys []string
		for i := 0; i < n; i++ {
			outs = append(outs, SynthOutput{TaskID: fmt.Sprint(i), Output: domain.StructuredOutput{Summary: strings.Repeat("a", bytes)}})
			keys = append(keys, fmt.Sprintf("wf%d", i/10))
		}
		return outs, keys
	}
	small, k := mk(3, 400)
	if g := planSynthGroups(small, k, 12000, 6); len(g) != 1 {
		t.Fatalf("small request must be a single pass, got %d groups", len(g))
	}
	big, k := mk(100, 4000) // 100k tokens
	g := planSynthGroups(big, k, 12000, 6)
	if len(g) < 2 || len(g) > 6 {
		t.Fatalf("groups = %d", len(g))
	}
	seen := map[int]bool{}
	for _, grp := range g {
		for _, i := range grp {
			if seen[i] {
				t.Fatalf("output %d in two groups", i)
			}
			seen[i] = true
		}
	}
	if len(seen) != 100 {
		t.Fatalf("covered %d of 100", len(seen))
	}
	// groups follow workflows when they fit: 10 workflows of 10 outputs (10k tokens each)
	wf, wk := mk(40, 4000)
	g = planSynthGroups(wf, wk, 12000, 8)
	for _, grp := range g {
		if len(grp) > 12 {
			t.Fatalf("group of %d exceeds the budget", len(grp))
		}
		for _, i := range grp {
			if wk[i] != wk[grp[0]] {
				t.Fatalf("group mixes workflows %s and %s", wk[i], wk[grp[0]])
			}
		}
	}
	if len(g) != 4 {
		t.Fatalf("one group per workflow expected, got %d", len(g))
	}
}
