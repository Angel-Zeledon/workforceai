package application

import (
	"context"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Per-organization work queue (A1 step 6). Every runtime call of the
// orchestrator (plan, task, consult, synthesis) takes a slot of its
// organization before it starts and gives it back when it ends, so one
// organization never has more than Config.MaxParallelPerOrg calls in flight
// (MAX_PARALLEL_PER_ORG, default 8). When no slot is free the call waits in a
// priority queue: interactive work (chat, typed requests, templates run by a
// person) goes before project work, which goes before scheduled work. Within a
// priority the requests take turns (G2): the waiting call of the request that
// was served longest ago goes first, and calls of one request keep their
// arrival order. A project that launches while others have a backlog of
// ready tasks therefore starts after one turn per active request, not after
// the whole backlog.
//
// What holds a slot is a runtime call, never a wait for a human: approvals,
// plan reviews, cost confirmations, kill switch, agent pauses and operating
// hours are all checked BEFORE the call (admitTask, reserveOrPause...), so a
// paused or blocked request does not starve the rest of the organization, and
// the queue never lets anything skip those checks. The semaphore lives in the
// process (one backend instance); the other instances keep their own.

// WorkPriority orders queued work of one organization (lower runs first).
type WorkPriority int

const (
	PriorityInteractive WorkPriority = iota
	PriorityProject
	PrioritySchedule
)

func (p WorkPriority) String() string {
	switch p {
	case PriorityProject:
		return "project"
	case PrioritySchedule:
		return "schedule"
	default:
		return "interactive"
	}
}

type priorityKey struct{}

// WithWorkPriority marks the work submitted under ctx (projects and schedules
// set it; anything else is interactive).
func WithWorkPriority(ctx context.Context, p WorkPriority) context.Context {
	return context.WithValue(ctx, priorityKey{}, p)
}

// WorkPriorityFrom returns the priority of ctx (interactive by default).
func WorkPriorityFrom(ctx context.Context) WorkPriority {
	if p, ok := ctx.Value(priorityKey{}).(WorkPriority); ok {
		return p
	}
	return PriorityInteractive
}

// orgQueue is a counting semaphore per organization with a priority queue.
type orgQueue struct {
	mu   sync.Mutex
	orgs map[string]*orgSlots
	seq  uint64
}

type orgSlots struct {
	inUse   int
	waiters []*slotWaiter
	// lastGrant is the grant counter at which each request was last served (round-robin turns).
	lastGrant map[string]uint64
	grants    uint64
}

type slotWaiter struct {
	prio    WorkPriority
	seq     uint64
	key     string // the request the call belongs to ("" for work without one)
	ready   chan struct{}
	granted bool
}

// before orders by arrival (used to count the calls ahead of a new one).
func (w *slotWaiter) before(o *slotWaiter) bool {
	if w.prio != o.prio {
		return w.prio < o.prio
	}
	return w.seq < o.seq
}

// noteGrant records that the request got a slot now (its turn).
func (s *orgSlots) noteGrant(key string) {
	if s.lastGrant == nil || len(s.lastGrant) > 4096 {
		s.lastGrant = map[string]uint64{} // bounded memory: forgetting the turns is harmless
	}
	s.grants++
	s.lastGrant[key] = s.grants
}

// served says which of two waiters goes first: priority, then the request served longest ago, then arrival.
func (s *orgSlots) served(w, o *slotWaiter) bool {
	if w.prio != o.prio {
		return w.prio < o.prio
	}
	if lw, lo := s.lastGrant[w.key], s.lastGrant[o.key]; lw != lo {
		return lw < lo
	}
	return w.seq < o.seq
}

// acquire takes a slot of org (limit <= 0: no cap). When it has to wait it
// calls onQueued once with the number of queued calls ahead of it. It returns
// the release function, or ctx's error if ctx ends while waiting.
func (q *orgQueue) acquire(ctx context.Context, org string, limit int, prio WorkPriority, onQueued func(ahead int)) (func(), error) {
	if limit <= 0 {
		return func() {}, nil
	}
	q.mu.Lock()
	if q.orgs == nil {
		q.orgs = map[string]*orgSlots{}
	}
	s := q.orgs[org]
	if s == nil {
		s = &orgSlots{}
		q.orgs[org] = s
	}
	if s.inUse < limit && len(s.waiters) == 0 {
		s.inUse++
		s.noteGrant(RequestIDFrom(ctx))
		q.mu.Unlock()
		return q.releaser(org, limit), nil
	}
	q.seq++
	w := &slotWaiter{prio: prio, seq: q.seq, key: RequestIDFrom(ctx), ready: make(chan struct{})}
	ahead := 0
	for _, o := range s.waiters {
		if o.before(w) {
			ahead++
		}
	}
	s.waiters = append(s.waiters, w)
	q.mu.Unlock()
	if onQueued != nil {
		onQueued(ahead)
	}
	select {
	case <-w.ready:
		return q.releaser(org, limit), nil
	case <-ctx.Done():
		q.mu.Lock()
		if w.granted {
			q.mu.Unlock()
			q.release(org, limit) // granted while cancelling: hand the slot on
		} else {
			for i, o := range s.waiters {
				if o == w {
					s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
					break
				}
			}
			q.mu.Unlock()
		}
		return nil, ctx.Err()
	}
}

func (q *orgQueue) releaser(org string, limit int) func() {
	var once sync.Once
	return func() { once.Do(func() { q.release(org, limit) }) }
}

// release frees a slot and grants it to the best waiter.
func (q *orgQueue) release(org string, limit int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := q.orgs[org]
	if s == nil {
		return
	}
	s.inUse--
	for s.inUse < limit && len(s.waiters) > 0 {
		best := 0
		for i, w := range s.waiters {
			if s.served(w, s.waiters[best]) {
				best = i
			}
		}
		w := s.waiters[best]
		s.waiters = append(s.waiters[:best], s.waiters[best+1:]...)
		w.granted = true
		s.inUse++
		s.noteGrant(w.key)
		close(w.ready)
	}
	if s.inUse == 0 && len(s.waiters) == 0 {
		delete(q.orgs, org)
	}
}

// stats returns the slots in use and the queued calls of org (tests, metrics).
func (q *orgQueue) stats(org string) (inUse, queued int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if s := q.orgs[org]; s != nil {
		return s.inUse, len(s.waiters)
	}
	return 0, 0
}

// acquireSlot takes a slot of the organization of ctx for one runtime call.
// Waiting is visible: a request.queued event (activity item the first time a
// request queues) and request.dequeued with the time waited.
func (o *Orchestrator) acquireSlot(ctx context.Context, call string) (func(), error) {
	org, prio, reqID := o.org(ctx), WorkPriorityFrom(ctx), RequestIDFrom(ctx)
	var since time.Time
	release, err := o.queue.acquire(ctx, org, o.cfg.MaxParallelPerOrg, prio, func(ahead int) {
		since = time.Now()
		act := Action{Type: domain.EvRequestQueued, Entity: "request", EntityID: reqID,
			Payload: map[string]any{"request_id": reqID, "priority": prio.String(), "call": call, "ahead": ahead,
				"max_parallel_per_org": o.cfg.MaxParallelPerOrg}}
		if reqID != "" && o.firstQueued(reqID) {
			act.Text = "Solicitud en cola: la organización está usando todos sus turnos de trabajo en paralelo"
		}
		o.rec.Emit(ctx, act)
	})
	if err != nil {
		return nil, err
	}
	if !since.IsZero() {
		o.rec.Emit(ctx, Action{Type: domain.EvRequestDequeued, Entity: "request", EntityID: reqID, SkipAudit: true,
			Payload: map[string]any{"request_id": reqID, "priority": prio.String(), "call": call,
				"waited_ms": time.Since(since).Milliseconds()}})
	}
	return release, nil
}

// firstQueued reports whether reqID queues for the first time (bounded memory:
// the set is cleared when it grows large; the worst case is a repeated line).
func (o *Orchestrator) firstQueued(reqID string) bool {
	o.queuedMu.Lock()
	defer o.queuedMu.Unlock()
	if o.queuedSeen == nil || len(o.queuedSeen) > 4096 {
		o.queuedSeen = map[string]bool{}
	}
	if o.queuedSeen[reqID] {
		return false
	}
	o.queuedSeen[reqID] = true
	return true
}
