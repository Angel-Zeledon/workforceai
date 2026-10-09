package application

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

type dynRun struct {
	h       *LiveHandle
	release chan struct{}
	mu      sync.Mutex
	order   []string
	skipped []string
	out     chan map[string]Outcome
}

// startDyn runs nodes; the node "gate" blocks until release is closed, every
// other node finishes at once (fail: ids in failIDs).
func startDyn(nodes []Node, failIDs ...string) *dynRun {
	d := &dynRun{h: NewLiveHandle(), release: make(chan struct{}), out: make(chan map[string]Outcome, 1)}
	go func() {
		d.out <- Scheduler{MaxParallel: 4, Live: d.h}.Run(context.Background(), nodes, func(_ context.Context, id string) Outcome {
			if id == "gate" {
				<-d.release
			}
			d.mu.Lock()
			d.order = append(d.order, id)
			d.mu.Unlock()
			if slices.Contains(failIDs, id) {
				return OutcomeFailed
			}
			return OutcomeDone
		}, func(id string) {
			d.mu.Lock()
			d.skipped = append(d.skipped, id)
			d.mu.Unlock()
		})
	}()
	return d
}

func (d *dynRun) ran() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.order)
}

func TestSchedulerAddNodesWhileRunning(t *testing.T) {
	d := startDyn([]Node{{ID: "gate"}, {ID: "a"}})
	// n1 depends on the running gate, n2 on n1 and a: deps must be respected.
	if err := d.h.AddNodes(Node{ID: "n1", DependsOn: []string{"gate"}}, Node{ID: "n2", DependsOn: []string{"n1", "a"}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if slices.Contains(d.ran(), "n1") {
		t.Fatalf("n1 ran before its dependency finished: %v", d.ran())
	}
	close(d.release)
	out := <-d.out
	for _, id := range []string{"gate", "a", "n1", "n2"} {
		if out[id] != OutcomeDone {
			t.Fatalf("%s = %v", id, out[id])
		}
	}
	ran := d.ran()
	if slices.Index(ran, "n1") < slices.Index(ran, "gate") || slices.Index(ran, "n2") < slices.Index(ran, "n1") {
		t.Fatalf("order %v", ran)
	}
	if err := d.h.AddNodes(Node{ID: "late"}); !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("after the run ended: %v", err)
	}
}

func TestSchedulerAddNodesRaceSafe(t *testing.T) {
	d := startDyn([]Node{{ID: "gate"}})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('A' + i))
			if err := d.h.AddNodes(Node{ID: id, DependsOn: []string{"gate"}}); err != nil {
				t.Errorf("add %s: %v", id, err)
			}
		}(i)
	}
	wg.Wait()
	close(d.release)
	out := <-d.out
	if len(out) != 21 {
		t.Fatalf("outcomes = %d, want 21", len(out))
	}
	for id, o := range out {
		if o != OutcomeDone {
			t.Fatalf("%s = %v", id, o)
		}
	}
}

func TestSchedulerReplaceFailedNodeRevivesDependents(t *testing.T) {
	d := startDyn([]Node{{ID: "f"}, {ID: "gate"}, {ID: "d1", DependsOn: []string{"f"}}, {ID: "d2", DependsOn: []string{"d1"}}}, "f")
	// f fails: d1 and d2 are skipped. Wait for that, then replace f by r and re-point d1.
	deadline := time.Now().Add(2 * time.Second)
	for {
		d.mu.Lock()
		n := len(d.skipped)
		d.mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dependents were not skipped")
		}
		time.Sleep(5 * time.Millisecond)
	}
	err := d.h.Amend(Amendment{Upsert: []Node{{ID: "r"}, {ID: "d1", DependsOn: []string{"r"}}, {ID: "d2", DependsOn: []string{"d1"}}}})
	if err != nil {
		t.Fatal(err)
	}
	close(d.release)
	out := <-d.out
	if out["r"] != OutcomeDone || out["d1"] != OutcomeDone || out["d2"] != OutcomeDone {
		t.Fatalf("outcomes: %v", out)
	}
	ran := d.ran()
	if slices.Index(ran, "r") > slices.Index(ran, "d1") || slices.Index(ran, "d1") > slices.Index(ran, "d2") {
		t.Fatalf("order %v", ran)
	}
}

func TestSchedulerAmendRefusesStartedNodeAndIsAtomic(t *testing.T) {
	d := startDyn([]Node{{ID: "gate"}, {ID: "p", DependsOn: []string{"gate"}}})
	time.Sleep(30 * time.Millisecond) // gate is running
	err := d.h.Amend(Amendment{Upsert: []Node{{ID: "x"}, {ID: "gate", DependsOn: []string{"x"}}}})
	if !errors.Is(err, ErrNodeStarted) {
		t.Fatalf("want ErrNodeStarted, got %v", err)
	}
	committed := false
	if err := d.h.Amend(Amendment{Upsert: []Node{{ID: "y"}}, Drop: []string{"gate"}, Commit: func() error { committed = true; return nil }}); !errors.Is(err, ErrNodeStarted) || committed {
		t.Fatalf("drop of a running node: err=%v committed=%v", err, committed)
	}
	// Drop a pending node: it never runs and its skip callback fires.
	if err := d.h.Amend(Amendment{Drop: []string{"p"}}); err != nil {
		t.Fatal(err)
	}
	if err := d.h.Amend(Amendment{Commit: func() error { return errors.New("store down") }, Upsert: []Node{{ID: "z"}}}); err == nil {
		t.Fatal("a failing Commit must abort the amendment")
	}
	close(d.release)
	out := <-d.out
	if _, ok := out["x"]; ok {
		t.Fatal("x must not exist: the refused amendment was atomic")
	}
	if _, ok := out["z"]; ok {
		t.Fatal("z must not exist")
	}
	if slices.Contains(d.ran(), "p") || out["p"] != OutcomeSkipped {
		t.Fatalf("p: ran=%v out=%v", d.ran(), out["p"])
	}
}
