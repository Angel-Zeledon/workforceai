package application

import (
	"context"
	"sort"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Durable holds (docs/architecture/organization.md, schedule). When a TaskGate holds a task until a known
// moment (GateDecision.Until: the next opening of the working schedule of its agent), the orchestrator
//   - sleeps until then on a timer instead of asking the gate every 100 ms (a weekend is not 4 million
//     lookups), and wakes early when WakeHeld is called (a schedule was edited);
//   - writes the hold to a HoldStore (migration 760), so the wake-up survives a restart: Recover brings the
//     request back, the gate holds it again until the same opening, and WakeDueHolds (a watcher) resumes the
//     requests whose opening passed while nobody was running them.
// Holds without Until (a paused project) keep the short poll as before.

// Hold is one task waiting for a known moment.
type Hold struct {
	TaskID    string    `json:"task_id"`
	RequestID string    `json:"request_id"`
	AgentID   string    `json:"agent_id"`
	Reason    string    `json:"reason"`
	WakeAt    time.Time `json:"wake_at"`
	CreatedAt time.Time `json:"created_at"`
}

// HoldStore persists the holds of an organization. A store error never blocks work: the in-memory wait
// still happens.
type HoldStore interface {
	PutHold(ctx context.Context, org string, h Hold) error
	DeleteHold(ctx context.Context, org, taskID string) error
	ListHolds(ctx context.Context, org string) ([]Hold, error)
}

// MemoryHolds is the in-memory HoldStore (tests, and the open demo without a database).
type MemoryHolds struct {
	mu sync.Mutex
	m  map[string]map[string]Hold
}

func (s *MemoryHolds) PutHold(_ context.Context, org string, h Hold) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]map[string]Hold{}
	}
	if s.m[org] == nil {
		s.m[org] = map[string]Hold{}
	}
	if old, ok := s.m[org][h.TaskID]; ok {
		h.CreatedAt = old.CreatedAt
	}
	s.m[org][h.TaskID] = h
	return nil
}

func (s *MemoryHolds) DeleteHold(_ context.Context, org, taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m[org], taskID)
	return nil
}

func (s *MemoryHolds) ListHolds(_ context.Context, org string) ([]Hold, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Hold, 0, len(s.m[org]))
	for _, h := range s.m[org] {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WakeAt.Before(out[j].WakeAt) })
	return out, nil
}

type holdState struct {
	mu    sync.Mutex
	store HoldStore
	wake  chan struct{}
}

// SetHoldStore installs the durable store of holds (nil: holds are only in memory).
func (o *Orchestrator) SetHoldStore(s HoldStore) { o.hold.mu.Lock(); o.hold.store = s; o.hold.mu.Unlock() }

func (o *Orchestrator) holdStore() HoldStore {
	o.hold.mu.Lock()
	defer o.hold.mu.Unlock()
	return o.hold.store
}

func (o *Orchestrator) holdWakeChan() <-chan struct{} {
	o.hold.mu.Lock()
	defer o.hold.mu.Unlock()
	if o.hold.wake == nil {
		o.hold.wake = make(chan struct{})
	}
	return o.hold.wake
}

// WakeHeld makes every held task ask its gate again now (a schedule or a rule changed).
func (o *Orchestrator) WakeHeld() {
	o.hold.mu.Lock()
	defer o.hold.mu.Unlock()
	if o.hold.wake != nil {
		close(o.hold.wake)
	}
	o.hold.wake = make(chan struct{})
}

// maxHoldSleep bounds one sleep: the gate is asked again at least this often, so a clock jump or a change
// made without WakeHeld is noticed.
const maxHoldSleep = 5 * time.Minute

// holdSleep waits for the next time the gate must be asked. It returns false when ctx ended.
func (o *Orchestrator) holdSleep(ctx context.Context, wake <-chan struct{}, d GateDecision) bool {
	wait := gatePoll
	if !d.Until.IsZero() {
		wait = min(max(time.Until(d.Until), gatePoll), maxHoldSleep)
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-wake:
	case <-t.C:
	}
	return true
}

// holdRecord stores (or refreshes) the durable hold of a task and returns the function that removes it.
func (o *Orchestrator) holdRecord(ctx context.Context, t domain.Task, d GateDecision) {
	st := o.holdStore()
	if st == nil || d.Until.IsZero() {
		return
	}
	h := Hold{TaskID: t.ID, RequestID: t.RequestID, AgentID: t.AgentID, Reason: d.Reason, WakeAt: d.Until.UTC(), CreatedAt: time.Now().UTC()}
	if err := st.PutHold(context.WithoutCancel(ctx), o.org(ctx), h); err != nil {
		o.log.Warn("put hold", "task", t.ID, "err", err)
	}
}

func (o *Orchestrator) holdRelease(ctx context.Context, taskID string) {
	if st := o.holdStore(); st != nil {
		if err := st.DeleteHold(context.WithoutCancel(ctx), o.org(ctx), taskID); err != nil {
			o.log.Warn("delete hold", "task", taskID, "err", err)
		}
	}
}

// Holds lists the durable holds of the organization of ctx (what is waiting for a working window).
func (o *Orchestrator) Holds(ctx context.Context) []Hold {
	st := o.holdStore()
	if st == nil {
		return []Hold{}
	}
	hs, err := st.ListHolds(ctx, o.org(ctx))
	if err != nil || hs == nil {
		return []Hold{}
	}
	return hs
}

// WakeDueHolds resumes the requests that have a hold whose wake-up moment passed and that no goroutine of
// this process is running (the process restarted across the opening, or an instance lost the request). A
// hold whose task is no longer pending is dropped. It returns how many requests were resumed.
func (o *Orchestrator) WakeDueHolds(ctx context.Context, orgs []string) int {
	st := o.holdStore()
	if st == nil {
		return 0
	}
	now := time.Now().UTC()
	resumed := 0
	for _, org := range WithDefaultOrg(orgs, o.cfg.OrgID) {
		octx := WithOrg(ctx, org)
		holds, err := st.ListHolds(octx, org)
		if err != nil {
			continue
		}
		for _, h := range holds {
			if h.WakeAt.After(now) {
				continue
			}
			t, err := o.store.GetTask(octx, org, h.TaskID)
			if err != nil || t.Status != domain.TaskPending {
				_ = st.DeleteHold(octx, org, h.TaskID)
				continue
			}
			req, err := o.store.GetRequest(octx, org, h.RequestID)
			if err != nil || !resumableStatus(req.Status) {
				_ = st.DeleteHold(octx, org, h.TaskID)
				continue
			}
			if !o.track(req.ID) {
				continue // a goroutine of this process runs it and will wake on its own timer
			}
			if o.recoverRequest(octx, req) {
				resumed++
			} else {
				o.untrack(req.ID)
			}
		}
	}
	return resumed
}

// StartHoldWatcher runs WakeDueHolds every `every` (default 30 s) until ctx ends. orgs lists the
// organizations to look at on each pass.
func (o *Orchestrator) StartHoldWatcher(ctx context.Context, every time.Duration, orgs func() []string) {
	if every <= 0 {
		every = 30 * time.Second
	}
	go func() {
		tk := time.NewTicker(every)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				var ids []string
				if orgs != nil {
					ids = orgs()
				}
				o.WakeDueHolds(ctx, ids)
			}
		}
	}()
}
