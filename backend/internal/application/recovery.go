package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// W2 failure recovery and approval expiry (docs/plans/large-workflows.md).
//
//   - Task-level retry: a task whose runtime call failed transiently is run
//     again (TaskMaxAttempts attempts in total, exponential backoff). The
//     retry only repeats the runtime call, which has no side effects; tool
//     requests are handled once, after the call that succeeded, and an
//     approved action runs at most once per approval id (execution ledger).
//   - Manual recovery (RetryTask / SkipTask): a human re-queues a failed or
//     blocked task, or skips a failed one so its dependents proceed.
//   - Project waits: approvals and budget pauses of a project never expire;
//     they remind instead.

// ErrNonRetryable marks a failure that retrying cannot fix (e.g. the runtime
// rejected the request itself). The runtime client wraps 4xx statuses with it.
var ErrNonRetryable = errors.New("non-retryable")

// retryableTaskErr: a transient runtime failure (error, timeout, invalid
// structured output) is retried; budget caps, cancellation and permanent
// request errors are not.
func retryableTaskErr(err error) bool {
	return err != nil && !errors.Is(err, errBudget) && !errors.Is(err, ErrNonRetryable) &&
		!errors.Is(err, context.Canceled)
}

// projectOwned reports whether the request belongs to a project. Only a
// positive answer is cached (a project registers right after its request).
func (o *Orchestrator) projectOwned(ctx context.Context, rs *run) bool {
	if rs.projOwned.Load() {
		return true
	}
	if own := o.durable.owner; own != nil && own.OwnsRequest(ctx, o.org(ctx), rs.req.ID) {
		rs.projOwned.Store(true)
		return true
	}
	return false
}

// awaitApproval waits for the human decision on ap. Interactive requests keep
// the deadline (auto-reject when it passes); project requests wait with
// periodic approval.reminder events (WaitReminding).
func (o *Orchestrator) awaitApproval(ctx context.Context, rs *run, ch <-chan domain.Approval, ap domain.Approval, deadline time.Time) (domain.Approval, error) {
	if !o.projectOwned(ctx, rs) {
		return o.approvals.WaitUntil(ctx, ch, ap.ID, deadline)
	}
	return o.approvals.WaitReminding(ctx, ch, ap, o.cfg.ProjectReminderEvery, o.cfg.ProjectApprovalTimeout)
}

// beginRetry prepares the next attempt of a task after a transient failure:
// it announces the retry, waits the backoff, asks the guards again (kill
// switch, agent pause, paused project) and reserves budget for the new call
// (a cap pauses or stops it exactly like for the first attempt). ok=false
// means the task must stop with out (it is already failed/blocked).
func (o *Orchestrator) beginRetry(ctx context.Context, rs *run, t domain.Task, attempt int, cause error) (res *Reservation, out Outcome, ok bool) {
	maxAtt := max(1, o.cfg.TaskMaxAttempts)
	backoff := o.cfg.TaskRetryBackoff << min(attempt-1, 6)
	o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "task.retry", Entity: "task", EntityID: t.ID,
		Details: map[string]any{"attempt": attempt, "next_attempt": attempt + 1, "max_attempts": maxAtt, "error": truncate(cause.Error(), 300),
			"backoff_ms": backoff.Milliseconds(), "agent_id": t.AgentID}})
	o.rec.Emit(ctx, Action{Type: domain.EvTaskRetrying, AgentID: t.AgentID, Entity: "task", EntityID: t.ID, SkipAudit: true,
		Payload: map[string]any{"task": t, "attempt": attempt + 1, "max_attempts": maxAtt, "error": truncate(cause.Error(), 300), "manual": false},
		Text:    fmt.Sprintf("Reintentando tarea (intento %d/%d): %s", attempt+1, maxAtt, t.Title)})
	tid := t.ID
	o.setState(ctx, t.AgentID, domain.StateWorking, fmt.Sprintf("Reintentando (%d/%d): %s", attempt+1, maxAtt, t.Title), &tid, 10)
	select {
	case <-time.After(backoff):
	case <-ctx.Done():
		return nil, OutcomeFailed, false
	}
	if !o.admitTask(ctx, rs, t) {
		return nil, OutcomeFailed, false
	}
	if out, handled := o.projectGate(ctx, rs, t); handled {
		return nil, out, false
	}
	res, err := o.reserveOrPause(ctx, rs, t.AgentID, t.ID, domain.UsageRunTask)
	if err != nil {
		if ctx.Err() == nil {
			o.failTask(ctx, rs, t, err)
		}
		return nil, OutcomeFailed, false
	}
	return res, OutcomeDone, true
}

// ---- manual recovery ----

// RecoveryResult is the outcome of RetryTask / SkipTask.
type RecoveryResult struct {
	Task domain.Task `json:"task"`
	// Requeued lists the ids of the tasks put back in the queue (the task
	// itself for a retry, plus the dependents that were blocked behind it).
	Requeued []string `json:"requeued"`
}

const maxSkipReason = 500

// RetryTask re-queues a failed (or blocked) task of a request and the tasks
// that were blocked behind it. Human-only; refused while the kill switch, an
// agent pause or read-only mode stop the agent.
func (o *Orchestrator) RetryTask(ctx context.Context, taskID string) (RecoveryResult, error) {
	return o.recoverTask(ctx, taskID, false, "")
}

// SkipTask marks a failed (or blocked) task as skipped by a human: it counts
// as finished so its dependents run, with a note that its input is missing.
func (o *Orchestrator) SkipTask(ctx context.Context, taskID, reason string) (RecoveryResult, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > maxSkipReason {
		return RecoveryResult{}, fmt.Errorf("%w: reason is required (max %d characters)", domain.ErrInvalid, maxSkipReason)
	}
	return o.recoverTask(ctx, taskID, true, reason)
}

func (o *Orchestrator) recoverTask(ctx context.Context, taskID string, skip bool, reason string) (RecoveryResult, error) {
	org := o.org(ctx)
	actor := ActorFrom(ctx, "user")
	action := map[bool]string{false: "task.retried", true: "task.skipped"}[skip]
	t, err := o.store.GetTask(ctx, org, taskID)
	if err != nil {
		return RecoveryResult{}, err
	}
	if isAgentPrincipal(actor, t.AgentID) {
		o.rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: action + "_refused", Entity: "task", EntityID: t.ID, Details: map[string]any{"reason": "agents_cannot_recover"}})
		return RecoveryResult{}, fmt.Errorf("%w: only humans can recover a task", domain.ErrForbidden)
	}
	if t.Status != domain.TaskFailed && t.Status != domain.TaskBlocked {
		return RecoveryResult{}, fmt.Errorf("%w: only a failed or blocked task can be %s (it is %s)", domain.ErrConflict, map[bool]string{false: "retried", true: "skipped"}[skip], t.Status)
	}
	req, err := o.store.GetRequest(ctx, org, t.RequestID)
	if err != nil {
		return RecoveryResult{}, err
	}
	if req.Status == domain.RequestPlanning || req.Status == domain.RequestAwaitingConfirmation {
		return RecoveryResult{}, fmt.Errorf("%w: the request has not started yet", domain.ErrConflict)
	}
	if g := o.conn.guard; g != nil {
		if v := g.Admit(ctx, org, t.AgentID); !v.Allowed {
			return RecoveryResult{}, fmt.Errorf("%w: execution_blocked (%s)", domain.ErrConflict, v.Code)
		}
	}
	all, err := o.store.ListTasksByRequest(ctx, org, req.ID)
	if err != nil {
		return RecoveryResult{}, err
	}
	rs, err := o.rebuildRun(ctx, req)
	if err != nil {
		return RecoveryResult{}, err
	}
	o.applyMeta(ctx, rs)
	if containsID(rs.removed, t.ID) {
		return RecoveryResult{}, fmt.Errorf("%w: the task was removed in the plan review", domain.ErrConflict)
	}
	byID := make(map[string]domain.Task, len(all))
	for _, x := range all {
		byID[x.ID] = x
	}
	for _, d := range t.DependsOn {
		if dt, ok := byID[d]; ok && dt.Status != domain.TaskDone {
			return RecoveryResult{}, fmt.Errorf("%w: dependency %q is %s: recover it first", domain.ErrConflict, dt.Title, dt.Status)
		}
	}
	queue := requeueSet(t, all, rs.removed, skip)

	now := time.Now().UTC()
	prevStatus := req.Status
	if skip {
		t.Status, t.StartedAt, t.FinishedAt = domain.TaskDone, nil, &now
		t.Output = skippedOutput(t, actor, reason)
		if err := o.store.UpdateTask(ctx, org, t); err != nil {
			return RecoveryResult{}, err
		}
		o.dropCheckpoint(ctx, t.ID)
		o.rec.Emit(ctx, Action{Type: domain.EvTaskCompleted, AgentID: t.AgentID, Entity: "task", EntityID: t.ID, SkipAudit: true,
			Payload: map[string]any{"task": t, "skipped": true}, Text: "Tarea omitida por una persona: " + t.Title})
	}
	requeued := make([]domain.Task, 0, len(queue))
	ids := make([]string, 0, len(queue))
	for _, id := range queue {
		q := byID[id]
		q.Status, q.StartedAt, q.FinishedAt, q.Output = domain.TaskPending, nil, nil, nil
		if err := o.store.UpdateTask(ctx, org, q); err != nil {
			return RecoveryResult{}, err
		}
		o.dropCheckpoint(ctx, q.ID)
		requeued = append(requeued, q)
		ids = append(ids, q.ID)
		o.rec.Emit(ctx, Action{Type: domain.EvTaskRetrying, AgentID: q.AgentID, Entity: "task", EntityID: q.ID, SkipAudit: true,
			Payload: map[string]any{"task": q, "attempt": 1, "manual": true}, Text: "Tarea puesta de nuevo en cola: " + q.Title})
	}
	o.rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: action, Entity: "task", EntityID: t.ID, RequestID: req.ID,
		Details: map[string]any{"request_id": req.ID, "title": t.Title, "agent_id": t.AgentID, "reason": reason, "requeued": ids, "previous_request_status": req.Status}})
	for i := range all {
		if all[i].ID == t.ID {
			all[i] = t
		}
	}
	res := RecoveryResult{Task: t, Requeued: ids}
	if len(requeued) == 0 {
		return res, nil
	}
	// A finished request goes back to running: it ends again (report) when the
	// re-queued tasks do; a restart in between resumes it (Recover).
	restore := domain.RequestStatus("")
	if prevStatus == domain.RequestDone || prevStatus == domain.RequestFailed {
		restore = prevStatus
		o.setRequestStatus(ctx, rs, domain.RequestRunning)
	}
	o.startReopen(ctx, rs, requeued, restore)
	return res, nil
}

// applyMeta restores what the run meta persisted (who asked, the conversation,
// "no external actions", the tasks removed in the plan review).
func (o *Orchestrator) applyMeta(ctx context.Context, rs *run) {
	if o.durable.runs == nil {
		return
	}
	meta, err := o.durable.runs.GetRunMeta(ctx, o.org(ctx), rs.req.ID)
	if err != nil {
		return
	}
	rs.requestedBy, rs.removed = meta.RequestedBy, meta.RemovedTaskIDs
	rs.maxParallel = meta.MaxParallel
	if meta.ConversationID != "" {
		rs.convID = meta.ConversationID
	}
	rs.ext.readOnly = meta.ReadOnly
	rs.chat = meta.Chat.link()
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// requeueSet returns the ids to put back in the queue: the task itself (retry)
// and the blocked transitive dependents whose other dependencies are done
// (the ones that still wait for another failed task stay blocked until that
// one is recovered). Tasks removed in the plan review are never re-queued.
func requeueSet(t domain.Task, all []domain.Task, removed []string, skip bool) []string {
	byID := make(map[string]domain.Task, len(all))
	for _, x := range all {
		byID[x.ID] = x
	}
	// transitive dependents of t
	dep := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, x := range all {
			if dep[x.ID] || x.ID == t.ID {
				continue
			}
			for _, d := range x.DependsOn {
				if d == t.ID || dep[d] {
					dep[x.ID], changed = true, true
					break
				}
			}
		}
	}
	in := map[string]bool{}
	var order []string
	if !skip {
		in[t.ID] = true
		order = append(order, t.ID)
	}
	finished := func(id string) bool {
		if id == t.ID {
			return skip || in[id]
		}
		if in[id] {
			return true
		}
		x, ok := byID[id]
		return ok && x.Status == domain.TaskDone
	}
	for changed := true; changed; {
		changed = false
		for _, x := range all { // creation order: stable result
			if !dep[x.ID] || in[x.ID] || x.Status != domain.TaskBlocked || containsID(removed, x.ID) {
				continue
			}
			ok := true
			for _, d := range x.DependsOn {
				if !finished(d) {
					ok = false
					break
				}
			}
			if ok {
				in[x.ID], changed = true, true
				order = append(order, x.ID)
			}
		}
	}
	return order
}

func skippedOutput(t domain.Task, actor, reason string) *domain.StructuredOutput {
	out := &domain.StructuredOutput{
		Summary: fmt.Sprintf("SKIPPED by %s: %s. The output of %q is NOT available; continue without it and state this gap.", actor, reason, t.Title),
		Metrics: map[string]any{"skipped": true, "skip_reason": reason, "skipped_by": actor},
	}
	out.Normalize()
	return out
}

// startReopen runs the re-queued tasks in the background. Several recoveries of
// one request may overlap: the last one to end closes the request (report).
func (o *Orchestrator) startReopen(ctx context.Context, rs *run, tasks []domain.Task, restore domain.RequestStatus) {
	org := o.org(ctx)
	owned := o.projectOwned(ctx, rs)
	o.durable.mu.Lock()
	if o.durable.reopening == nil {
		o.durable.reopening = map[string]int{}
	}
	o.durable.reopening[rs.req.ID]++
	o.durable.mu.Unlock()
	o.mu.Lock()
	defer o.mu.Unlock()
	rctx := WithRequestID(WithOrg(o.base, org), rs.req.ID)
	if owned {
		rctx = WithWorkPriority(rctx, PriorityProject)
	}
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		o.reopenRun(rctx, rs, tasks, restore)
	}()
}

func (o *Orchestrator) reopenRun(ctx context.Context, rs *run, sub []domain.Task, restore domain.RequestStatus) {
	defer o.releaseAgents(ctx, rs)
	org := o.org(ctx)
	if all, err := o.store.ListTasksByRequest(ctx, org, rs.req.ID); err == nil {
		for _, t := range all {
			if t.Status == domain.TaskDone {
				if t.Output == nil {
					t.Output = &domain.StructuredOutput{}
					t.Output.Normalize()
				}
				rs.done[t.ID] = t
			}
			rs.touch(t.AgentID)
		}
	}
	inSub := map[string]bool{}
	byID := make(map[string]domain.Task, len(sub))
	for _, t := range sub {
		inSub[t.ID] = true
		byID[t.ID] = t
	}
	nodes := make([]Node, 0, len(sub))
	for _, t := range sub {
		deps := []string{}
		for _, d := range t.DependsOn {
			if inSub[d] {
				deps = append(deps, d)
			}
		}
		nodes = append(nodes, Node{ID: t.ID, DependsOn: deps})
	}
	outcomes := Scheduler{MaxParallel: o.parallelFor(rs)}.Run(ctx, nodes,
		func(c context.Context, id string) Outcome {
			out := o.runTask(c, rs, byID[id])
			if c.Err() == nil {
				o.dropCheckpoint(c, id)
			}
			return out
		},
		func(id string) { o.skipTask(ctx, rs, byID[id]) })
	if ctx.Err() != nil {
		return // shutdown: the request stays running and Recover resumes it
	}
	o.durable.mu.Lock()
	o.durable.reopening[rs.req.ID]--
	last := o.durable.reopening[rs.req.ID] <= 0
	if last {
		delete(o.durable.reopening, rs.req.ID)
	}
	o.durable.mu.Unlock()
	if !last {
		return // the last recovery of the request writes the report
	}
	for o.isTracked(rs.req.ID) { // the original run is still going: it ends first
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	all, err := o.store.ListTasksByRequest(ctx, org, rs.req.ID)
	if err != nil {
		return
	}
	anyDone := false
	final := make(map[string]Outcome, len(all))
	for _, t := range all {
		switch t.Status {
		case domain.TaskDone:
			final[t.ID] = OutcomeDone
			if out, ok := outcomes[t.ID]; ok && out == OutcomeDone {
				anyDone = true
			}
		case domain.TaskFailed:
			final[t.ID] = OutcomeFailed
		case domain.TaskBlocked:
			final[t.ID] = OutcomeBlocked
		default:
			final[t.ID] = OutcomeSkipped
		}
	}
	if !anyDone && restore != "" {
		o.setRequestStatus(ctx, rs, restore) // nothing new finished: no new report
		return
	}
	o.finish(ctx, rs, all, final)
}

func (o *Orchestrator) isTracked(id string) bool {
	o.durable.mu.Lock()
	defer o.durable.mu.Unlock()
	return o.durable.active[id]
}
