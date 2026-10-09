package application

import (
	"container/heap"
	"errors"
	"fmt"
	"sync"
)

// Dynamic scheduling (Q2, docs/plans/large-workflows.md "Q2 replanning"): a
// running Scheduler accepts new nodes, re-points the dependencies of nodes that
// have not started and drops nodes that must never run. The amendment is
// applied by the scheduler's own loop (so the indegree counters, the
// dependents index and the ready heap keep a single owner) and is atomic: it
// either applies completely or not at all.

// ErrSchedulerClosed means the Run already ended: nothing can be added to it.
var ErrSchedulerClosed = errors.New("scheduler closed")

// ErrNodeStarted means an amendment touched a node that already started.
var ErrNodeStarted = errors.New("node already started")

// Amendment changes the node set of a running Scheduler.
type Amendment struct {
	// Upsert adds new nodes and redefines the dependencies of nodes that have
	// not started. A node that was skipped because a dependency failed is
	// revived (it runs again when its new dependencies finish).
	Upsert []Node
	// Drop lists not-yet-started nodes that must never run (their skip callback
	// is invoked). Callers re-point the dependents of a dropped node themselves.
	Drop []string
	// Commit (optional) runs inside the scheduler loop after the amendment was
	// validated and before it is applied; no node can start in between. An
	// error aborts the amendment.
	Commit func() error
}

type amendReq struct {
	a     Amendment
	reply chan error
}

// LiveHandle connects a caller with the Scheduler.Run it was passed to.
type LiveHandle struct {
	mu      sync.Mutex
	closed  bool
	pending []*amendReq
	wake    chan struct{}
}

// NewLiveHandle returns a handle to pass as Scheduler.Live.
func NewLiveHandle() *LiveHandle { return &LiveHandle{wake: make(chan struct{}, 1)} }

// Amend submits an amendment and waits until the scheduler applied (or
// refused) it. It returns ErrSchedulerClosed when the Run has already ended.
func (h *LiveHandle) Amend(a Amendment) error {
	req := &amendReq{a: a, reply: make(chan error, 1)}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return ErrSchedulerClosed
	}
	h.pending = append(h.pending, req)
	h.mu.Unlock()
	select {
	case h.wake <- struct{}{}:
	default:
	}
	return <-req.reply
}

// AddNodes adds nodes to the running scheduler.
func (h *LiveHandle) AddNodes(nodes ...Node) error { return h.Amend(Amendment{Upsert: nodes}) }

// Closed reports whether the Run already ended.
func (h *LiveHandle) Closed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

// closeIfIdle closes the handle unless an amendment is waiting. Called by Run
// when it has nothing left to do; the check and the close are one step, so an
// amendment is either applied or refused with ErrSchedulerClosed, never lost.
func (h *LiveHandle) closeIfIdle() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.pending) > 0 {
		return false
	}
	h.closed = true
	return true
}

// schedState is the view of Run's state the amendments work on.
type schedState struct {
	nodes       *[]Node
	index       map[string]int
	uniq        *[]int
	dependents  *[][]int
	indeg       *[]int
	unknownDep  *[]bool
	started     *[]bool
	rank        *[]float64
	outcomes    map[string]Outcome
	ready       *readyHeap
	decided     func(i int) bool
	skipCascade func(i int)
	markSkipped func(i int)
	cancelled   func() bool
}

func (h *LiveHandle) applyPending(st *schedState) {
	h.mu.Lock()
	reqs := h.pending
	h.pending = nil
	h.mu.Unlock()
	for _, r := range reqs {
		r.reply <- st.apply(r.a)
	}
}

func (st *schedState) apply(a Amendment) error {
	nodes := *st.nodes
	// Validate first: all or nothing.
	seen := map[string]bool{}
	for _, nd := range a.Upsert {
		if nd.ID == "" || seen[nd.ID] {
			return fmt.Errorf("amendment: invalid or duplicate node %q", nd.ID)
		}
		seen[nd.ID] = true
		if i, ok := st.index[nd.ID]; ok {
			if (*st.started)[i] {
				return fmt.Errorf("%w: %s", ErrNodeStarted, nd.ID)
			}
			if out, dec := st.outcomes[nd.ID]; dec && out != OutcomeSkipped {
				return fmt.Errorf("%w: %s", ErrNodeStarted, nd.ID)
			}
		}
	}
	for _, id := range a.Drop {
		i, ok := st.index[id]
		if !ok {
			return fmt.Errorf("amendment: unknown node %q", id)
		}
		if (*st.started)[i] {
			return fmt.Errorf("%w: %s", ErrNodeStarted, id)
		}
		if out, dec := st.outcomes[id]; dec && out != OutcomeSkipped {
			return fmt.Errorf("%w: %s", ErrNodeStarted, id)
		}
		if seen[id] {
			return fmt.Errorf("amendment: node %q is both upserted and dropped", id)
		}
	}
	if a.Commit != nil {
		if err := a.Commit(); err != nil {
			return err
		}
	}

	for _, id := range a.Drop {
		if i := st.index[id]; !st.decided(i) {
			st.markSkipped(i)
		}
	}
	// Phase 1: register new nodes; detach the old edges and forget the skipped
	// outcome of the revived ones.
	touched := make([]int, 0, len(a.Upsert))
	for _, nd := range a.Upsert {
		i, ok := st.index[nd.ID]
		if !ok {
			i = len(nodes)
			nodes = append(nodes, nd)
			*st.nodes = nodes
			st.index[nd.ID] = i
			*st.uniq = append(*st.uniq, i)
			*st.dependents = append(*st.dependents, nil)
			*st.indeg = append(*st.indeg, 0)
			*st.unknownDep = append(*st.unknownDep, false)
			*st.started = append(*st.started, false)
			*st.rank = append(*st.rank, 0)
		} else {
			for _, d := range nodes[i].DependsOn {
				if j, ok := st.index[d]; ok {
					deps := (*st.dependents)[j]
					for k, x := range deps {
						if x == i {
							(*st.dependents)[j] = append(deps[:k:k], deps[k+1:]...)
							break
						}
					}
				}
			}
			delete(st.outcomes, nd.ID)
			nodes[i] = nd
			(*st.indeg)[i] = 0
			(*st.unknownDep)[i] = false
		}
		touched = append(touched, i)
	}
	// Phase 2: edges from the current outcomes.
	for _, i := range touched {
		nd := nodes[i]
		dead := false
		dseen := map[string]bool{}
		for _, d := range nd.DependsOn {
			if dseen[d] {
				continue
			}
			dseen[d] = true
			j, ok := st.index[d]
			if !ok {
				(*st.unknownDep)[i] = true
				dead = true
				continue
			}
			if out, dec := st.outcomes[d]; dec {
				if out != OutcomeDone {
					dead = true
				}
				continue
			}
			(*st.dependents)[j] = append((*st.dependents)[j], i)
			(*st.indeg)[i]++
		}
		w := nd.Weight
		if w <= 0 {
			w = 1
		}
		best := 0.0
		for _, d := range (*st.dependents)[i] {
			if (*st.rank)[d] > best {
				best = (*st.rank)[d]
			}
		}
		(*st.rank)[i] = w + best
		switch {
		case dead:
			st.skipCascade(i)
		case (*st.indeg)[i] == 0 && !st.cancelled():
			heap.Push(st.ready, readyItem{i, (*st.rank)[i]})
		}
	}
	return nil
}
