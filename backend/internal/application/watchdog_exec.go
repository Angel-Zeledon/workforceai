package application

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Stalled-work watchdog, execution side (docs/architecture/watchdog.md).
//
// Every task the scheduler runs goes through supervise: it registers an execution handle (when it started, whether
// it is waiting for a person or a pause, the cancel of its context) so that
//   - the detection (Stalled) tells a task that is moving or waiting legitimately from one that stopped, and
//   - a recovery (RecoverStalled) never starts a second execution of a live task: it leaves an order on the handle
//     and cancels its context, and the SAME goroutine re-runs the task through runTask (all guards, budget,
//     approvals) or blocks it.
// Nothing here calls the model.

// errStallRecover is the cause of a task context cancelled by a stalled-work recovery.
var errStallRecover = errors.New("stalled work recovery")

// Recovery actions.
const (
	StallRetry    = "retry"
	StallReassign = "reassign"
	StallCancel   = "cancel"
)

// Kinds of stall.
const (
	// StallNoProgress: this process is running the task and it made no progress (no new call, no wait ended).
	StallNoProgress = "no_progress"
	// StallNoWorker: no execution of the task is alive in this process (a restart that did not resume the request,
	// an instance that died): the task stays running or ready and nobody does it.
	StallNoWorker = "no_worker"
)

// recoveringTTL bounds the "being recovered" mark when the new execution never registers (it is then visible again).
const recoveringTTL = 2 * time.Minute

type watchState struct {
	mu         sync.Mutex
	exec       map[string]*taskExec // org|task -> the execution alive in this process
	recovering map[string]time.Time // org|task -> claimed by a recovery, until the new execution registers
	cancelled  map[string]string    // org|task -> who cancelled it (applied when the scheduler reaches it)
	resumedBy  map[string]string    // org|request -> who resumed it (the text of request.resumed)
	// agentOffice answers the department of an agent ("" = General); nil = an organization without departments.
	agentOffice func(ctx context.Context, org, agentID string) (string, error)
}

// taskExec is one execution of a task by this process.
type taskExec struct {
	mu         sync.Mutex
	since      time.Time
	lastActive time.Time // the end of its last legitimate wait (or since)
	waiting    int       // > 0: waiting for a person, a pause or a slot of the organization's queue
	cancel     context.CancelCauseFunc
	cmd        *stallCmd // guarded by watchState.mu
}

type stallCmd struct {
	action  string
	agentID string // reassign: the new agent
	actor   string
	done    chan string // receives how the order was applied
}

func (c *stallCmd) finish(how string) {
	select {
	case c.done <- how:
	default:
	}
}

type execKey struct{}

func watchKey(org, id string) string { return org + "|" + id }

// SetAgentOffice installs the department lookup used to offer and check reassignment targets (D3).
func (o *Orchestrator) SetAgentOffice(fn func(ctx context.Context, org, agentID string) (string, error)) {
	o.watch.mu.Lock()
	o.watch.agentOffice = fn
	o.watch.mu.Unlock()
}

func (w *watchState) begin(ctx context.Context, org, id string) (context.Context, *taskExec) {
	tctx, cancel := context.WithCancelCause(ctx)
	now := time.Now()
	h := &taskExec{since: now, lastActive: now, cancel: cancel}
	w.mu.Lock()
	if w.exec == nil {
		w.exec = map[string]*taskExec{}
	}
	w.exec[watchKey(org, id)] = h
	delete(w.recovering, watchKey(org, id))
	w.mu.Unlock()
	return context.WithValue(tctx, execKey{}, h), h
}

// end unregisters the execution and returns the recovery order it received (nil: none).
func (w *watchState) end(org, id string, h *taskExec) *stallCmd {
	w.mu.Lock()
	if w.exec[watchKey(org, id)] == h {
		delete(w.exec, watchKey(org, id))
	}
	cmd := h.cmd
	w.mu.Unlock()
	h.cancel(nil)
	return cmd
}

func (w *watchState) handle(org, id string) *taskExec {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.exec[watchKey(org, id)]
}

// order leaves a recovery order on a live execution and cancels its context. false: the execution already ended or
// already has an order.
func (w *watchState) order(org, id string, h *taskExec, cmd *stallCmd) bool {
	w.mu.Lock()
	if w.exec[watchKey(org, id)] != h || h.cmd != nil {
		w.mu.Unlock()
		return false
	}
	h.cmd = cmd
	w.mu.Unlock()
	h.cancel(errStallRecover)
	return true
}

// claim marks a task as being recovered (false when it already is).
func (w *watchState) claim(org, id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if at, ok := w.recovering[watchKey(org, id)]; ok && time.Since(at) < recoveringTTL {
		return false
	}
	if w.recovering == nil {
		w.recovering = map[string]time.Time{}
	}
	w.recovering[watchKey(org, id)] = time.Now()
	return true
}

func (w *watchState) release(org, id string) {
	w.mu.Lock()
	delete(w.recovering, watchKey(org, id))
	w.mu.Unlock()
}

func (w *watchState) isRecovering(org, id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	at, ok := w.recovering[watchKey(org, id)]
	return ok && time.Since(at) < recoveringTTL
}

func (w *watchState) markCancelled(org, id, actor string) {
	w.mu.Lock()
	if w.cancelled == nil {
		w.cancelled = map[string]string{}
	}
	w.cancelled[watchKey(org, id)] = actor
	w.mu.Unlock()
}

func (w *watchState) takeCancelled(org, id string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	by, ok := w.cancelled[watchKey(org, id)]
	delete(w.cancelled, watchKey(org, id))
	return by, ok
}

func (w *watchState) setResumedBy(org, reqID, actor string) {
	w.mu.Lock()
	if w.resumedBy == nil {
		w.resumedBy = map[string]string{}
	}
	w.resumedBy[watchKey(org, reqID)] = actor
	w.mu.Unlock()
}

func (w *watchState) takeResumedBy(org, reqID string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	by := w.resumedBy[watchKey(org, reqID)]
	delete(w.resumedBy, watchKey(org, reqID))
	return by
}

// execWait marks the execution of ctx (if any) as waiting legitimately; the returned func ends the wait (idempotent).
func execWait(ctx context.Context) func() {
	h, _ := ctx.Value(execKey{}).(*taskExec)
	if h == nil {
		return func() {}
	}
	h.mu.Lock()
	h.waiting++
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			h.waiting--
			h.lastActive = time.Now()
			h.mu.Unlock()
		})
	}
}

// progress returns the last moment of progress of the execution, or ok=false while it waits legitimately.
func (h *taskExec) progress() (time.Time, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastActive, h.waiting == 0
}

// supervise runs one task of a request under an execution handle and applies the recovery orders it receives:
// retry / reassign re-run it through runTask (the normal path), cancel blocks it. A shutdown wins over an order: the
// request stays as it is and Recover resumes it.
func (o *Orchestrator) supervise(ctx context.Context, rs *run, t domain.Task, first func(context.Context) Outcome) Outcome {
	org := o.org(ctx)
	run := first
	for {
		if by, ok := o.watch.takeCancelled(org, t.ID); ok {
			cur, err := o.store.GetTask(ctx, org, t.ID)
			if err != nil {
				cur = t
			}
			return o.stallBlock(ctx, rs, cur, by)
		}
		tctx, h := o.watch.begin(ctx, org, t.ID)
		out := run(tctx)
		cmd := o.watch.end(org, t.ID, h)
		if cmd == nil {
			return out
		}
		if ctx.Err() != nil {
			cmd.finish("shutdown")
			return out
		}
		cur, err := o.store.GetTask(context.WithoutCancel(ctx), org, t.ID)
		if err != nil {
			cmd.finish("error")
			return out
		}
		if cur.Status == domain.TaskDone { // it finished before the order took effect: nothing to recover
			cmd.finish("finished")
			return out
		}
		if cmd.action == StallCancel {
			res := o.stallBlock(ctx, rs, cur, cmd.actor)
			cmd.finish("applied")
			return res
		}
		cur = o.stallRequeue(ctx, rs, cur, cmd.action, cmd.agentID, cmd.actor)
		t = cur
		run = func(c context.Context) Outcome { return o.runTask(c, rs, cur) }
		cmd.finish("applied")
	}
}

// stallRequeue puts a task back to pending (with its new agent for a reassignment) before it runs again: leftovers
// of the interrupted attempt (a pending tool approval, a checkpoint) are dropped so a human decides on fresh ones.
func (o *Orchestrator) stallRequeue(ctx context.Context, rs *run, t domain.Task, action, agentID, actor string) domain.Task {
	ctx = context.WithoutCancel(ctx)
	org := o.org(ctx)
	o.supersedeApprovals(ctx, t.ID, true)
	o.dropCheckpoint(ctx, t.ID)
	from := t.AgentID
	t.Status, t.StartedAt, t.FinishedAt, t.Output = domain.TaskPending, nil, nil, nil
	if action == StallReassign && agentID != "" {
		t.AgentID, t.AssignedReason = agentID, "Reasignada por "+actor+" (trabajo atascado)"
	}
	if err := o.store.UpdateTask(ctx, org, t); err != nil {
		o.log.Warn("requeue stalled task", "task", t.ID, "err", err)
	}
	if rs != nil {
		rs.touch(t.AgentID)
		for _, lr := range o.liveRuns(t.RequestID) {
			if lr.has(t.ID) {
				lr.put(t)
			}
		}
	}
	if from != t.AgentID {
		o.setState(ctx, from, domain.StateIdle, "Disponible", nil, 0)
		o.rec.Emit(ctx, Action{Type: domain.EvTaskReassigned, AgentID: t.AgentID, Entity: "task", EntityID: t.ID, SkipAudit: true,
			Payload: map[string]any{"task": t, "from": from, "to": t.AgentID, "reason": "stalled"}, Text: "Tarea atascada reasignada: " + t.Title})
		return t
	}
	o.rec.Emit(ctx, Action{Type: domain.EvTaskRetrying, AgentID: t.AgentID, Entity: "task", EntityID: t.ID, SkipAudit: true,
		Payload: map[string]any{"task": t, "attempt": 1, "manual": true, "reason": "stalled"}, Text: "Tarea atascada puesta de nuevo en cola: " + t.Title})
	return t
}

// stallBlock stops a task cancelled by a person: its pending approvals are withdrawn and its open question closed
// (nothing is decided for it), it is blocked with a note and its agent is free again.
func (o *Orchestrator) stallBlock(ctx context.Context, rs *run, t domain.Task, actor string) Outcome {
	ctx = context.WithoutCancel(ctx)
	org := o.org(ctx)
	if ps, err := o.store.ListApprovals(ctx, org, string(domain.ApprovalPending)); err == nil {
		for _, a := range ps {
			if a.TaskID == t.ID {
				if _, err := o.approvals.Withdraw(WithActor(ctx, actor), a.ID, "task cancelled"); err != nil && !errors.Is(err, domain.ErrConflict) {
					o.log.Warn("withdraw approval of cancelled task", "approval", a.ID, "err", err)
				}
			}
		}
	}
	if qs := o.help.questions; qs != nil {
		if q, ok := qs.OpenFor(ctx, t.ID); ok {
			_, _ = qs.Resolve(ctx, q.ID, "system", Resolution{Kind: QuestionCancelled, Text: "task cancelled"})
		}
	}
	o.dropCheckpoint(ctx, t.ID)
	fin := time.Now().UTC()
	t.Status, t.FinishedAt = domain.TaskBlocked, &fin
	t.Output = &domain.StructuredOutput{Summary: "CANCELLED by " + actor + ": the task had stopped making progress. Its output is NOT available.",
		Metrics: map[string]any{"cancelled": true, "cancelled_by": actor, "reason": "stalled"}}
	t.Output.Normalize()
	if err := o.store.UpdateTask(ctx, org, t); err != nil {
		o.log.Warn("block cancelled task", "task", t.ID, "err", err)
	}
	if t.AgentID != assistantID && !o.awaitingOtherTask(ctx, org, t.AgentID, &t.ID) {
		o.setState(ctx, t.AgentID, domain.StateIdle, "Disponible", nil, 0)
	}
	o.rec.Emit(ctx, Action{Type: domain.EvTaskBlocked, AgentID: t.AgentID, Entity: "task", EntityID: t.ID, SkipAudit: true,
		Payload: map[string]any{"task": t, "reason": "cancelled"}, Text: "Tarea cancelada por una persona: " + t.Title})
	o.emitMetrics(ctx)
	return OutcomeBlocked
}

// requestLive reports whether this process is running the request (its run, or a manual recovery of it).
func (o *Orchestrator) requestLive(id string) bool {
	o.durable.mu.Lock()
	defer o.durable.mu.Unlock()
	return o.durable.active[id] || o.durable.reopening[id] > 0
}

// ---- detection ----

// StallPolicy is the threshold of "no progress" (per kind of work) and the clock of a detection.
type StallPolicy struct {
	Task    time.Duration // a task of a normal request
	Project time.Duration // a task of a project
	Now     time.Time
}

// StalledTask is a task that stopped making progress.
type StalledTask struct {
	TaskID    string            `json:"task_id"`
	RequestID string            `json:"request_id"`
	AgentID   string            `json:"agent_id"`
	Title     string            `json:"title"`
	Status    domain.TaskStatus `json:"status"`
	Kind      string            `json:"kind"` // StallNoProgress | StallNoWorker
	Project   bool              `json:"project"`
	// LastProgress is the last moment the task moved; Minutes how long ago (against the policy's clock).
	LastProgress time.Time `json:"last_progress"`
	Minutes      int       `json:"minutes"`
	// Episode identifies this stall (the task and the start of the attempt that stopped): a new attempt that stops
	// again is a new episode.
	Episode string `json:"episode"`
}

func latest(ts ...*time.Time) time.Time {
	var m time.Time
	for _, t := range ts {
		if t != nil && t.After(m) {
			m = *t
		}
	}
	return m
}

// Stalled lists the stalled tasks of the organization of ctx: running tasks without progress beyond the threshold
// and, for a request that no process runs, its first ready task. What waits for an approval, an answer, a spend cap,
// a pause or a slot of the organization's queue is never stalled. A request without a worker contributes one task
// (resuming it covers the others). Plain reads, no model call.
func (o *Orchestrator) Stalled(ctx context.Context, p StallPolicy) ([]StalledTask, error) {
	org := o.org(ctx)
	running, err := o.store.ListTasks(ctx, org, "", string(domain.TaskRunning))
	if err != nil {
		return nil, err
	}
	pending, err := o.store.ListTasks(ctx, org, "", string(domain.TaskPending))
	if err != nil {
		return nil, err
	}
	var reqIDs []string
	for _, t := range append(running, pending...) {
		if !slices.Contains(reqIDs, t.RequestID) {
			reqIDs = append(reqIDs, t.RequestID)
		}
	}
	var out []StalledTask
	var usage map[string]time.Time
	for _, id := range reqIDs {
		cands, err := o.stalledIn(ctx, id, p, &usage)
		if err != nil {
			return nil, err
		}
		if len(cands) > 1 && cands[0].Kind == StallNoWorker && !o.requestLive(id) {
			cands = cands[:1] // resuming the request covers all its tasks
		}
		out = append(out, cands...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastProgress.Before(out[j].LastProgress) })
	return out, nil
}

// stalledIn returns the stalled tasks of one request, the longest stopped first. usage caches the last ledger entry
// per task (loaded once, only when a candidate exists).
func (o *Orchestrator) stalledIn(ctx context.Context, reqID string, p StallPolicy, usage *map[string]time.Time) ([]StalledTask, error) {
	org := o.org(ctx)
	req, err := o.store.GetRequest(ctx, org, reqID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	switch req.Status {
	case domain.RequestPaused, domain.RequestPlanning, domain.RequestAwaitingConfirmation:
		return nil, nil // a spend cap waits for a person; before the start there is no task to run
	}
	tasks, err := o.store.ListTasksByRequest(ctx, org, reqID)
	if err != nil {
		return nil, err
	}
	live := o.requestLive(reqID)
	proj := o.durable.owner != nil && o.durable.owner.OwnsRequest(ctx, org, reqID)
	limit := p.Task
	if proj {
		limit = p.Project
	}
	if limit <= 0 {
		return nil, nil
	}
	byID := make(map[string]domain.Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	var out []StalledTask
	for _, t := range tasks {
		var last time.Time
		kind := StallNoWorker
		switch {
		case t.Status == domain.TaskRunning:
			last = latest(&t.CreatedAt, t.StartedAt)
			if h := o.watch.handle(org, t.ID); h != nil {
				at, active := h.progress()
				if !active {
					continue // waiting for a person, a pause or a queue slot
				}
				if at.After(last) {
					last = at
				}
				kind = StallNoProgress
			}
		case t.Status == domain.TaskPending && !live:
			last = t.CreatedAt
			ready := true
			for _, d := range t.DependsOn {
				dt, ok := byID[d]
				if !ok || dt.Status != domain.TaskDone {
					ready = false
					break
				}
				if f := latest(dt.FinishedAt); f.After(last) {
					last = f
				}
			}
			if !ready {
				continue
			}
		default:
			continue
		}
		if o.watch.isRecovering(org, t.ID) || p.Now.Sub(last) < limit {
			continue
		}
		if *usage == nil {
			*usage = o.lastUsage(ctx, org)
		}
		if u := (*usage)[t.ID]; u.After(last) {
			last = u
		}
		if p.Now.Sub(last) < limit {
			continue
		}
		start := latest(&t.CreatedAt, t.StartedAt)
		out = append(out, StalledTask{TaskID: t.ID, RequestID: reqID, AgentID: t.AgentID, Title: t.Title, Status: t.Status, Kind: kind,
			Project: proj, LastProgress: last, Minutes: int(p.Now.Sub(last) / time.Minute),
			Episode: t.ID + ":" + strconv.FormatInt(start.Unix(), 10)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Status == domain.TaskRunning) != (out[j].Status == domain.TaskRunning) {
			return out[i].Status == domain.TaskRunning
		}
		return out[i].LastProgress.Before(out[j].LastProgress)
	})
	return out, nil
}

// lastUsage is the moment of the last cost-ledger entry of each task (every runtime call that ends writes one).
func (o *Orchestrator) lastUsage(ctx context.Context, org string) map[string]time.Time {
	out := map[string]time.Time{}
	us, err := o.store.ListUsage(ctx, org)
	if err != nil {
		o.log.Warn("watchdog: read the cost ledger", "err", err)
		return out
	}
	for _, u := range us {
		if u.TaskID != "" && u.TS.After(out[u.TaskID]) {
			out[u.TaskID] = u.TS
		}
	}
	return out
}
