package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Stalled-work watchdog, recovery side (docs/architecture/watchdog.md). A person retries, reassigns or cancels a task
// that stopped making progress. The guarantees:
//   - only people (never an agent) and only while the task is still stalled;
//   - one recovery at a time per task: a lock of the Locker (Redis in production) plus a mark in this process, so two
//     people cannot retry the same thing twice;
//   - never a second execution: a live execution receives the order and re-runs itself (supervise); a task nobody
//     runs is resumed with the same exclusive gate and path as the durable resume (track + recoverRequest), so a
//     Recover in progress and a recovery exclude each other;
//   - retry and reassign go through runTask: budget, approvals, kill switch, pause and read-only apply as always.

// CascadeTask is a dependent that a cancellation also stops.
type CascadeTask struct {
	ID      string            `json:"id"`
	Title   string            `json:"title"`
	AgentID string            `json:"agent_id"`
	Status  domain.TaskStatus `json:"status"`
}

// StallDetail is what a person may do with a task right now.
type StallDetail struct {
	Task    domain.Task  `json:"task"`
	Stalled bool         `json:"stalled"`
	Stall   *StalledTask `json:"stall,omitempty"`
	// Candidates are the agents it can be reassigned to (same department, same role, available).
	Candidates []Candidate `json:"reassign_candidates"`
	// Cascade lists the active dependents a cancellation also stops (the person confirms this exact list).
	Cascade []CascadeTask `json:"cancel_cascade"`
}

// StallRecoverInput is one recovery. ConfirmTaskIDs (cancel) must be exactly the cascade the person was shown.
type StallRecoverInput struct {
	Action         string   `json:"action"`
	AgentID        string   `json:"agent_id,omitempty"`
	ConfirmTaskIDs []string `json:"confirm_task_ids,omitempty"`
}

// StallRecoverResult says what happened. Status: applied (the live execution took the order), requested (the order
// waits for its current step to stop), resumed (the request had no worker and runs again), reopened (a finished
// request was reopened for the task), finished (it ended on its own meanwhile).
type StallRecoverResult struct {
	Task      domain.Task `json:"task"`
	Action    string      `json:"action"`
	Status    string      `json:"status"`
	Cancelled []string    `json:"cancelled,omitempty"`
}

// CascadeChangedError: the dependents a cancellation would stop are not the ones the person confirmed.
type CascadeChangedError struct{ Cascade []CascadeTask }

func (e *CascadeChangedError) Error() string {
	return "the tasks that a cancellation stops changed: confirm the new list"
}
func (e *CascadeChangedError) Unwrap() error { return domain.ErrConflict }

// stallOf returns the stall of a task (ok=false: it is not stalled).
func (o *Orchestrator) stallOf(ctx context.Context, t domain.Task, p StallPolicy) (StalledTask, bool, error) {
	var usage map[string]time.Time
	cands, err := o.stalledIn(ctx, t.RequestID, p, &usage)
	if err != nil {
		return StalledTask{}, false, err
	}
	for _, c := range cands {
		if c.TaskID == t.ID {
			return c, true, nil
		}
	}
	return StalledTask{}, false, nil
}

// cascadeOf lists the transitive dependents of t that are still active (creation order).
func cascadeOf(t domain.Task, all []domain.Task) []CascadeTask {
	dep := map[string]bool{t.ID: true}
	for changed := true; changed; {
		changed = false
		for _, x := range all {
			if dep[x.ID] {
				continue
			}
			for _, d := range x.DependsOn {
				if dep[d] {
					dep[x.ID], changed = true, true
					break
				}
			}
		}
	}
	out := []CascadeTask{}
	for _, x := range all {
		if x.ID == t.ID || !dep[x.ID] {
			continue
		}
		switch x.Status {
		case domain.TaskPending, domain.TaskRunning, domain.TaskAwaitingApproval, domain.TaskNeedsInput:
			out = append(out, CascadeTask{ID: x.ID, Title: x.Title, AgentID: x.AgentID, Status: x.Status})
		}
	}
	return out
}

// StallDetail describes the stall of a task, its reassignment candidates and the cascade of a cancellation.
func (o *Orchestrator) StallDetail(ctx context.Context, taskID string, p StallPolicy) (StallDetail, error) {
	org := o.org(ctx)
	t, err := o.store.GetTask(ctx, org, taskID)
	if err != nil {
		return StallDetail{}, err
	}
	out := StallDetail{Task: t, Candidates: []Candidate{}, Cascade: []CascadeTask{}}
	st, ok, err := o.stallOf(ctx, t, p)
	if err != nil {
		return out, err
	}
	if !ok {
		return out, nil
	}
	out.Stalled, out.Stall = true, &st
	all, err := o.store.ListTasksByRequest(ctx, org, t.RequestID)
	if err != nil {
		return out, err
	}
	out.Cascade = cascadeOf(t, all)
	out.Candidates = o.stallCandidates(ctx, t)
	return out, nil
}

// stallCandidates are the agents a stalled task may be handed to: same role, same department, able to take it.
func (o *Orchestrator) stallCandidates(ctx context.Context, t domain.Task) []Candidate {
	out := []Candidate{}
	agents, err := o.store.ListAgents(ctx, o.org(ctx))
	if err != nil {
		return out
	}
	for _, a := range agents {
		if o.reassignable(ctx, t, a) == nil {
			out = append(out, Candidate{ID: a.ID, Name: a.Name, Role: a.Role, Title: a.Title})
		}
	}
	return out
}

func (o *Orchestrator) officeOf(ctx context.Context, agentID string) (string, error) {
	o.watch.mu.Lock()
	fn := o.watch.agentOffice
	o.watch.mu.Unlock()
	if fn == nil {
		return "", nil
	}
	return fn(ctx, o.org(ctx), agentID)
}

// reassignable checks that a stalled task may be handed to agent a.
func (o *Orchestrator) reassignable(ctx context.Context, t domain.Task, a domain.Agent) error {
	cur, err := o.store.GetAgent(ctx, o.org(ctx), t.AgentID)
	if err != nil {
		return fmt.Errorf("%w: the current agent is unknown", domain.ErrInvalid)
	}
	if !strings.EqualFold(cur.Role, a.Role) {
		return fmt.Errorf("%w: the new agent must have the same role (%s)", domain.ErrInvalid, cur.Role)
	}
	from, err := o.officeOf(ctx, cur.ID)
	if err != nil {
		return err
	}
	to, err := o.officeOf(ctx, a.ID)
	if err != nil {
		return err
	}
	if from != to {
		return fmt.Errorf("%w: the new agent must be of the same department", domain.ErrInvalid)
	}
	return o.canTake(ctx, t, a)
}

// RecoverStalled retries, reassigns or cancels a stalled task (human-only, audited).
func (o *Orchestrator) RecoverStalled(ctx context.Context, taskID string, p StallPolicy, in StallRecoverInput) (StallRecoverResult, error) {
	org := o.org(ctx)
	actor := ActorFrom(ctx, "user")
	t, err := o.store.GetTask(ctx, org, taskID)
	if err != nil {
		return StallRecoverResult{}, err
	}
	switch in.Action {
	case StallRetry, StallReassign, StallCancel:
	default:
		return StallRecoverResult{}, fmt.Errorf("%w: action must be retry, reassign or cancel", domain.ErrInvalid)
	}
	audit := "task.stall_" + map[string]string{StallRetry: "retried", StallReassign: "reassigned", StallCancel: "cancelled"}[in.Action]
	if isAgentPrincipal(actor, t.AgentID) {
		o.rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: audit + "_refused", Entity: "task", EntityID: t.ID, Details: map[string]any{"reason": "agents_cannot_recover"}})
		return StallRecoverResult{}, fmt.Errorf("%w: only people can recover a task", domain.ErrForbidden)
	}
	if _, ok, err := o.stallOf(ctx, t, p); err != nil {
		return StallRecoverResult{}, err
	} else if !ok {
		return StallRecoverResult{}, fmt.Errorf("%w: not_stalled: the task is moving again, waits for a person or is being recovered", domain.ErrConflict)
	}
	req, err := o.store.GetRequest(ctx, org, t.RequestID)
	if err != nil {
		return StallRecoverResult{}, err
	}
	all, err := o.store.ListTasksByRequest(ctx, org, t.RequestID)
	if err != nil {
		return StallRecoverResult{}, err
	}
	cascade := cascadeOf(t, all)
	target := t.AgentID
	switch in.Action {
	case StallCancel:
		if !sameIDs(in.ConfirmTaskIDs, cascade) {
			return StallRecoverResult{}, &CascadeChangedError{Cascade: cascade}
		}
	case StallReassign:
		a, err := o.store.GetAgent(ctx, org, strings.TrimSpace(in.AgentID))
		if err != nil {
			return StallRecoverResult{}, fmt.Errorf("%w: unknown agent", domain.ErrInvalid)
		}
		if err := o.reassignable(ctx, t, a); err != nil {
			return StallRecoverResult{}, err
		}
		if _, err := o.enforceDelegation(ctx, ActorFrom(ctx, ""), o.agentMap(ctx), a.ID, "task", t.ID, true); err != nil {
			return StallRecoverResult{}, err
		}
		target = a.ID
	}
	if in.Action != StallCancel {
		if g := o.conn.guard; g != nil {
			if v := g.Admit(ctx, org, target); !v.Allowed {
				return StallRecoverResult{}, fmt.Errorf("%w: execution_blocked (%s)", domain.ErrConflict, v.Code)
			}
		}
	}
	unlock, ok, err := o.locks.Lock(ctx, "stall:"+org+":"+t.ID, time.Minute)
	if err != nil {
		return StallRecoverResult{}, err
	}
	if !ok {
		return StallRecoverResult{}, fmt.Errorf("%w: already_recovering: someone is recovering this task", domain.ErrConflict)
	}
	defer unlock()
	if !o.watch.claim(org, t.ID) {
		return StallRecoverResult{}, fmt.Errorf("%w: already_recovering: someone is recovering this task", domain.ErrConflict)
	}
	cmd := &stallCmd{action: in.Action, agentID: target, actor: actor, done: make(chan string, 1)}
	var res StallRecoverResult
	if h := o.watch.handle(org, t.ID); h != nil {
		res, err = o.recoverLive(ctx, t, h, cmd, cascade)
	} else if !o.requestLive(req.ID) && o.track(req.ID) {
		res, err = o.recoverOrphan(ctx, req, t, cmd, cascade)
	} else if o.requestLive(req.ID) {
		res, err = o.recoverStray(ctx, t, cmd, cascade)
	} else {
		err = fmt.Errorf("%w: the request is being resumed right now; try again in a moment", domain.ErrConflict)
	}
	if err != nil {
		o.watch.release(org, t.ID)
		return StallRecoverResult{}, err
	}
	if in.Action == StallCancel || res.Status == "finished" {
		o.watch.release(org, t.ID)
	}
	res.Action = in.Action
	o.rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: audit, Entity: "task", EntityID: t.ID, RequestID: t.RequestID,
		Details: map[string]any{"request_id": t.RequestID, "title": t.Title, "from": t.AgentID, "to": target, "status": res.Status, "cancelled": res.Cancelled}})
	return res, nil
}

func sameIDs(confirmed []string, cascade []CascadeTask) bool {
	if len(confirmed) != len(cascade) {
		return false
	}
	for _, c := range cascade {
		if !slices.Contains(confirmed, c.ID) {
			return false
		}
	}
	return true
}

// recoverLive gives the order to the execution of this process and waits (bounded) for it to apply.
func (o *Orchestrator) recoverLive(ctx context.Context, t domain.Task, h *taskExec, cmd *stallCmd, cascade []CascadeTask) (StallRecoverResult, error) {
	org := o.org(ctx)
	if !o.watch.order(org, t.ID, h, cmd) {
		return StallRecoverResult{}, fmt.Errorf("%w: not_stalled: the task moved on meanwhile", domain.ErrConflict)
	}
	res := StallRecoverResult{Status: "requested"}
	if cmd.action == StallCancel {
		for _, c := range cascade { // the dependents stop too: live ones get the same order, the others when reached
			res.Cancelled = append(res.Cancelled, c.ID)
			ch := o.watch.handle(org, c.ID)
			if ch == nil || !o.watch.order(org, c.ID, ch, &stallCmd{action: StallCancel, actor: cmd.actor, done: make(chan string, 1)}) {
				o.watch.markCancelled(org, c.ID, cmd.actor)
			}
		}
	}
	select {
	case how := <-cmd.done:
		res.Status = map[string]string{"applied": "applied", "finished": "finished"}[how]
		if res.Status == "" {
			res.Status = "requested"
		}
	case <-time.After(settleWait):
	case <-ctx.Done():
	}
	if cur, err := o.store.GetTask(context.WithoutCancel(ctx), org, t.ID); err == nil {
		res.Task = cur
	}
	return res, nil
}

// recoverOrphan handles a task whose request no process runs. The caller holds the request (track): the task is
// updated in the store and the request is resumed exactly like after a restart.
func (o *Orchestrator) recoverOrphan(ctx context.Context, req domain.Request, t domain.Task, cmd *stallCmd, cascade []CascadeTask) (StallRecoverResult, error) {
	org := o.org(ctx)
	res := StallRecoverResult{Status: "resumed"}
	switch cmd.action {
	case StallReassign:
		t = o.stallRequeue(ctx, nil, t, StallReassign, cmd.agentID, cmd.actor)
	case StallCancel:
		o.stallBlock(ctx, nil, t, cmd.actor)
		for _, c := range cascade {
			if ct, err := o.store.GetTask(ctx, org, c.ID); err == nil {
				o.stallBlock(ctx, nil, ct, cmd.actor)
				res.Cancelled = append(res.Cancelled, c.ID)
			}
		}
	}
	if !resumableStatus(req.Status) { // a finished request with a stray task: no run to resume
		o.untrack(req.ID)
		if cmd.action == StallCancel {
			res.Status = "applied"
		} else {
			fin := time.Now().UTC()
			t.Status, t.FinishedAt = domain.TaskFailed, &fin
			if err := o.store.UpdateTask(ctx, org, t); err != nil {
				return StallRecoverResult{}, err
			}
			if _, err := o.recoverTask(ctx, t.ID, false, ""); err != nil {
				return StallRecoverResult{}, err
			}
			res.Status = "reopened"
		}
	} else {
		o.watch.setResumedBy(org, req.ID, cmd.actor)
		if !o.recoverRequest(ctx, req) {
			o.untrack(req.ID)
			o.watch.takeResumedBy(org, req.ID)
			return StallRecoverResult{}, fmt.Errorf("%w: the request could not be resumed and was closed with an explanation", domain.ErrConflict)
		}
	}
	if cur, err := o.store.GetTask(ctx, org, t.ID); err == nil {
		res.Task = cur
	}
	return res, nil
}

// recoverStray handles a task left running in a request this process runs although no execution of it is alive
// (an attempt that ended without writing its status): the manual recovery of a failed task re-queues it inside the
// live request; a cancellation blocks it and its dependents (the scheduler skips the ones it reaches).
func (o *Orchestrator) recoverStray(ctx context.Context, t domain.Task, cmd *stallCmd, cascade []CascadeTask) (StallRecoverResult, error) {
	org := o.org(ctx)
	if cmd.action == StallCancel {
		res := StallRecoverResult{Status: "applied"}
		o.stallBlock(ctx, nil, t, cmd.actor)
		for _, c := range cascade {
			ch := o.watch.handle(org, c.ID)
			if ch != nil && o.watch.order(org, c.ID, ch, &stallCmd{action: StallCancel, actor: cmd.actor, done: make(chan string, 1)}) {
				res.Cancelled = append(res.Cancelled, c.ID)
				continue
			}
			o.watch.markCancelled(org, c.ID, cmd.actor)
			if ct, err := o.store.GetTask(ctx, org, c.ID); err == nil && ct.Status != domain.TaskPending {
				o.stallBlock(ctx, nil, ct, cmd.actor)
			}
			res.Cancelled = append(res.Cancelled, c.ID)
		}
		if cur, err := o.store.GetTask(ctx, org, t.ID); err == nil {
			res.Task = cur
		}
		return res, nil
	}
	if cmd.action == StallReassign {
		t.AgentID, t.AssignedReason = cmd.agentID, "Reasignada por "+cmd.actor+" (trabajo atascado)"
	}
	fin := time.Now().UTC()
	t.Status, t.FinishedAt = domain.TaskFailed, &fin
	if err := o.store.UpdateTask(ctx, org, t); err != nil {
		return StallRecoverResult{}, err
	}
	rr, err := o.recoverTask(ctx, t.ID, false, "")
	if err != nil {
		return StallRecoverResult{}, err
	}
	return StallRecoverResult{Task: rr.Task, Status: "reopened"}, nil
}

// errIsCascade reports whether err is a changed cancellation cascade (and returns it).
func errIsCascade(err error) (*CascadeChangedError, bool) {
	var ce *CascadeChangedError
	ok := errors.As(err, &ce)
	return ce, ok
}

// IsCascadeChanged exposes errIsCascade to the API.
func IsCascadeChanged(err error) ([]CascadeTask, bool) {
	if ce, ok := errIsCascade(err); ok {
		return ce.Cascade, true
	}
	return nil, false
}
