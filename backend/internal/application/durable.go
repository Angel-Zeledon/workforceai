package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Durable execution (A1, docs/architecture/07-seguridad-costos.md 5.4).
//
// A request runs in a goroutine with in-memory state. What that goroutine needs
// to continue after a restart is persisted at these points:
//   - RunMeta, when processing starts, when a human gate (plan review, cost
//     confirmation) begins and ends: who asked (the "on behalf of" of
//     approvals), the conversation, the chat turn that started it, the "no
//     external actions" flag, the tasks removed by the human and the gate the
//     request waits on (with its start, so the original deadline is kept);
//   - TaskCheckpoint, right after a tool approval is requested: the task output,
//     the tool requests still to handle, the approval it waits for, the taint
//     of what the task read and, for a Tool Gateway action, its approval card.
//     It is deleted when the task ends;
//   - ApprovalExecution (ExecutionLedger), right before an approved action
//     runs: one record per approval id, so the action runs at most once
//     across restarts, paths (task or outbox) and instances.
//
// Recover, called once at startup, rebuilds every unfinished request:
//   - planning / awaiting_confirmation with a gate in the RunMeta: the gate is
//     asked again (same deadline) and the request continues once the human
//     decides; without a gate (the plan call itself was interrupted) the
//     request is failed with a clear message;
//   - running / awaiting_approval / paused: the scheduler runs again over all
//     the tasks; finished ones return their outcome, pending/running ones run
//     again (the runtime has no side effects), awaiting_approval ones resume on
//     their checkpoint (simulated tool or Tool Gateway action) with the
//     approval's original deadline. Without a checkpoint (pre-migration tasks)
//     the pending approval is superseded and the task runs again, so a human
//     always decides on a fresh approval. Project gate approvals are not
//     superseded: the project gate attaches to them again;
//   - requests owned by a project (RequestOwner) are resumed once the owner
//     re-attached its state (ResumeRequest); otherwise they are failed.
//
// Recover assumes it is the only process recovering (one backend instance, or
// recovery enabled on one of them): the per-task locks and the execution
// ledger prevent a double execution but the loser would fail its task.

// RunMeta is what a request execution needs to be rebuilt after a restart.
type RunMeta struct {
	RequestID      string   `json:"request_id"`
	RequestedBy    string   `json:"requested_by"`
	ConversationID string   `json:"conversation_id"`
	ReadOnly       bool     `json:"read_only"`
	RemovedTaskIDs []string `json:"removed_task_ids"`
	// MaxParallel is the per-request parallelism override (WithRunParallel); 0 = Config.MaxParallel.
	MaxParallel int `json:"max_parallel,omitempty"`
	// Gate is the human gate the request waits on before it starts (nil: none).
	Gate *RunGate `json:"gate,omitempty"`
	// Chat is the chat turn that started the request (nil: not chat-born).
	Chat      *ChatLinkMeta `json:"chat,omitempty"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// Gate kinds of a request that has not started yet.
const (
	GatePlanReview       = "plan_review"
	GateCostConfirmation = "cost_confirmation"
)

// RunGate is a human gate in progress. StartedAt fixes its deadline
// (StartedAt + ApprovalTimeout), also after a restart (decision D-A1b).
type RunGate struct {
	Kind      string               `json:"kind"`
	StartedAt time.Time            `json:"started_at"`
	Estimate  *domain.CostEstimate `json:"estimate,omitempty"` // the estimate the human was asked to confirm
}

// ChatLinkMeta is the persisted form of a chat link (chat.go chatLink).
type ChatLinkMeta struct {
	ConversationID string `json:"conversation_id"`
	TurnID         string `json:"turn_id"`
	ReplyTo        string `json:"reply_to"`
	Speaker        string `json:"speaker"`
	Locale         string `json:"locale"`
	ReassignFrom   string `json:"reassign_from,omitempty"`
	ReassignTo     string `json:"reassign_to,omitempty"`
}

func chatMeta(l *chatLink) *ChatLinkMeta {
	if l == nil {
		return nil
	}
	return &ChatLinkMeta{ConversationID: l.conv, TurnID: l.turnID, ReplyTo: l.replyTo, Speaker: l.speaker, Locale: l.loc,
		ReassignFrom: l.reassignFrom, ReassignTo: l.reassignTo}
}

func (m *ChatLinkMeta) link() *chatLink {
	if m == nil || m.ConversationID == "" {
		return nil
	}
	return &chatLink{conv: m.ConversationID, turnID: m.TurnID, replyTo: m.ReplyTo, speaker: m.Speaker, loc: m.Locale,
		reassignFrom: m.ReassignFrom, reassignTo: m.ReassignTo}
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
	// Gateway is the approval card of a Tool Gateway action (nil: simulated tool).
	Gateway *GatewayPending `json:"gateway,omitempty"`
	// Executed: the approved action was already recorded (at most once).
	Executed  bool      `json:"executed"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ExecutionClaimed is the status of an execution record whose action is running
// (or was interrupted while running: it is never run again).
const ExecutionClaimed = "claimed"

// ApprovalExecution records that the action of an approval was executed.
type ApprovalExecution struct {
	ApprovalID string     `json:"approval_id"`
	TaskID     string     `json:"task_id"`
	Tool       string     `json:"tool"`
	Action     string     `json:"action"`
	ArgsHash   string     `json:"args_hash"`
	Status     string     `json:"status"` // claimed | succeeded | scheduled | failed | simulated
	HoldID     string     `json:"hold_id"`
	ClaimedAt  time.Time  `json:"claimed_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// ExecutionLedger makes an approved action run at most once: the first caller
// that claims an approval id wins, in any process.
type ExecutionLedger interface {
	// ClaimExecution inserts the record. claimed=false means the approval was
	// already executed (or is being executed); prev is the existing record.
	ClaimExecution(ctx context.Context, orgID string, e ApprovalExecution) (claimed bool, prev ApprovalExecution, err error)
	// FinishExecution stores the outcome of a claimed execution.
	FinishExecution(ctx context.Context, orgID, approvalID, status, holdID string) error
}

// RunStore persists run meta, task checkpoints and executed approvals.
// Optional: without it the orchestrator works as before and nothing survives a
// restart.
type RunStore interface {
	ExecutionLedger
	PutRunMeta(ctx context.Context, orgID string, m RunMeta) error
	// GetRunMeta returns domain.ErrNotFound when the request has none.
	GetRunMeta(ctx context.Context, orgID, requestID string) (RunMeta, error)
	PutCheckpoint(ctx context.Context, orgID string, c TaskCheckpoint) error
	// GetCheckpoint returns domain.ErrNotFound when the task has none.
	GetCheckpoint(ctx context.Context, orgID, taskID string) (TaskCheckpoint, error)
	DeleteCheckpoint(ctx context.Context, orgID, taskID string) error
}

// RequestOwner is implemented by layers that own some requests (projects).
type RequestOwner interface {
	OwnsRequest(ctx context.Context, org, requestID string) bool
	// ResumeRequest re-attaches the owner's state to a request about to be
	// resumed after a restart; false means it cannot be resumed (it is failed).
	ResumeRequest(ctx context.Context, org, requestID string) bool
}

type durableState struct {
	runs  RunStore
	owner RequestOwner

	mu     sync.Mutex
	active map[string]bool // requests being processed by this process
	// reopening counts the manual recoveries (recovery.go) running per request.
	reopening map[string]int
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
	rs.mu.Lock()
	gate := rs.gate
	rs.mu.Unlock()
	m := RunMeta{RequestID: rs.req.ID, RequestedBy: rs.requestedBy, ConversationID: rs.convID, ReadOnly: ro, MaxParallel: rs.maxParallel,
		RemovedTaskIDs: append([]string{}, rs.removed...), Gate: gate, Chat: chatMeta(rs.chat), UpdatedAt: time.Now().UTC()}
	if err := o.durable.runs.PutRunMeta(context.WithoutCancel(ctx), o.org(ctx), m); err != nil {
		o.log.Warn("save run meta", "request", rs.req.ID, "err", err)
	}
}

// setGate records the human gate the request waits on (nil: none) in the run meta.
func (o *Orchestrator) setGate(ctx context.Context, rs *run, g *RunGate) {
	rs.mu.Lock()
	rs.gate = g
	rs.mu.Unlock()
	o.saveRunMeta(ctx, rs)
}

func (o *Orchestrator) saveCheckpoint(ctx context.Context, rs *run, t domain.Task, tools []ToolRequest, idx int, approvalID string, gw *GatewayPending) {
	if o.durable.runs == nil {
		return
	}
	cp := TaskCheckpoint{TaskID: t.ID, RequestID: t.RequestID, ApprovalID: approvalID, ToolIndex: idx,
		Tools: tools, Gateway: gw, UpdatedAt: time.Now().UTC()}
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

// claimExecution claims the execution of an approved action in the ledger.
// It returns false when the action must not run: it was already executed (by
// this or another process) or the ledger is unreachable (fail closed). Without
// a RunStore there is nothing durable to claim against.
func (o *Orchestrator) claimExecution(ctx context.Context, t domain.Task, approvalID string, tr ToolRequest, argsHash string) (bool, string) {
	if o.durable.runs == nil {
		return true, ""
	}
	ok, prev, err := o.durable.runs.ClaimExecution(context.WithoutCancel(ctx), o.org(ctx), ApprovalExecution{ApprovalID: approvalID, TaskID: t.ID,
		Tool: tr.Tool, Action: tr.Action, ArgsHash: argsHash})
	switch {
	case err != nil:
		o.log.Warn("claim approved execution", "approval", approvalID, "err", err)
		return false, "execution_record_unavailable"
	case !ok:
		return false, "already_executed:" + prev.Status
	}
	return true, ""
}

// argsHashOf binds an execution record to the exact simulated call.
func argsHashOf(tr ToolRequest) string {
	b, _ := json.Marshal(struct {
		Tool   string         `json:"tool"`
		Action string         `json:"action"`
		Args   map[string]any `json:"args"`
	}{strings.ToLower(tr.Tool), strings.ToLower(tr.Action), tr.Args})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (o *Orchestrator) finishExecution(ctx context.Context, approvalID, status string) {
	if o.durable.runs == nil {
		return
	}
	if err := o.durable.runs.FinishExecution(context.WithoutCancel(ctx), o.org(ctx), approvalID, status, ""); err != nil {
		o.log.Warn("finish approved execution", "approval", approvalID, "err", err)
	}
}

// ---- recovery ----

// WithDefaultOrg returns orgs plus def when it is missing (and not empty).
func WithDefaultOrg(orgs []string, def string) []string {
	if def == "" || slices.Contains(orgs, def) {
		return orgs
	}
	return append(slices.Clone(orgs), def)
}

func resumableStatus(st domain.RequestStatus) bool {
	return st == domain.RequestRunning || st == domain.RequestAwaitingApproval || st == domain.RequestPaused
}

func beforeStartStatus(st domain.RequestStatus) bool {
	return st == domain.RequestPlanning || st == domain.RequestAwaitingConfirmation
}

// Recover resumes (or fails, when they cannot be resumed) the requests that
// were in progress when the process stopped. The default organization is
// always included: it has no memberships, so ListOrgIDs never lists it. It
// returns how many requests were resumed.
func (o *Orchestrator) Recover(ctx context.Context, orgs []string) (int, error) {
	orgs = WithDefaultOrg(orgs, o.cfg.OrgID)
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
	meta, metaErr := RunMeta{}, error(domain.ErrNotFound)
	if o.durable.runs != nil {
		meta, metaErr = o.durable.runs.GetRunMeta(ctx, org, req.ID)
	}
	if metaErr == nil {
		rs.requestedBy, rs.removed = meta.RequestedBy, meta.RemovedTaskIDs
		rs.maxParallel = meta.MaxParallel
		if meta.ConversationID != "" {
			rs.convID = meta.ConversationID
		}
		rs.ext.readOnly = meta.ReadOnly
		rs.chat = meta.Chat.link() // the report (or the failure) is echoed into the chat that asked
	}
	gated := metaErr == nil && meta.Gate != nil && beforeStartStatus(req.Status)
	switch {
	case beforeStartStatus(req.Status) && !gated:
		o.abandon(ctx, rs, "Solicitud interrumpida antes de empezar", errors.New("el servidor se reinició; vuelve a enviarla"))
		return false
	case o.durable.runs == nil:
		o.abandon(ctx, rs, "Solicitud interrumpida", errors.New("el servidor se reinició y esta instalación no guarda el progreso"))
		return false
	case metaErr != nil:
		o.abandon(ctx, rs, "Solicitud interrumpida", errors.New("el servidor se reinició y no hay datos para reanudarla; vuelve a enviarla"))
		return false
	case o.durable.owner != nil && o.durable.owner.OwnsRequest(ctx, org, req.ID) && !o.durable.owner.ResumeRequest(ctx, org, req.ID):
		o.abandon(ctx, rs, "Solicitud interrumpida", errors.New("el servidor se reinició y el proyecto no se pudo reanudar"))
		return false
	}
	tasks, err := o.store.ListTasksByRequest(ctx, org, req.ID)
	if err != nil {
		o.log.Warn("recover request tasks", "request", req.ID, "err", err)
		return false
	}
	if gated && len(tasks) == 0 {
		o.abandon(ctx, rs, "Solicitud interrumpida antes de empezar", errors.New("el servidor se reinició; vuelve a enviarla"))
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	rctx := WithRequestID(WithOrg(o.base, org), req.ID)
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		if gated {
			o.resumeGated(rctx, rs, tasks, meta.Gate)
			return
		}
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

// applyRemoved wires the tasks removed in the plan review to a dependency that
// never completes, so the scheduler skips them (as reviewPlan does).
func applyRemoved(rs *run, tasks []domain.Task) {
	for i := range tasks {
		if slices.Contains(rs.removed, tasks[i].ID) {
			tasks[i].DependsOn = append(append([]string{}, tasks[i].DependsOn...), "removed:"+tasks[i].ID)
		}
	}
}

func (o *Orchestrator) emitResumed(ctx context.Context, rs *run, done, total int, gate string) {
	p := map[string]any{"request_id": rs.req.ID, "tasks_done": done, "tasks_total": total}
	if gate != "" {
		p["gate"] = gate
	}
	o.rec.Emit(ctx, Action{Type: domain.EvRequestResumed, Entity: "request", EntityID: rs.req.ID, Payload: p,
		Text: "Solicitud reanudada tras un reinicio del servidor"})
}

// resumeGated continues a request that was waiting on a human gate before it
// started: the gate is asked again with its original deadline, then the
// request follows the normal path (cost confirmation, execution, report).
func (o *Orchestrator) resumeGated(ctx context.Context, rs *run, tasks []domain.Task, gate *RunGate) {
	defer o.releaseAgents(ctx, rs)
	defer o.untrack(rs.req.ID)
	for _, t := range tasks {
		rs.touch(t.AgentID)
	}
	applyRemoved(rs, tasks)
	o.emitResumed(ctx, rs, 0, len(tasks), gate.Kind)
	switch gate.Kind {
	case GatePlanReview:
		if !o.reviewPlanSince(ctx, rs, tasks, gate.StartedAt) || !o.confirmEstimateFrom(ctx, rs, tasks, nil) {
			return
		}
	default:
		if !o.confirmEstimateFrom(ctx, rs, tasks, gate) {
			return
		}
	}
	o.setRequestStatus(ctx, rs, domain.RequestRunning)
	o.setState(ctx, assistantID, domain.StateWaiting, "Coordinando al equipo", nil, 30)
	o.execute(ctx, rs, tasks, func(c context.Context, t domain.Task) Outcome { return o.runTask(c, rs, t) })
}

// resume runs a recovered request to the end.
func (o *Orchestrator) resume(ctx context.Context, rs *run, tasks []domain.Task) {
	defer o.releaseAgents(ctx, rs)
	defer o.untrack(rs.req.ID)
	done := 0
	applyRemoved(rs, tasks)
	for i := range tasks {
		t := &tasks[i]
		rs.touch(t.AgentID)
		if t.Status == domain.TaskDone {
			if t.Output == nil {
				t.Output = &domain.StructuredOutput{}
				t.Output.Normalize()
			}
			rs.done[t.ID] = *t
			done++
		}
	}
	o.emitResumed(ctx, rs, done, len(tasks), "")
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
	if err != nil || cp.ToolIndex < 0 || cp.ToolIndex >= len(cp.Tools) || (cp.Gateway != nil && o.conn.gw == nil) {
		// Nothing to resume from (or the gateway that owns the action is gone):
		// the pending tool approval is superseded and the task runs again (it
		// will ask a human again if it still needs to). A project gate approval
		// is kept: the gate attaches to it again (projectGate).
		o.supersedeApprovals(ctx, t.ID, true)
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
	tr := cp.Tools[cp.ToolIndex]
	switch {
	case cp.Executed:
		// The approved action was already executed (at most once): continue after it.
	case cp.Gateway != nil:
		if res := o.resumeGatewayApproval(ctx, rs, &t, agent, tr, cp); res != OutcomeDone {
			return res
		}
	default:
		ap, ch, err := o.approvals.Attach(ctx, cp.ApprovalID)
		if err != nil {
			o.failTask(ctx, rs, t, err)
			return OutcomeFailed
		}
		if res := o.waitToolApproval(ctx, rs, &t, agent, tr, ap, ch, o.approvals.Deadline(ap)); res != OutcomeDone {
			return res
		}
	}
	if res := o.handleToolsFrom(ctx, rs, &t, agent, cp.Tools, cp.ToolIndex+1); res != OutcomeDone {
		return res
	}
	return o.completeTask(ctx, rs, t, agent)
}

// resumeGatewayApproval waits again on the approval of a Tool Gateway action
// (from its exact point: same approval, same arguments, same deadline) and
// executes it through the gateway once approved. The outbox item, which lives
// in the gateway's memory, is restored so the human can still see and decide it.
func (o *Orchestrator) resumeGatewayApproval(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, tr ToolRequest, cp TaskCheckpoint) Outcome {
	ap, ch, err := o.approvals.Attach(ctx, cp.ApprovalID)
	if err != nil {
		o.failTask(ctx, rs, *t, err)
		return OutcomeFailed
	}
	call := o.gatewayCall(ctx, rs, *t, agent, tr)
	if r, ok := o.conn.gw.(GatewayRestorer); ok && ap.Status == domain.ApprovalPending {
		r.RestoreApproval(ctx, ap.ID, call, *cp.Gateway)
	}
	return o.gatewayAwait(ctx, rs, t, agent, tr, call, *cp.Gateway, ap, ch, o.approvals.Deadline(ap))
}

// supersedeApprovals rejects, as the system, the pending approvals of a task
// that is going to run again. keepGates keeps the approvals of a project gate.
func (o *Orchestrator) supersedeApprovals(ctx context.Context, taskID string, keepGates bool) {
	ps, err := o.store.ListApprovals(ctx, o.org(ctx), string(domain.ApprovalPending))
	if err != nil {
		o.log.Warn("list approvals", "err", err)
		return
	}
	for _, a := range ps {
		if a.TaskID != taskID || (keepGates && isGateApproval(a)) {
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
			o.supersedeApprovals(ctx, t.ID, false)
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
