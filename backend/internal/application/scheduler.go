package application

import (
	"container/heap"
	"context"
	"sync"
)

// Outcome is the result of executing one node.
type Outcome int

const (
	OutcomeDone Outcome = iota
	OutcomeFailed
	OutcomeBlocked
	// OutcomeSkipped is assigned to nodes never executed because a
	// dependency did not finish successfully (or the context was cancelled).
	OutcomeSkipped
)

// Node is a unit of work with dependencies.
type Node struct {
	ID        string
	DependsOn []string
	// Weight is the expected cost of the node, used only to rank ready nodes by
	// the longest remaining downstream path (critical path). <= 0 counts as 1.
	Weight float64
}

// Scheduler runs nodes in parallel respecting dependencies and a concurrency limit.
//
// Ready nodes are launched by priority: the one with the longest remaining
// downstream path first (ties: list order), so the critical path never waits
// behind short leaves. Completion handling is O(dependents): indegree counters
// and a dependents index replace the rescans of the node list.
//
// A running node may give its slot back while it only waits for a human or a
// timer (YieldSlot) and takes one again before it continues, so independent
// branches keep running meanwhile.
type Scheduler struct {
	MaxParallel int
	// Live (optional) lets the caller amend the node set while Run is running
	// (scheduler_dynamic.go).
	Live *LiveHandle
}

type evKind int

const (
	evResult evKind = iota
	evYield
	evResume
)

type event struct {
	kind  evKind
	id    string
	out   Outcome
	grant chan struct{} // evResume: closed when a slot is granted
}

// slotHandle lets the task running under the scheduler release its slot.
type slotHandle struct {
	events chan<- event
	mu     sync.Mutex
	held   bool
}

type slotKey struct{}

// YieldSlot releases the scheduler slot of the task running under ctx while it
// waits for something that is not computation (human decision, timer, budget
// pause). The returned function takes a slot again (blocking until one is
// free) and must be called before the task continues; it is idempotent. Outside
// a scheduler, or when the slot is already released, it does nothing.
func YieldSlot(ctx context.Context) (resume func()) {
	h, _ := ctx.Value(slotKey{}).(*slotHandle)
	if h == nil {
		return func() {}
	}
	h.mu.Lock()
	if !h.held {
		h.mu.Unlock()
		return func() {}
	}
	h.held = false
	h.mu.Unlock()
	h.events <- event{kind: evYield}
	var once sync.Once
	return func() { once.Do(h.reacquire) }
}

func (h *slotHandle) reacquire() {
	grant := make(chan struct{})
	h.events <- event{kind: evResume, grant: grant}
	<-grant
	h.mu.Lock()
	h.held = true
	h.mu.Unlock()
}

// readyItem is a node whose dependencies are all done.
type readyItem struct {
	idx  int
	rank float64
}

type readyHeap []readyItem

func (h readyHeap) Len() int { return len(h) }
func (h readyHeap) Less(i, j int) bool {
	if h[i].rank != h[j].rank {
		return h[i].rank > h[j].rank
	}
	return h[i].idx < h[j].idx
}
func (h readyHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *readyHeap) Push(x any)   { *h = append(*h, x.(readyItem)) }
func (h *readyHeap) Pop() any {
	old := *h
	it := old[len(old)-1]
	*h = old[:len(old)-1]
	return it
}

// Run executes nodes whose dependencies are all Done. Nodes whose dependencies
// ended Failed/Blocked/Skipped (or are unknown/cyclic) are not executed: skip
// is invoked for them and they get OutcomeSkipped. Run returns when every node
// has an outcome.
func (s Scheduler) Run(ctx context.Context, nodes []Node, exec func(ctx context.Context, id string) Outcome, skip func(id string)) map[string]Outcome {
	limit := s.MaxParallel
	if limit < 1 {
		limit = 1
	}
	nodes = append([]Node(nil), nodes...) // Live amendments append to it: never touch the caller's array
	n := len(nodes)
	outcomes := make(map[string]Outcome, n)
	index := make(map[string]int, n)
	uniq := make([]int, 0, n) // first occurrence of each id, in list order
	for i, nd := range nodes {
		if _, dup := index[nd.ID]; !dup {
			index[nd.ID] = i
			uniq = append(uniq, i)
		}
	}
	// Dependents index and indegree (distinct, known dependencies only).
	dependents := make([][]int, n)
	indeg := make([]int, n)
	unknownDep := make([]bool, n)
	for _, i := range uniq {
		seen := map[string]bool{}
		for _, d := range nodes[i].DependsOn {
			if seen[d] {
				continue
			}
			seen[d] = true
			j, ok := index[d]
			if !ok {
				unknownDep[i] = true
				continue
			}
			dependents[j] = append(dependents[j], i)
			indeg[i]++
		}
	}
	rank := downstreamRank(nodes, uniq, dependents, indeg)

	started := make([]bool, n)
	decided := func(i int) bool { _, ok := outcomes[nodes[i].ID]; return ok }
	markSkipped := func(i int) {
		outcomes[nodes[i].ID] = OutcomeSkipped
		if skip != nil {
			skip(nodes[i].ID)
		}
	}
	// skipCascade skips i and everything that (transitively) depends on it.
	skipCascade := func(i int) {
		stack := []int{i}
		for len(stack) > 0 {
			c := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if decided(c) || started[c] {
				continue
			}
			markSkipped(c)
			stack = append(stack, dependents[c]...)
		}
	}

	ready := &readyHeap{}
	for _, i := range uniq {
		if unknownDep[i] {
			skipCascade(i)
		}
	}
	for _, i := range uniq {
		if indeg[i] == 0 && !decided(i) {
			heap.Push(ready, readyItem{i, rank[i]})
		}
	}

	events := make(chan event)
	running, active := 0, 0 // goroutines alive / slots held
	var resumers []chan struct{}
	swept := false
	done := ctx.Done()
	lv := s.Live
	var wakeCh <-chan struct{}
	if lv != nil {
		wakeCh = lv.wake
	}
	st := &schedState{nodes: &nodes, index: index, uniq: &uniq, dependents: &dependents, indeg: &indeg, unknownDep: &unknownDep,
		started: &started, rank: &rank, outcomes: outcomes, ready: ready, decided: decided, skipCascade: skipCascade, markSkipped: markSkipped,
		cancelled: func() bool { return ctx.Err() != nil }}

	for {
		if lv != nil {
			lv.applyPending(st)
		}
		if ctx.Err() != nil {
			if !swept {
				swept = true
				for _, i := range uniq {
					if !decided(i) && !started[i] {
						markSkipped(i)
					}
				}
				*ready = (*ready)[:0]
			}
			// Waiting tasks must be able to wind down: grant regardless of the limit.
			for _, g := range resumers {
				active++
				close(g)
			}
			resumers = nil
		}
		// Tasks that resume go first (already started), then new ready nodes.
		for len(resumers) > 0 && active < limit {
			g := resumers[0]
			resumers = resumers[1:]
			active++
			close(g)
		}
		for ready.Len() > 0 && active < limit && len(resumers) == 0 {
			it := heap.Pop(ready).(readyItem)
			i := it.idx
			if decided(i) || started[i] {
				continue
			}
			started[i] = true
			running++
			active++
			h := &slotHandle{events: events, held: true}
			cctx := context.WithValue(ctx, slotKey{}, h)
			go func(id string) {
				out := exec(cctx, id)
				h.mu.Lock()
				held := h.held
				h.mu.Unlock()
				if !held {
					h.reacquire() // a result always comes from a slot holder
				}
				events <- event{kind: evResult, id: id, out: out}
			}(nodes[i].ID)
		}
		if running == 0 {
			if lv != nil && !lv.closeIfIdle() {
				continue // an amendment arrived: apply it before deciding the run is over
			}
			// Nothing running and nothing launchable: whatever is left is stuck (cycle).
			for _, i := range uniq {
				if !decided(i) {
					markSkipped(i)
				}
			}
			return outcomes
		}
		select {
		case ev := <-events:
			switch ev.kind {
			case evYield:
				active--
			case evResume:
				resumers = append(resumers, ev.grant)
			case evResult:
				running--
				active--
				outcomes[ev.id] = ev.out
				i := index[ev.id]
				for _, d := range dependents[i] {
					if ev.out == OutcomeDone {
						indeg[d]--
						if indeg[d] == 0 && !decided(d) && ctx.Err() == nil {
							heap.Push(ready, readyItem{d, rank[d]})
						}
					} else {
						skipCascade(d)
					}
				}
			}
		case <-wakeCh: // an amendment is pending (applied at the top of the loop)
		case <-done:
			done = nil // handled at the top of the loop
		}
	}
}

// downstreamRank returns, per node, its weight plus the heaviest chain of
// dependents below it (cycle members and their ancestors get what is
// computable; they are skipped at the end anyway).
func downstreamRank(nodes []Node, uniq []int, dependents [][]int, indeg []int) []float64 {
	rank := make([]float64, len(nodes))
	deg := make([]int, len(nodes))
	copy(deg, indeg)
	order := make([]int, 0, len(uniq))
	for _, i := range uniq {
		if deg[i] == 0 {
			order = append(order, i)
		}
	}
	for k := 0; k < len(order); k++ {
		for _, d := range dependents[order[k]] {
			if deg[d]--; deg[d] == 0 {
				order = append(order, d)
			}
		}
	}
	for k := len(order) - 1; k >= 0; k-- {
		i := order[k]
		w := nodes[i].Weight
		if w <= 0 {
			w = 1
		}
		best := 0.0
		for _, d := range dependents[i] {
			if rank[d] > best {
				best = rank[d]
			}
		}
		rank[i] = w + best
	}
	return rank
}

type runParallelKey struct{}

// WithRunParallel sets how many tasks of the request submitted under ctx may
// run at once (projects pass their max_parallel). Values outside 1..64 are
// ignored and the configured MAX_PARALLEL applies. The per-organization queue
// (MAX_PARALLEL_PER_ORG) still bounds the runtime calls in flight.
func WithRunParallel(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, runParallelKey{}, n)
}

// RunParallelFrom returns the override of ctx (0: none).
func RunParallelFrom(ctx context.Context) int {
	if n, ok := ctx.Value(runParallelKey{}).(int); ok && n >= 1 && n <= maxRunParallel {
		return n
	}
	return 0
}

const maxRunParallel = 64

func (o *Orchestrator) parallelFor(rs *run) int {
	if rs.maxParallel > 0 {
		return rs.maxParallel
	}
	return o.cfg.MaxParallel
}
