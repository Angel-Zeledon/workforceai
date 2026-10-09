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

// Ready nodes go by longest remaining downstream path, not by list order.
func TestSchedulerLaunchesCriticalPathFirst(t *testing.T) {
	nodes := []Node{
		{ID: "leaf"}, {ID: "short"}, {ID: "s2", DependsOn: []string{"short"}},
		{ID: "head"}, {ID: "h2", DependsOn: []string{"head"}}, {ID: "h3", DependsOn: []string{"h2"}}, {ID: "h4", DependsOn: []string{"h3"}},
	}
	var mu sync.Mutex
	var first []string
	Scheduler{MaxParallel: 1}.Run(context.Background(), nodes, func(_ context.Context, id string) Outcome {
		mu.Lock()
		first = append(first, id)
		mu.Unlock()
		return OutcomeDone
	}, nil)
	if first[0] != "head" || first[1] != "h2" {
		t.Fatalf("order = %v, want the longest chain first", first)
	}
}

// Weights rank by cost, not by node count.
func TestSchedulerRanksByWeight(t *testing.T) {
	nodes := []Node{{ID: "a", Weight: 1}, {ID: "b", Weight: 50}, {ID: "c", Weight: 2}}
	var first string
	Scheduler{MaxParallel: 1}.Run(context.Background(), nodes, func(_ context.Context, id string) Outcome {
		if first == "" {
			first = id
		}
		return OutcomeDone
	}, nil)
	if first != "b" {
		t.Fatalf("first = %s, want b", first)
	}
}

// A node that yields its slot lets others run; it takes a slot again to finish.
func TestSchedulerYieldSlotFreesParallelism(t *testing.T) {
	nodes := []Node{{ID: "w1"}, {ID: "w2"}, {ID: "x1"}, {ID: "x2"}, {ID: "x3"}}
	release := make(chan struct{})
	var cur, peak atomic.Int32
	var finishedX atomic.Int32
	done := make(chan map[string]Outcome, 1)
	go func() {
		done <- Scheduler{MaxParallel: 2}.Run(context.Background(), nodes, func(ctx context.Context, id string) Outcome {
			if id[0] == 'w' {
				resume := YieldSlot(ctx)
				<-release
				resume()
				return OutcomeDone
			}
			n := cur.Add(1)
			for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
			}
			time.Sleep(5 * time.Millisecond)
			cur.Add(-1)
			finishedX.Add(1)
			return OutcomeDone
		}, nil)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for finishedX.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if finishedX.Load() != 3 {
		t.Fatal("independent nodes starved by waiting ones")
	}
	if peak.Load() > 2 {
		t.Fatalf("peak %d exceeds the limit", peak.Load())
	}
	close(release)
	out := <-done
	for _, id := range []string{"w1", "w2", "x1", "x2", "x3"} {
		if out[id] != OutcomeDone {
			t.Fatalf("%s = %v", id, out[id])
		}
	}
}

// Slots are never over-granted: a resuming node waits while the limit is used.
func TestSchedulerResumeRespectsLimit(t *testing.T) {
	nodes := []Node{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	var cur, peak atomic.Int32
	enter := func() {
		n := cur.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
	}
	Scheduler{MaxParallel: 2}.Run(context.Background(), nodes, func(ctx context.Context, id string) Outcome {
		enter()
		cur.Add(-1)
		resume := YieldSlot(ctx)
		time.Sleep(5 * time.Millisecond)
		resume()
		enter()
		time.Sleep(5 * time.Millisecond)
		cur.Add(-1)
		return OutcomeDone
	}, nil)
	if peak.Load() > 2 {
		t.Fatalf("peak %d exceeds the limit", peak.Load())
	}
}

// Cancelling while nodes wait on a yielded slot lets everything wind down.
func TestSchedulerCancelWhileYielded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	nodes := []Node{{ID: "a"}, {ID: "b"}, {ID: "c", DependsOn: []string{"a"}}}
	out := make(chan map[string]Outcome, 1)
	go func() {
		out <- Scheduler{MaxParallel: 1}.Run(ctx, nodes, func(c context.Context, id string) Outcome {
			resume := YieldSlot(c)
			<-c.Done()
			resume()
			return OutcomeFailed
		}, nil)
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case o := <-out:
		if o["c"] != OutcomeSkipped {
			t.Fatalf("c = %v", o["c"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not wind down")
	}
}

// ~500 nodes (layered DAG) must complete fast: guards the O(N^2) rescans.
func TestSchedulerScaleLayeredDAG(t *testing.T) {
	const layers, width = 50, 10
	var nodes []Node
	for l := 0; l < layers; l++ {
		for w := 0; w < width; w++ {
			n := Node{ID: nodeName(l, w)}
			if l > 0 {
				n.DependsOn = []string{nodeName(l-1, w), nodeName(l-1, (w+1)%width)}
			}
			nodes = append(nodes, n)
		}
	}
	start := time.Now()
	out := Scheduler{MaxParallel: 16}.Run(context.Background(), nodes, func(context.Context, string) Outcome { return OutcomeDone }, nil)
	if len(out) != layers*width {
		t.Fatalf("outcomes = %d", len(out))
	}
	for id, o := range out {
		if o != OutcomeDone {
			t.Fatalf("%s = %v", id, o)
		}
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("500 nodes took %s", d)
	}
}

func nodeName(l, w int) string {
	return "n" + string(rune('A'+l%26)) + string(rune('a'+l/26)) + string(rune('0'+w))
}
