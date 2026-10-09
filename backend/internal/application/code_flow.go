package application

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// This file wires the code sandbox into the orchestrator (docs/architecture/
// code-sandbox.md). The invariant of the whole feature lives here: a code.run
// request ALWAYS creates a high-risk approval and runs only after a human
// approves it. There is no path from a tool request to an execution that does
// not go through awaitApproval: no policy verdict (not even "allow"), autonomy
// level, standing rule or org rule can skip it.

func isCodeRun(tr ToolRequest) bool {
	return strings.EqualFold(strings.TrimSpace(tr.Tool), CodeToolName) && strings.EqualFold(strings.TrimSpace(tr.Action), CodeActionRun)
}

// codeApprovalAction is the Action of the approval of a code run; the UI uses
// it to render the code card (it is persisted, unlike the approval context).
const codeApprovalAction = "code.run"

func (o *Orchestrator) codeDenied(ctx context.Context, t *domain.Task, agent domain.Agent, reason string) {
	o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.denied", Entity: "task", EntityID: t.ID,
		Details: map[string]any{"tool": CodeToolName, "action": CodeActionRun, "reason": reason}})
	noteTask(t, "Acción code.run denegada (%s)", reason)
}

// handleCodeRun turns a code.run tool request into an approval and, once a
// human approves, into an execution in the sandbox.
func (o *Orchestrator) handleCodeRun(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, tr ToolRequest, idx int, reqs []ToolRequest) Outcome {
	switch {
	case o.code == nil:
		o.codeDenied(ctx, t, agent, "code_execution_disabled")
		return OutcomeDone
	case !slices.Contains(agent.Tools, CodeToolName):
		o.codeDenied(ctx, t, agent, "code_tool_not_granted") // the tool must be granted to the agent
		return OutcomeDone
	case o.legacyBlocked(ctx, rs, t, agent, tr): // per-tool kill switch, read-only mode (also "no external actions" of the request)
		return OutcomeDone
	}
	if g := o.conn.guard; g != nil {
		if v := g.Admit(ctx, o.org(ctx), agent.ID); !v.Allowed {
			o.codeDenied(ctx, t, agent, v.Code)
			return OutcomeDone
		}
	}
	tr.Risk = "high"
	pv := o.decideTool(ctx, rs, t, agent, tr)
	if pv.Denied() { // a deny rule still denies; an "allow" is IGNORED: approval is unconditional
		o.policyDenied(ctx, t, agent, tr, pv, false)
		return OutcomeDone
	}
	org := o.org(ctx)
	apID := newID()
	plan, err := o.code.Prepare(ctx, CodeRunInput{Org: org, AgentID: agent.ID, TaskID: t.ID, RequestID: rs.req.ID, OnBehalfOf: ActorFrom(ctx, ""),
		ApprovalID: apID, Args: tr.Args})
	if err != nil {
		o.codeDenied(ctx, t, agent, codeErrCode(err))
		return OutcomeDone
	}
	ap := domain.Approval{ID: apID, TaskID: t.ID, AgentID: agent.ID, Action: codeApprovalAction, Risk: "high", Title: plan.Title, Details: plan.Details,
		Context: plan.Card}
	governApproval(&ap, rs, pv)
	t.Status = domain.TaskAwaitingApproval
	if err := o.store.UpdateTask(ctx, org, *t); err != nil {
		o.code.Abandon(ctx, org, apID, "task_update_failed")
		o.failTask(ctx, rs, *t, err)
		return OutcomeFailed
	}
	ap, ch, err := o.approvals.Request(ctx, ap)
	if err != nil {
		o.code.Abandon(ctx, org, apID, "approval_unavailable")
		o.failTask(ctx, rs, *t, err)
		return OutcomeFailed
	}
	o.saveCheckpoint(ctx, rs, *t, reqs, idx, ap.ID, nil) // durable: the approval survives a restart
	return o.codeAwait(ctx, rs, t, agent, tr, ap, ch, o.approvals.Deadline(ap), plan.ArgsHash)
}

// codeErrCode extracts the stable code of a Prepare error.
func codeErrCode(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ":"); i > 0 {
		msg = msg[:i]
	}
	return msg
}

// codeAwait waits for the human decision and executes the run once approved.
// It is also the resume point of a task interrupted by a restart (argsHash is
// "" then: the stored plan, the one the human saw, is what runs).
func (o *Orchestrator) codeAwait(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, tr ToolRequest, ap domain.Approval,
	ch <-chan domain.Approval, deadline time.Time, argsHash string) Outcome {
	org := o.org(ctx)
	tid := t.ID
	o.setRequestStatus(ctx, rs, domain.RequestAwaitingApproval)
	o.setState(ctx, agent.ID, domain.StateAwaitingApproval, "Esperando aprobación: "+ap.Title, &tid, 80)
	o.emitMetrics(ctx)
	resume := YieldSlot(ctx) // a task waiting for a human does not hold a scheduler slot
	decision, err := o.awaitApproval(ctx, rs, ch, ap, deadline)
	resume()
	if err != nil {
		return OutcomeFailed // cancelled
	}
	o.setRequestStatus(ctx, rs, domain.RequestRunning)
	approved := decision.Status == domain.ApprovalApproved
	reason := "rejected"
	if approved && o.policy != nil {
		// The rules may have changed while the approval waited: re-validate.
		if rv := o.policy.Recheck(ctx, o.policyInput(rs, agent, tr), decision.ID, approverIDs(decision)); rv.Denied() {
			approved, reason = false, "policy:"+rv.RuleID
			o.policyDenied(ctx, t, agent, tr, rv, false)
		}
	}
	if !approved {
		o.code.Abandon(ctx, org, ap.ID, reason)
		fin := time.Now().UTC()
		t.Status, t.FinishedAt = domain.TaskBlocked, &fin
		if err := o.store.UpdateTask(ctx, org, *t); err != nil {
			o.log.Warn("update task", "err", err)
		}
		o.setState(ctx, agent.ID, domain.StateBlocked, "Acción rechazada: "+ap.Title, &tid, 80)
		o.rec.Emit(ctx, Action{Type: domain.EvTaskBlocked, AgentID: agent.ID, Entity: "task", EntityID: t.ID,
			Payload: map[string]any{"task": *t}, Text: fmt.Sprintf("Tarea bloqueada (aprobación rechazada): %s", t.Title)})
		o.emitMetrics(ctx)
		return OutcomeBlocked
	}
	t.Status = domain.TaskRunning
	// At most once: mark the checkpoint, then claim the approval id in the
	// execution ledger (any process). The sandbox also starts a run only once.
	o.markExecuted(ctx, t.ID, decision.ID)
	hash := argsHash
	if hash == "" {
		hash = argsHashOf(tr)
	}
	if ok, why := o.claimExecution(ctx, *t, decision.ID, tr, hash); !ok {
		o.code.Abandon(ctx, org, ap.ID, why)
		o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.execution_skipped", Entity: "task", EntityID: t.ID,
			Details: map[string]any{"tool": CodeToolName, "action": CodeActionRun, "approval_id": decision.ID, "reason": why}})
		noteTask(t, "code.run: la acción aprobada ya se había ejecutado")
		return OutcomeDone
	}
	o.setState(ctx, agent.ID, domain.StateWorking, "Ejecutando código aprobado: "+t.Title, &tid, 85)
	out := o.code.Execute(ctx, org, CodeExec{ApprovalID: decision.ID, ArgsHash: argsHash, Approvers: approverIDs(decision)})
	o.finishExecution(ctx, decision.ID, out.Status)
	if out.Status == "denied" {
		o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.denied", Entity: "task", EntityID: t.ID,
			Details: map[string]any{"tool": CodeToolName, "action": CodeActionRun, "reason": out.DenyReason, "approval_id": decision.ID}})
		noteTask(t, "Código aprobado pero no ejecutado (%s)", out.DenyReason)
	} else {
		o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.executed", Entity: "task", EntityID: t.ID,
			Details: map[string]any{"tool": CodeToolName, "action": CodeActionRun, "status": out.Status, "approval_id": decision.ID,
				"approvers": approverIDs(decision), "exit_code": out.ExitCode, "outputs": out.OutputRefs}})
		noteTask(t, "code.run: %s", out.Summary)
		o.codeFollowUp(ctx, rs, t, agent, out)
	}
	o.setState(ctx, agent.ID, domain.StateWorking, "Código ejecutado, finalizando: "+t.Title, &tid, 90)
	return OutcomeDone
}

// codeFollowUp hands the (redacted, delimited) result of the run to the agent
// as external content, in ONE extra runtime call, so it can use the result in
// its output. Tool requests of that call are not executed. A failure keeps the
// original output (with the evidence line of the run).
func (o *Orchestrator) codeFollowUp(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, out CodeOutcome) {
	if len(out.Blocks) == 0 {
		return
	}
	in := o.buildRunRequest(ctx, rs, *t, agent)
	in.ExternalContent = append(append([]string{}, in.ExternalContent...), out.Blocks...)
	res, err := o.reserveOrPause(ctx, rs, agent.ID, t.ID, domain.UsageRunTask)
	if err != nil {
		return
	}
	var next RunTaskResponse
	err = o.call(ctx, "run-task", func(c context.Context) (err error) {
		next, err = o.rt.RunTask(c, in)
		return err
	})
	if err != nil {
		res.Release()
		noteTask(t, "code.run: no pude entregar el resultado al agente (%v)", err)
		return
	}
	o.recordUsage(ctx, rs, t.ID, agent.ID, domain.UsageRunTask, next.Usage, next.ToolRequests, res)
	next.Output.Normalize()
	if t.Output != nil {
		next.Output.Evidence = append(append([]string{}, t.Output.Evidence...), next.Output.Evidence...)
	}
	if len(next.ToolRequests) > 0 {
		next.Output.Evidence = append(next.Output.Evidence, fmt.Sprintf("code.run: %d solicitud(es) de herramientas tras el resultado no se ejecutaron", len(next.ToolRequests)))
	}
	t.Output = &next.Output
}
