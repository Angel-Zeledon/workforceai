package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Durable execution (A1, docs/architecture/07-seguridad-costos.md 5.4).
//
// A request runs in a goroutine with in-memory state. What that goroutine needs
// to continue after a restart is persisted at two points:
//   - RunMeta, when processing starts and after the plan review: who asked
//     (the "on behalf of" of approvals), the conversation, the "no external
//     actions" flag and the tasks removed by the human;
//   - TaskCheckpoint, right after a tool approval is requested: the task output,
//     the tool requests still to handle, the approval it waits for and the taint
//     of what the task read. It is deleted when the task ends.
//
// Recover, called once at startup, rebuilds every unfinished request:
//   - planning / awaiting_confirmation: failed with a clear message (the plan
//     review and the cost confirmation live in memory);
//   - running / awaiting_approval / paused: the scheduler runs again over all
//     the tasks; finished ones return their outcome, pending/running ones run
//     again (the runtime has no side effects), awaiting_approval ones resume on
//     their checkpoint with the approval's original deadline. Without a
//     checkpoint (gateway or pre-migration tasks) the pending approval is
//     superseded and the task runs again, so a human always decides on a fresh
//     approval.
//   - requests owned by a project (RequestOwner) or without RunMeta are failed:
//     the projects engine still marks its interrupted projects failed.
//
// Recover assumes it is the only process recovering (one backend instance, or
// recovery enabled on one of them): the per-task locks prevent a double
// execution but the loser would fail its task.

// RunMeta is what a request execution needs to be rebuilt after a restart.
type RunMeta struct {
	RequestID      string    `json:"request_id"`
	RequestedBy    string    `json:"requested_by"`
	ConversationID string    `json:"conversation_id"`
	ReadOnly       bool      `json:"read_only"`
	RemovedTaskIDs []string  `json:"removed_task_ids"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// CheckpointTaint is the taint of a task (what external content it read).
type CheckpointTaint struct {
	Tainted   bool     `json:"tainted"`
	Suspected bool     `json:"suspected"`
	Conns     []string `json:"conns"`
}

// TaskCheckpoint is the resume point of a task waiting on a tool approval.
type TaskCheckpoint struct {
	TaskID     string                  `json:"task_id"`
	RequestID  string                  `json:"request_id"`
	ApprovalID string                  `json:"approval_id"`
	ToolIndex  int                     `json:"tool_index"` // Tools[ToolIndex] waits for ApprovalID
	Tools      []ToolRequest           `json:"tools"`
	Output     domain.StructuredOutput `json:"output"`
	Taint      *CheckpointTaint        `json:"taint,omitempty"`
	// Executed: the approved action was already recorded (at most once).
	Executed  bool      `json:"executed"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RunStore persists run meta and task checkpoints. Optional: without it the
// orchestrator works as before and nothing survives a restart.
type RunStore interface {
	PutRunMeta(ctx context.Context, orgID string, m RunMeta) error
	// GetRunMeta returns domain.ErrNotFound when the request has none.
	GetRunMeta(ctx context.Context, orgID, requestID string) (RunMeta, error)
	PutCheckpoint(ctx context.Context, orgID string, c TaskCheckpoint) error
	// GetCheckpoint returns domain.ErrNotFound when the task has none.
	GetCheckpoint(ctx context.Context, orgID, taskID string) (TaskCheckpoint, error)
	DeleteCheckpoint(ctx context.Context, orgID, taskID string) error
}

// RequestOwner is implemented by layers that run their own requests (projects)
// and resume (or fail) them themselves.
type RequestOwner interface {
	OwnsRequest(ctx context.Context, org, requestID string) bool
}

type durableState struct {
	runs  RunStore
	owner RequestOwner

	mu     sync.Mutex
	active map[string]bool // requests being processed by this process
}

// track registers a request as being processed; false if it already was.
func (o *Orchestrator) track(id string) bool {
	d := &o.durable
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active == nil {
		d.active = map[string]bool{}
	}
	if d.active[id] {
		return false
	}
	d.active[id] = true
	return true
}

func (o *Orchestrator) untrack(id string) {
	o.durable.mu.Lock()
	delete(o.durable.active, id)
	o.durable.mu.Unlock()
}

func (o *Orchestrator) saveRunMeta(ctx context.Context, rs *run) {
	if o.durable.runs == nil {
		return
	}
	rs.ext.mu.Lock()
	ro := rs.ext.readOnly
	rs.ext.mu.Unlock()
	m := RunMeta{RequestID: rs.req.ID, RequestedBy: rs.requestedBy, ConversationID: rs.convID, ReadOnly: ro,
		RemovedTaskIDs: append([]string{}, rs.removed...), UpdatedAt: time.Now().UTC()}
	if err := o.durable.runs.PutRunMeta(context.WithoutCancel(ctx), o.org(ctx), m); err != nil {
		o.log.Warn("save run meta", "request", rs.req.ID, "err", err)
	}
}

func (o *Orchestrator) saveCheckpoint(ctx context.Context, rs *run, t domain.Task, tools []ToolRequest, idx int, approvalID string) {
	if o.durable.runs == nil {
		return
	}
	cp := TaskCheckpoint{TaskID: t.ID, RequestID: t.RequestID, ApprovalID: approvalID, ToolIndex: idx,
		Tools: tools, UpdatedAt: time.Now().UTC()}
	if t.Output != nil {
		cp.Output = *t.Output
	}
	rs.ext.mu.Lock()
	if tt := rs.ext.taint[t.ID]; tt != nil {
		cp.Taint = &CheckpointTaint{Tainted: tt.tainted, Suspected: tt.suspected, Conns: append([]string{}, tt.conns...)}
	}
	rs.ext.mu.Unlock()
	if err := o.durable.runs.PutCheckpoint(context.WithoutCancel(ctx), o.org(ctx), cp); err != nil {
		o.log.Warn("save task checkpoint", "task", t.ID, "err", err)
	}
}

func (o *Orchestrator) markExecuted(ctx context.Context, taskID, approvalID string) {
	if o.durable.runs == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	cp, err := o.durable.runs.GetCheckpoint(ctx, o.org(ctx), taskID)
	if err != nil || cp.ApprovalID != approvalID {
		return
	}
	cp.Executed, cp.UpdatedAt = true, time.Now().UTC()
	if err := o.durable.runs.PutCheckpoint(ctx, o.org(ctx), cp); err != nil {
		o.log.Warn("mark checkpoint executed", "task", taskID, "err", err)
	}
}

func (o *Orchestrator) dropCheckpoint(ctx context.Context, taskID string) {
	if o.durable.runs == nil {
		return
	}
	if err := o.durable.runs.DeleteCheckpoint(context.WithoutCancel(ctx), o.org(ctx), taskID); err != nil && !errors.Is(err, domain.ErrNotFound) {
		o.log.Warn("drop task checkpoint", "task", taskID, "err", err)
	}
}

// ---- recovery ----

func resumableStatus(st domain.RequestStatus) bool {
	return st == domain.RequestRunning || st == domain.RequestAwaitingApproval || st == domain.RequestPaused
}

func beforeStartStatus(st domain.RequestStatus) bool {
	return st == domain.RequestPlanning || st == domain.RequestAwaitingConfirmation
}

// Recover resumes (or fails, when they cannot be resumed) the requests that
// were in progress when the process stopped. orgs empty means the default
// organization. It returns how many requests were resumed.
func (o *Orchestrator) Recover(ctx context.Context, orgs []string) (int, error) {
	if len(orgs) == 0 {
		orgs = []string{o.cfg.OrgID}
	}
	resumed := 0
	for _, org := range orgs {
		octx := WithOrg(ctx, org)
		reqs, err := o.store.ListRequests(octx, org)
		if err != nil {
			return resumed, fmt.Errorf("recover %s: %w", org, err)
		}
		for _, req := range reqs {
			if !resumableStatus(req.Status) && !beforeStartStatus(req.Status) {
				continue
			}
			if !o.track(req.ID) {
				continue // already being processed (a concurrent Recover or a live run)
			}
			if o.recoverRequest(octx, req) {
				resumed++
			} else {
				o.untrack(req.ID)
			}
		}
	}
	return resumed, nil
}

// recoverRequest starts the resumed run of one request, or fails it. It
// returns true when a run was started (the run untracks itself).
func (o *Orchestrator) recoverRequest(ctx context.Context, req domain.Request) bool {
	org := o.org(ctx)
	rs, err := o.rebuildRun(ctx, req)
	if err != nil {
		o.log.Warn("recover request", "request", req.ID, "err", err)
		return false
	}
	switch {
	case beforeStartStatus(req.Status):
		o.abandon(ctx, rs, "Solicitud interrumpida antes de empezar", errors.New("el servidor se reinició; vuelve a enviarla"))
		return false
	case o.durable.owner != nil && o.durable.owner.OwnsRequest(ctx, org, req.ID):
		o.abandon(ctx, rs, "Solicitud interrumpida", errors.New("el servidor se reinició mientras el proyecto se ejecutaba"))
		return false
	case o.durable.runs == nil:
		o.abandon(ctx, rs, "Solicitud interrumpida", errors.New("el servidor se reinició y esta instalación no guarda el progreso"))
		return false
	}
	meta, err := o.durable.runs.GetRunMeta(ctx, org, req.ID)
	if err != nil {
		o.abandon(ctx, rs, "Solicitud interrumpida", errors.New("el servidor se reinició y no hay datos para reanudarla; vuelve a enviarla"))
		return false
	}
	rs.requestedBy, rs.removed = meta.RequestedBy, meta.RemovedTaskIDs
	if meta.ConversationID != "" {
		rs.convID = meta.ConversationID
	}
	rs.ext.readOnly = meta.ReadOnly
	tasks, err := o.store.ListTasksByRequest(ctx, org, req.ID)
	if err != nil {
		o.log.Warn("recover request tasks", "request", req.ID, "err", err)
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	rctx := WithRequestID(WithOrg(o.base, org), req.ID)
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		o.resume(rctx, rs, tasks)
	}()
	return true
}

// rebuildRun recreates the in-memory state of a request from the store.
func (o *Orchestrator) rebuildRun(ctx context.Context, req domain.Request) (*run, error) {
	org := o.org(ctx)
	agents, err := o.store.ListAgents(ctx, org)
	if err != nil {
		return nil, err
	}
	rs := &run{req: req, agents: map[string]domain.Agent{}, done: map[string]domain.Task{}, involved: map[string]bool{assistantID: true}}
	for _, a := range agents {
		rs.agents[a.ID] = a
	}
	if convs, err := o.store.ListConversations(ctx, org); err == nil {
		for _, c := range convs {
			if c.RequestID != nil && *c.RequestID == req.ID {
				rs.convID = c.ID
				break
			}
		}
	}
	rs.style = o.loadStyle(ctx)
	return rs, nil
}

// resume runs a recovered request to the end.
func (o *Orchestrator) resume(ctx context.Context, rs *run, tasks []domain.Task) {
	defer o.releaseAgents(ctx, rs)
	defer o.untrack(rs.req.ID)
	done := 0
	for i := range tasks {
		t := &tasks[i]
		rs.touch(t.AgentID)
		if slices.Contains(rs.removed, t.ID) {
			t.DependsOn = append(append([]string{}, t.DependsOn...), "removed:"+t.ID)
		}
		if t.Status == domain.TaskDone {
			if t.Output == nil {
				t.Output = &domain.StructuredOutput{}
				t.Output.Normalize()
			}
			rs.done[t.ID] = *t
			done++
		}
	}
	o.rec.Emit(ctx, Action{Type: domain.EvRequestResumed, Entity: "request", EntityID: rs.req.ID,
		Payload: map[string]any{"request_id": rs.req.ID, "tasks_done": done, "tasks_total": len(tasks)},
		Text:    "Solicitud reanudada tras un reinicio del servidor"})
	o.setRequestStatus(ctx, rs, domain.RequestRunning)
	o.execute(ctx, rs, tasks, func(c context.Context, t domain.Task) Outcome {
		switch t.Status {
		case domain.TaskDone:
			return OutcomeDone
		case domain.TaskFailed:
			return OutcomeFailed
		case domain.TaskBlocked:
			return OutcomeBlocked
		case domain.TaskAwaitingApproval:
			return o.resumeTask(c, rs, t)
		}
		t.Status, t.Output = domain.TaskPending, nil // interrupted mid-call: run it again
		return o.runTask(c, rs, t)
	})
}

// resumeTask continues a task that was waiting on a tool approval.
func (o *Orchestrator) resumeTask(ctx context.Context, rs *run, t domain.Task) Outcome {
	org := o.org(ctx)
	cp, err := o.durable.runs.GetCheckpoint(ctx, org, t.ID)
	if err != nil || cp.ToolIndex < 0 || cp.ToolIndex >= len(cp.Tools) {
		// Nothing to resume from: the pending approval is superseded and the task
		// runs again (it will ask a human again if it still needs to).
		o.supersedeApprovals(ctx, t.ID)
		t.Status, t.Output = domain.TaskPending, nil
		return o.runTask(ctx, rs, t)
	}
	unlock, ok, err := o.locks.Lock(ctx, "task:"+t.ID, o.cfg.LockTTL)
	if err != nil {
		o.log.Warn("task lock unavailable, continuing without it", "task", t.ID, "err", err)
	} else if !ok {
		o.failTask(ctx, rs, t, errors.New("la tarea ya está siendo ejecutada por otro worker"))
		return OutcomeFailed
	} else {
		defer unlock()
	}
	out := cp.Output
	t.Output = &out
	if cp.Taint != nil {
		rs.ext.mu.Lock()
		if rs.ext.taint == nil {
			rs.ext.taint = map[string]*taskTaint{}
		}
		rs.ext.taint[t.ID] = &taskTaint{tainted: cp.Taint.Tainted, suspected: cp.Taint.Suspected, conns: cp.Taint.Conns}
		rs.ext.mu.Unlock()
	}
	if !o.admitTask(ctx, rs, t) {
		return OutcomeFailed // kill switch / pause never released before shutdown
	}
	agent := rs.agents[t.AgentID]
	if !cp.Executed {
		ap, ch, err := o.approvals.Attach(ctx, cp.ApprovalID)
		if err != nil {
			o.failTask(ctx, rs, t, err)
			return OutcomeFailed
		}
		if res := o.waitToolApproval(ctx, rs, &t, agent, cp.Tools[cp.ToolIndex], ap, ch, o.approvals.Deadline(ap)); res != OutcomeDone {
			return res
		}
	}
	if res := o.handleToolsFrom(ctx, rs, &t, agent, cp.Tools, cp.ToolIndex+1); res != OutcomeDone {
		return res
	}
	return o.completeTask(ctx, rs, t, agent)
}

// supersedeApprovals rejects, as the system, the pending approvals of a task
// that is going to run again.
func (o *Orchestrator) supersedeApprovals(ctx context.Context, taskID string) {
	ps, err := o.store.ListApprovals(ctx, o.org(ctx), string(domain.ApprovalPending))
	if err != nil {
		o.log.Warn("list approvals", "err", err)
		return
	}
	for _, a := range ps {
		if a.TaskID != taskID {
			continue
		}
		if _, err := o.approvals.Supersede(ctx, a.ID, "superseded: server restarted"); err != nil && !errors.Is(err, domain.ErrConflict) {
			o.log.Warn("supersede approval", "approval", a.ID, "err", err)
		}
	}
}

// abandon fails a request that cannot be resumed: its unfinished tasks are
// blocked, their pending approvals superseded and the user is told why.
func (o *Orchestrator) abandon(ctx context.Context, rs *run, what string, cause error) {
	org := o.org(ctx)
	if tasks, err := o.store.ListTasksByRequest(ctx, org, rs.req.ID); err == nil {
		fin := time.Now().UTC()
		for _, t := range tasks {
			if t.Status == domain.TaskDone || t.Status == domain.TaskFailed || t.Status == domain.TaskBlocked {
				continue
			}
			o.supersedeApprovals(ctx, t.ID)
			t.Status, t.FinishedAt = domain.TaskBlocked, &fin
			if err := o.store.UpdateTask(ctx, org, t); err != nil {
				o.log.Warn("update task", "err", err)
			}
			o.rec.Emit(ctx, Action{Type: domain.EvTaskBlocked, AgentID: t.AgentID, Entity: "task", EntityID: t.ID,
				Payload: map[string]any{"task": t}, Text: "Tarea interrumpida por un reinicio: " + t.Title})
			if t.AgentID != assistantID {
				o.setState(ctx, t.AgentID, domain.StateIdle, "Disponible", nil, 0)
			}
		}
	}
	o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "request.interrupted", Entity: "request", EntityID: rs.req.ID,
		Details: map[string]any{"status": rs.req.Status, "reason": cause.Error()}})
	o.failRequest(ctx, rs, assistantID, what, cause)
}
