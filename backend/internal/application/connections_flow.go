package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// This file wires the connections/controls ports into the orchestrator
// (docs/architecture/integrations-credentials.md sections 7, 8 and 14). Every
// hook is a no-op while the ports are not set, so the simulation keeps working
// exactly as before.

// connState holds the optional ports.
type connState struct {
	guard ExecutionGuard
	gw    ToolGateway
	plans PlanReviewer
	gate  TaskGate // optional project gate (projects_hooks.go)
	// poll is how often a blocked task re-checks the guard.
	poll time.Duration
}

// runExt is the per-request state added by this feature.
type runExt struct {
	mu       sync.Mutex
	readOnly bool                  // the human marked the request "no external actions"
	taint    map[string]*taskTaint // task id -> what it read
}

type taskTaint struct {
	tainted   bool
	suspected bool
	conns     []string
}

// SetConnections installs the controls guard, the Tool Gateway and the plan
// reviewer. Any of them may be nil.
func (o *Orchestrator) SetConnections(g ExecutionGuard, gw ToolGateway, p PlanReviewer) {
	o.conn.guard, o.conn.gw, o.conn.plans = g, gw, p
	if o.conn.poll <= 0 {
		o.conn.poll = 500 * time.Millisecond
	}
}

// SetGuardPoll changes how often blocked tasks re-check the guard (tests).
func (o *Orchestrator) SetGuardPoll(d time.Duration) { o.conn.poll = d }

func (r *run) taintOf(taskID string) *taskTaint {
	r.ext.mu.Lock()
	defer r.ext.mu.Unlock()
	if r.ext.taint == nil {
		r.ext.taint = map[string]*taskTaint{}
	}
	t := r.ext.taint[taskID]
	if t == nil {
		t = &taskTaint{}
		r.ext.taint[taskID] = t
	}
	return t
}

func (r *run) readOnlyRequest() bool {
	r.ext.mu.Lock()
	defer r.ext.mu.Unlock()
	return r.ext.readOnly
}

// SetAnomalyObserver installs the anomaly detection observer for spend.
func (o *Orchestrator) SetAnomalyObserver(a AnomalyObserver) { o.budget.observer = a }

// admitTask blocks (visibly) while the kill switch, an agent pause or a drain
// forbids work, and returns false only when ctx ends. A paused task is not
// lost: it resumes from the same point once allowed (the guard fails closed,
// so an unreachable controls store keeps the task waiting).
func (o *Orchestrator) admitTask(ctx context.Context, rs *run, t domain.Task) bool {
	g := o.conn.guard
	if g == nil {
		return true
	}
	org := o.org(ctx)
	announced := false
	for {
		v := g.Admit(ctx, org, t.AgentID)
		if v.Allowed {
			if announced {
				o.setState(ctx, t.AgentID, domain.StateWorking, "Reanudado: "+t.Title, &t.ID, 10)
			}
			return true
		}
		if !announced {
			announced = true
			tid := t.ID
			o.setState(ctx, t.AgentID, domain.StateBlocked, "En pausa ("+v.Code+"): "+t.Title, &tid, 0)
			o.rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "task.paused", Entity: "task", EntityID: t.ID,
				Details: map[string]any{"reason": v.Code, "agent_id": t.AgentID}})
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(o.conn.poll):
		}
	}
}

// reviewPlan implements the mandatory plan review (owner decision 7): when the
// plan can reach a connection with write capabilities (or the org asks for
// review of everything) nothing runs until a human approves. Removed tasks are
// wired to a dependency that never completes, so the scheduler skips them and
// everything that depends on them.
func (o *Orchestrator) reviewPlan(ctx context.Context, rs *run, tasks []domain.Task) bool {
	p := o.conn.plans
	if p == nil {
		return true
	}
	org := o.org(ctx)
	infos := make([]PlanTaskInfo, 0, len(tasks))
	for _, t := range tasks {
		infos = append(infos, PlanTaskInfo{ID: t.ID, Title: t.Title, AgentID: t.AgentID, DependsOn: t.DependsOn})
	}
	_, required, ch, err := p.Begin(ctx, org, rs.req.ID, infos)
	if err != nil {
		o.cancelPlanned(ctx, rs, tasks, "revisión de plan no disponible")
		o.failRequest(ctx, rs, assistantID, "No pude preparar la revisión del plan", err)
		return false
	}
	if !required {
		return true
	}
	o.setState(ctx, assistantID, domain.StateWaiting, "Esperando la revisión de tu plan", nil, 20)
	o.emitMetrics(ctx)
	timer := time.NewTimer(o.cfg.ApprovalTimeout)
	defer timer.Stop()
	select {
	case d := <-ch:
		if !d.Approved {
			o.cancelPlanned(ctx, rs, tasks, "plan rechazado")
			o.failRequest(ctx, rs, assistantID, "Plan rechazado", fmt.Errorf("el plan no fue aprobado"))
			return false
		}
		rs.ext.mu.Lock()
		rs.ext.readOnly = d.NoExternalActions
		rs.ext.mu.Unlock()
		removed := map[string]bool{}
		for _, id := range d.RemoveTaskIDs {
			removed[id] = true
		}
		for i := range tasks {
			if removed[tasks[i].ID] {
				tasks[i].DependsOn = append(append([]string{}, tasks[i].DependsOn...), "removed:"+tasks[i].ID)
			}
		}
		o.rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "user"), Action: "plan.approved", Entity: "request", EntityID: rs.req.ID,
			Details: map[string]any{"removed": d.RemoveTaskIDs, "no_external_actions": d.NoExternalActions}})
		return true
	case <-timer.C:
		p.Forget(org, rs.req.ID)
		o.cancelPlanned(ctx, rs, tasks, "plan sin revisar")
		o.failRequest(ctx, rs, assistantID, "Plan sin revisar", fmt.Errorf("el plan no se revisó a tiempo"))
		return false
	case <-ctx.Done():
		p.Forget(org, rs.req.ID)
		return false
	}
}

const maxReadsPerRound = 5

// connectionReads executes the connection-backed READ tool requests of a task
// between runtime turns (up to two rounds): results come back sanitized and
// delimited as untrusted data and are handed to the runtime as external
// content; the task becomes tainted. Write requests are never run here.
func (o *Orchestrator) connectionReads(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, in RunTaskRequest, resp RunTaskResponse) RunTaskResponse {
	gw := o.conn.gw
	if gw == nil {
		return resp
	}
	org := o.org(ctx)
	var notes []string // evidence lines; appended to whichever response we return
	fin := func() RunTaskResponse {
		resp.Output.Evidence = append(resp.Output.Evidence, notes...)
		return resp
	}
	for round := 0; round < 2; round++ {
		var reads, rest []ToolRequest
		for _, tr := range resp.ToolRequests {
			if gw.Route(ctx, org, agent.ID, tr.Tool, tr.Action) == RouteRead {
				reads = append(reads, tr)
			} else {
				rest = append(rest, tr)
			}
		}
		if len(reads) == 0 {
			return fin()
		}
		if !o.admitTask(ctx, rs, *t) {
			resp.ToolRequests = rest
			return fin()
		}
		taint := rs.taintOf(t.ID)
		var blocks []string
		for i, tr := range reads {
			if i >= maxReadsPerRound {
				break
			}
			out := gw.Execute(ctx, GatewayCall{Org: org, AgentID: agent.ID, TaskID: t.ID, RequestID: rs.req.ID, OnBehalfOf: ActorFrom(ctx, ""),
				Tool: tr.Tool, Action: tr.Action, Args: tr.Args, Autonomy: agent.Autonomy, Tainted: taint.tainted,
				ReadOnlyRequest: rs.readOnlyRequest()})
			switch {
			case out.Decision == "allowed" && out.Status == "succeeded":
				blocks = append(blocks, out.Blocks...)
				rs.ext.mu.Lock()
				taint.tainted = taint.tainted || out.Tainted
				taint.suspected = taint.suspected || out.InjectionSuspected
				if out.ConnectionID != "" {
					taint.conns = append(taint.conns, out.ConnectionID)
				}
				rs.ext.mu.Unlock()
			default:
				reason := out.DenyReason
				if reason == "" {
					reason = out.Decision
				}
				o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.denied", Entity: "task", EntityID: t.ID,
					Details: map[string]any{"tool": tr.Tool, "action": tr.Action, "reason": reason}})
				notes = append(notes, fmt.Sprintf("Lectura %s.%s no disponible (%s)", tr.Tool, tr.Action, reason))
			}
		}
		if len(blocks) == 0 {
			resp.ToolRequests = rest
			return fin()
		}
		in2 := in
		in2.ExternalContent = append(append([]string{}, in.ExternalContent...), blocks...)
		res, err := o.reserveOrPause(ctx, rs, agent.ID, t.ID, domain.UsageRunTask)
		if err != nil {
			resp.ToolRequests = rest
			return fin()
		}
		var next RunTaskResponse
		err = o.call(ctx, "run-task", func(c context.Context) (err error) {
			next, err = o.rt.RunTask(c, in2)
			return err
		})
		if err != nil {
			res.Release()
			resp.ToolRequests = rest
			return fin()
		}
		o.recordUsage(ctx, rs, t.ID, agent.ID, domain.UsageRunTask, next.Usage, next.ToolRequests, res)
		resp, in = next, in2
		resp.Output.Normalize()
	}
	// Reads left after two rounds are dropped (the model asked for too much).
	var keep []ToolRequest
	for _, tr := range resp.ToolRequests {
		if gw.Route(ctx, org, agent.ID, tr.Tool, tr.Action) != RouteRead {
			keep = append(keep, tr)
		}
	}
	resp.ToolRequests = keep
	return fin()
}

// legacyBlocked applies read-only mode and the per-tool kill switch to tools
// that are NOT connection-backed (the simulated fakes), so the controls hold
// everywhere. It returns true when the request must not run.
func (o *Orchestrator) legacyBlocked(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, tr ToolRequest) bool {
	g := o.conn.guard
	if g == nil {
		return false
	}
	org := o.org(ctx)
	reason := ""
	switch {
	case g.ToolBlocked(ctx, org, tr.Tool, tr.Action):
		reason = "tool_disabled"
	case g.IsSideEffect(tr.Tool, tr.Action) && (rs.readOnlyRequest() || g.SideEffectsBlocked(ctx, org, agent.ID)):
		reason = "read_only_mode"
	}
	if reason == "" {
		return false
	}
	o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.denied", Entity: "task", EntityID: t.ID,
		Details: map[string]any{"tool": tr.Tool, "action": tr.Action, "reason": reason, "simulated": true}})
	if t.Output != nil {
		t.Output.Evidence = append(t.Output.Evidence, fmt.Sprintf("Acción %s.%s bloqueada (%s)", tr.Tool, tr.Action, reason))
	}
	return true
}

// gatewayTool routes a tool request through the Tool Gateway when it is
// connection-backed. handled=false means "use the legacy path". pv is the
// verdict of the policy layer (already checked: not a deny); its approval
// requirements (role, double approval) are attached to the gateway's approval.
func (o *Orchestrator) gatewayTool(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, tr ToolRequest, pv PolicyVerdict) (handled bool, out Outcome) {
	gw := o.conn.gw
	if gw == nil {
		return false, OutcomeDone
	}
	org := o.org(ctx)
	switch gw.Route(ctx, org, agent.ID, tr.Tool, tr.Action) {
	case RouteNone:
		return false, OutcomeDone
	case RouteRead:
		// A read that survived connectionReads (budget of rounds): not executed.
		o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.read_skipped", Entity: "task", EntityID: t.ID,
			Details: map[string]any{"tool": tr.Tool, "action": tr.Action}})
		return true, OutcomeDone
	}
	if !o.admitTask(ctx, rs, *t) {
		return true, OutcomeFailed
	}
	taint := rs.taintOf(t.ID)
	rs.ext.mu.Lock()
	call := GatewayCall{Org: org, AgentID: agent.ID, TaskID: t.ID, RequestID: rs.req.ID, OnBehalfOf: ActorFrom(ctx, ""),
		Tool: tr.Tool, Action: tr.Action, Args: tr.Args, Autonomy: agent.Autonomy, Tainted: taint.tainted,
		ReadConnections: append([]string{}, taint.conns...), InjectionSuspected: taint.suspected, ReadOnlyRequest: rs.ext.readOnly}
	rs.ext.mu.Unlock()
	res := gw.Execute(ctx, call)

	note := func(format string, a ...any) {
		if t.Output != nil {
			t.Output.Evidence = append(t.Output.Evidence, fmt.Sprintf(format, a...))
		}
	}
	switch res.Decision {
	case "denied":
		o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.denied", Entity: "task", EntityID: t.ID,
			Details: map[string]any{"tool": tr.Tool, "action": tr.Action, "reason": res.DenyReason}})
		note("Acción %s.%s denegada (%s)", tr.Tool, tr.Action, res.DenyReason)
		return true, OutcomeDone
	case "allowed":
		if o.policy != nil {
			o.policy.Reserve(ctx, o.policyInput(rs, agent, tr)) // counts against the windowed limits
		}
		o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.executed", Entity: "task", EntityID: t.ID,
			Details: map[string]any{"tool": tr.Tool, "action": tr.Action, "status": res.Status, "hold_id": res.HoldID}})
		note("%s.%s: %s", tr.Tool, tr.Action, res.Summary)
		return true, OutcomeDone
	}
	// needs_approval: the human decides; the approval is bound to these exact args.
	p := res.Pending
	if p == nil {
		return true, OutcomeDone
	}
	risk := strings.ToLower(p.Risk)
	if risk != "low" && risk != "medium" && risk != "high" {
		risk = "high"
	}
	ap := domain.Approval{TaskID: t.ID, AgentID: agent.ID, Action: tr.Action, Risk: risk, Title: p.Title, Details: p.Details,
		Context: map[string]any{"account": p.Account, "recipients": p.Recipients, "reversibility": p.Reversibility, "tainted": p.Tainted,
			"external_origin": p.ExternalOrigin, "hold_seconds": p.HoldSeconds, "flags": p.Flags, "args_hash": p.ArgsHash, "outbox_id": p.OutboxID}}
	governApproval(&ap, rs, pv)
	tid := t.ID
	t.Status = domain.TaskAwaitingApproval
	if err := o.store.UpdateTask(ctx, org, *t); err != nil {
		o.failTask(ctx, rs, *t, err)
		return true, OutcomeFailed
	}
	ap, ch, err := o.approvals.Request(ctx, ap)
	if err != nil {
		o.failTask(ctx, rs, *t, err)
		return true, OutcomeFailed
	}
	gw.RegisterApproval(ap.ID, *p)
	o.setRequestStatus(ctx, rs, domain.RequestAwaitingApproval)
	o.setState(ctx, agent.ID, domain.StateAwaitingApproval, "Esperando aprobación: "+ap.Title, &tid, 80)
	o.emitMetrics(ctx)
	decision, err := o.approvals.Wait(ctx, ch, ap.ID)
	if err != nil {
		return true, OutcomeFailed // cancelled
	}
	approved := decision.Status == domain.ApprovalApproved
	if approved && o.policy != nil {
		// The rules may have changed while the approval waited: re-validate.
		if rv := o.policy.Recheck(ctx, o.policyInput(rs, agent, tr), decision.ID, approverIDs(decision)); rv.Denied() {
			approved = false
			o.policyDenied(ctx, t, agent, tr, rv, false)
		}
	}
	gw.ApprovalResolved(ap.ID, approved)
	o.setRequestStatus(ctx, rs, domain.RequestRunning)
	if !approved {
		fin := time.Now().UTC()
		t.Status, t.FinishedAt = domain.TaskBlocked, &fin
		if err := o.store.UpdateTask(ctx, org, *t); err != nil {
			o.log.Warn("update task", "err", err)
		}
		o.setState(ctx, agent.ID, domain.StateBlocked, "Acción rechazada: "+ap.Title, &tid, 80)
		o.rec.Emit(ctx, Action{Type: domain.EvTaskBlocked, AgentID: agent.ID, Entity: "task", EntityID: t.ID,
			Payload: map[string]any{"task": *t}, Text: fmt.Sprintf("Tarea bloqueada (aprobación rechazada): %s", t.Title)})
		o.emitMetrics(ctx)
		return true, OutcomeBlocked
	}
	t.Status = domain.TaskRunning
	call.ApprovedArgsHash, call.ApprovalID = p.ArgsHash, decision.ID
	// Everything is re-validated at execution time (TOCTOU): a revoked grant or
	// an active kill switch stops an approved action.
	res2 := gw.Execute(ctx, call)
	switch {
	case res2.Decision == "denied":
		o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.denied", Entity: "task", EntityID: t.ID,
			Details: map[string]any{"tool": tr.Tool, "action": tr.Action, "reason": res2.DenyReason, "approval_id": decision.ID}})
		note("Acción aprobada pero denegada al ejecutar (%s)", res2.DenyReason)
	default:
		o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.executed", Entity: "task", EntityID: t.ID,
			Details: map[string]any{"tool": tr.Tool, "action": tr.Action, "status": res2.Status, "approval_id": decision.ID, "approvers": approverIDs(decision), "hold_id": res2.HoldID}})
		note("%s.%s: %s", tr.Tool, tr.Action, res2.Summary)
	}
	o.setState(ctx, agent.ID, domain.StateWorking, "Acción aprobada, finalizando: "+t.Title, &tid, 90)
	return true, OutcomeDone
}
