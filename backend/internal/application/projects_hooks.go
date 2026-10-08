package application

import (
	"context"
	"fmt"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Projects hook (docs/architecture/workflow-visualization.md): a project is a
// set of real tasks submitted as ONE request (SubmitPlan), so scheduling,
// dependencies, parallelism, budget caps, approvals, the kill switch and the
// audit trail are the orchestrator's. The only thing the orchestrator does not
// know is the project itself: pause/cancel of a whole project and the human
// gates of a project plan. internal/projects plugs in here through a TaskGate,
// consulted once per task right after the execution guard (kill switch, agent
// pause) admitted it and before any budget is reserved.

// GateAction is what a TaskGate decides for one task.
type GateAction int

const (
	// GateRun lets the task run normally (the zero value: no gate, no change).
	GateRun GateAction = iota
	// GateHold keeps the task waiting (project paused); it is asked again shortly.
	GateHold
	// GateAbort blocks the task for good (project cancelled, gate rejected).
	GateAbort
	// GateComplete finishes the task without calling the runtime (milestones and
	// approved human gates); its dependents are released.
	GateComplete
	// GateApproval asks a human before the task starts; the answer is reported
	// back with GateResolved and the gate is consulted again.
	GateApproval
)

// GateApprovalSpec describes the approval of a GateApproval decision.
type GateApprovalSpec struct {
	Action  string
	Title   string
	Details string
	Risk    string // low|medium|high
}

// GateDecision is the answer of a TaskGate.
type GateDecision struct {
	Action   GateAction
	Reason   string
	Summary  string // GateComplete: summary stored as the task output
	Approval GateApprovalSpec
}

// TaskGate is implemented by the projects layer. Implementations must be
// idempotent: a task can be consulted several times.
type TaskGate interface {
	GateTask(ctx context.Context, org string, t domain.Task) GateDecision
	GateResolved(ctx context.Context, org string, t domain.Task, approvalID string, approved bool)
}

// SetTaskGate installs the project gate (nil removes it).
// A gate that is also a RequestOwner (projects) keeps its requests out of the
// restart recovery (durable.go).
func (o *Orchestrator) SetTaskGate(g TaskGate) {
	o.conn.gate = g
	o.durable.owner, _ = g.(RequestOwner)
}

const gatePoll = 100 * time.Millisecond

// projectGate consults the TaskGate for a task that the execution guard
// admitted. handled=false means "run the task normally".
func (o *Orchestrator) projectGate(ctx context.Context, rs *run, t domain.Task) (out Outcome, handled bool) {
	g := o.conn.gate
	if g == nil {
		return OutcomeDone, false
	}
	org := o.org(ctx)
	held := false
	for {
		d := g.GateTask(ctx, org, t)
		switch d.Action {
		case GateRun:
			if held {
				// The guard may have changed while the project was paused: check again.
				if !o.admitTask(ctx, rs, t) {
					return OutcomeFailed, true
				}
				o.setState(ctx, t.AgentID, domain.StateWorking, "Reanudado: "+t.Title, &t.ID, 10)
			}
			return OutcomeDone, false
		case GateHold:
			if !held {
				held = true
				tid := t.ID
				o.setState(ctx, t.AgentID, domain.StateBlocked, "En pausa (proyecto): "+t.Title, &tid, 0)
				o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "task.paused", Entity: "task", EntityID: t.ID,
					Details: map[string]any{"reason": d.Reason, "agent_id": t.AgentID}})
			}
			select {
			case <-ctx.Done():
				return OutcomeFailed, true
			case <-time.After(gatePoll):
			}
		case GateAbort:
			o.blockTask(ctx, rs, t, d.Reason)
			return OutcomeBlocked, true
		case GateComplete:
			return o.completeGateTask(ctx, rs, t, d.Summary), true
		case GateApproval:
			if !o.gateApproval(ctx, rs, &t, d.Approval, g) {
				return OutcomeFailed, true
			}
		default:
			return OutcomeDone, false
		}
	}
}

// gateApproval asks a human and reports the answer to the gate. It returns
// false when the wait was interrupted (shutdown) or the approval could not be
// created (the task is then failed).
func (o *Orchestrator) gateApproval(ctx context.Context, rs *run, t *domain.Task, spec GateApprovalSpec, g TaskGate) bool {
	risk := spec.Risk
	if risk != "low" && risk != "medium" && risk != "high" {
		risk = "medium"
	}
	ap := domain.Approval{TaskID: t.ID, AgentID: t.AgentID, Action: spec.Action, Risk: risk, Title: spec.Title, Details: spec.Details}
	governApproval(&ap, rs, PolicyVerdict{RequiredApprovals: 1})
	t.Status = domain.TaskAwaitingApproval
	if err := o.store.UpdateTask(ctx, o.org(ctx), *t); err != nil {
		o.failTask(ctx, rs, *t, err)
		return false
	}
	ap, ch, err := o.approvals.Request(ctx, ap)
	if err != nil {
		o.failTask(ctx, rs, *t, err)
		return false
	}
	tid := t.ID
	o.setRequestStatus(ctx, rs, domain.RequestAwaitingApproval)
	o.setState(ctx, t.AgentID, domain.StateAwaitingApproval, "Esperando aprobación: "+ap.Title, &tid, 80)
	o.emitMetrics(ctx)
	res, err := o.approvals.Wait(ctx, ch, ap.ID)
	if err != nil {
		return false // cancelled
	}
	o.setRequestStatus(ctx, rs, domain.RequestRunning)
	t.Status = domain.TaskPending
	if err := o.store.UpdateTask(ctx, o.org(ctx), *t); err != nil {
		o.log.Warn("update task", "err", err)
	}
	g.GateResolved(ctx, o.org(ctx), *t, res.ID, res.Status == domain.ApprovalApproved)
	return true
}

// blockTask marks a task that will never run (project cancelled or gate rejected).
func (o *Orchestrator) blockTask(ctx context.Context, rs *run, t domain.Task, why string) {
	fin := time.Now().UTC()
	t.Status, t.FinishedAt = domain.TaskBlocked, &fin
	if err := o.store.UpdateTask(context.WithoutCancel(ctx), o.org(ctx), t); err != nil {
		o.log.Warn("update task", "err", err)
	}
	o.rec.Emit(ctx, Action{Type: domain.EvTaskBlocked, AgentID: t.AgentID, Entity: "task", EntityID: t.ID,
		Payload: map[string]any{"task": t}, Text: fmt.Sprintf("Tarea bloqueada (%s): %s", why, t.Title)})
	o.emitMetrics(ctx)
}

// completeGateTask finishes a gate/milestone task without a runtime call.
func (o *Orchestrator) completeGateTask(ctx context.Context, rs *run, t domain.Task, summary string) Outcome {
	fin := time.Now().UTC()
	t.Status, t.StartedAt, t.FinishedAt = domain.TaskDone, &fin, &fin
	t.Output = &domain.StructuredOutput{Summary: summary, Confidence: 1}
	t.Output.Normalize()
	if err := o.store.UpdateTask(ctx, o.org(ctx), t); err != nil {
		o.failTask(ctx, rs, t, err)
		return OutcomeFailed
	}
	rs.mu.Lock()
	rs.done[t.ID] = t
	rs.mu.Unlock()
	o.rec.Emit(ctx, Action{Type: domain.EvTaskCompleted, AgentID: t.AgentID, Entity: "task", EntityID: t.ID,
		Payload: map[string]any{"task": t}, Text: "Completado: " + t.Title})
	o.emitMetrics(ctx)
	return OutcomeDone
}
