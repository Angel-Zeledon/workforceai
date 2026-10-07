package application

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerRespectsDependencies(t *testing.T) {
	nodes := []Node{
		{ID: "c", DependsOn: []string{"a", "b"}},
		{ID: "a"},
		{ID: "b", DependsOn: []string{"a"}},
	}
	var mu sync.Mutex
	var order []string
	out := Scheduler{MaxParallel: 4}.Run(context.Background(), nodes, func(_ context.Context, id string) Outcome {
		mu.Lock()
		order = append(order, id)
		mu.Unlock()
		return OutcomeDone
	}, nil)
	if want := []string{"a", "b", "c"}; !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for _, id := range []string{"a", "b", "c"} {
		if out[id] != OutcomeDone {
			t.Fatalf("%s outcome = %v", id, out[id])
		}
	}
}

func TestSchedulerRunsIndependentNodesInParallelWithLimit(t *testing.T) {
	nodes := []Node{{ID: "1"}, {ID: "2"}, {ID: "3"}, {ID: "4"}, {ID: "5"}}
	var cur, peak atomic.Int32
	Scheduler{MaxParallel: 3}.Run(context.Background(), nodes, func(_ context.Context, _ string) Outcome {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		cur.Add(-1)
		return OutcomeDone
	}, nil)
	if peak.Load() != 3 {
		t.Fatalf("peak parallelism = %d, want 3", peak.Load())
	}
}

func TestSchedulerSkipsDependantsOfFailedOrBlocked(t *testing.T) {
	nodes := []Node{
		{ID: "fail"},
		{ID: "blocked"},
		{ID: "ok"},
		{ID: "d1", DependsOn: []string{"fail"}},
		{ID: "d2", DependsOn: []string{"d1"}},
		{ID: "d3", DependsOn: []string{"blocked", "ok"}},
		{ID: "d4", DependsOn: []string{"ok"}},
	}
	var mu sync.Mutex
	var skipped []string
	out := Scheduler{MaxParallel: 2}.Run(context.Background(), nodes, func(_ context.Context, id string) Outcome {
		switch id {
		case "fail":
			return OutcomeFailed
		case "blocked":
			return OutcomeBlocked
		}
		return OutcomeDone
	}, func(id string) {
		mu.Lock()
		skipped = append(skipped, id)
		mu.Unlock()
	})
	slices.Sort(skipped)
	if want := []string{"d1", "d2", "d3"}; !slices.Equal(skipped, want) {
		t.Fatalf("skipped = %v, want %v", skipped, want)
	}
	if out["d4"] != OutcomeDone || out["fail"] != OutcomeFailed || out["blocked"] != OutcomeBlocked {
		t.Fatalf("unexpected outcomes: %v", out)
	}
}

func TestSchedulerCycleAndUnknownDependencyDoNotHang(t *testing.T) {
	nodes := []Node{{ID: "x", DependsOn: []string{"y"}}, {ID: "y", DependsOn: []string{"x"}}, {ID: "z", DependsOn: []string{"nope"}}}
	done := make(chan map[string]Outcome, 1)
	go func() {
		done <- Scheduler{MaxParallel: 2}.Run(context.Background(), nodes, func(context.Context, string) Outcome { return OutcomeDone }, nil)
	}()
	select {
	case out := <-done:
		for id, o := range out {
			if o != OutcomeSkipped {
				t.Fatalf("%s = %v, want skipped", id, o)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler hung")
	}
}

func TestSchedulerContextCancelSkipsPending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	nodes := []Node{{ID: "a"}, {ID: "b", DependsOn: []string{"a"}}}
	out := Scheduler{MaxParallel: 1}.Run(ctx, nodes, func(context.Context, string) Outcome {
		cancel()
		return OutcomeDone
	}, nil)
	if out["a"] != OutcomeDone || out["b"] != OutcomeSkipped {
		t.Fatalf("outcomes = %v", out)
	}
}
