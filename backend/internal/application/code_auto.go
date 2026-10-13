package application

import (
	"context"
	"slices"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// Desk mode of the code sandbox (docs/architecture/code-sandbox.md, "Desk
// mode"). OFF by default. When an organization opts in, a code.run whose every
// input lives in the requesting agent's own files may start without a human
// approval. Everything that decides it is computed here from backend data (the
// agent id, the task, the drive, the conversation) and re-checked by the sandbox
// at Prepare and Execute time; nothing is taken from the runtime. Anything that
// does not qualify takes the normal approval path of code_flow.go.

// AutoRunDesk is the Settings.AutoRun value that enables desk mode.
const AutoRunDesk = "desk"

// AutoApprover is recorded as the "approver" of a run that started in desk mode.
const AutoApprover = "auto:desk"

// CodeScope is where a desk-mode run may read from. Paths are canonical drive
// paths of the organization.
type CodeScope struct {
	AgentID, TaskID string
	// Desk is the agent's own desk (/Agents/<agent>); Project the folder of the
	// project the task belongs to ("" outside a project).
	Desk, Project string
	// Attached are the files the user attached in the current conversation.
	Attached []CodeAttachment
	// PolicyClean is false when governance asks for more than the baseline
	// approval (a required role, double approval, no self-approval) or the
	// agent's autonomy says a human decides everything: no auto-run then.
	PolicyClean bool
}

// CodeAttachment is a user attachment of the conversation (by path and id).
type CodeAttachment struct{ Path, FileID string }

// CodeAutoModer is the optional capability of a CodeRunner that reports the
// organization's mode ("off" | "desk"). Without it desk mode does not exist.
type CodeAutoModer interface {
	AutoRunMode(ctx context.Context, org string) string
}

// codeAutoMode is the organization's mode ("off" when unknown or on any error).
func (o *Orchestrator) codeAutoMode(ctx context.Context) string {
	if m, ok := o.code.(CodeAutoModer); ok {
		return m.AutoRunMode(ctx, o.org(ctx))
	}
	return "off"
}

// codeAutoRunHint is the mode told to the runtime: "desk" only for an agent that
// holds the code tool in an organization that opted in; "" otherwise (the
// default payload does not change).
func (o *Orchestrator) codeAutoRunHint(ctx context.Context, agent domain.Agent) string {
	if o.code == nil || !slices.Contains(agent.Tools, CodeToolName) || o.codeAutoMode(ctx) != AutoRunDesk {
		return ""
	}
	return AutoRunDesk
}

// maxAttachScan is how many recent messages of the conversation are searched
// for the user's attachments.
const maxAttachScan = 200

// codeScope builds the scope of a request. It returns nil, with no extra work,
// unless the organization is in desk mode.
func (o *Orchestrator) codeScope(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, pv PolicyVerdict) *CodeScope {
	if o.code == nil || o.codeAutoMode(ctx) != AutoRunDesk {
		return nil
	}
	org := o.org(ctx)
	sc := &CodeScope{AgentID: agent.ID, TaskID: t.ID}
	sc.PolicyClean = !pv.Denied() && pv.RequiredRole == "" && pv.RequiredApprovals <= 1 && !pv.NoSelfApproval &&
		(pv.RuleID == "" || pv.RuleID == "baseline" || strings.HasPrefix(pv.RuleID, "baseline.")) &&
		agent.Autonomy != "suggest" && agent.Autonomy != "approve_each"
	if o.wsSrc != nil {
		if ws := o.wsSrc.Workspace(ctx, org, agent.ID, rs.req.ID); ws != nil {
			sc.Desk, sc.Project = ws.Desk, ws.Project
		}
	}
	if sc.Desk == "" {
		sc.Desk = "/Agents/" + agent.ID
	}
	if l := rs.chat; l != nil {
		sc.Attached = o.conversationAttachments(ctx, org, l.conv)
	}
	return sc
}

// conversationAttachments lists the files the USER attached in a conversation
// (recent messages only). Files the agents wrote are never included.
func (o *Orchestrator) conversationAttachments(ctx context.Context, org, conv string) []CodeAttachment {
	var msgs []domain.Message
	var err error
	if p, ok := o.store.(MessagePager); ok {
		msgs, _, err = p.ListMessagesPage(ctx, org, conv, "", maxAttachScan)
	} else {
		msgs, err = o.store.ListMessages(ctx, org, conv)
		if len(msgs) > maxAttachScan {
			msgs = msgs[len(msgs)-maxAttachScan:]
		}
	}
	if err != nil {
		return nil
	}
	var out []CodeAttachment
	for _, m := range msgs {
		if m.From != "user" {
			continue
		}
		for _, f := range m.Files {
			if f.Action == "attach" {
				out = append(out, CodeAttachment{Path: f.Path, FileID: f.FileID})
			}
		}
	}
	return out
}

// codeRunAuto runs a plan that qualified for desk mode. The run was recorded by
// Prepare under apID (an id no approval has: nobody can approve or replay it);
// the at-most-once ledger and the sandbox's compare-and-set still apply.
func (o *Orchestrator) codeRunAuto(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, tr ToolRequest, apID string, plan CodePlan, scope *CodeScope) Outcome {
	org := o.org(ctx)
	tid := t.ID
	o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.auto_approved", Entity: "task", EntityID: t.ID,
		Details: map[string]any{"tool": CodeToolName, "action": CodeActionRun, "auto_run": true, "mode": AutoRunDesk, "run_id": plan.RunID,
			"approval_id": apID, "args_hash": plan.ArgsHash, "code_hash": plan.Card["code_hash"]}})
	t.Status = domain.TaskRunning
	o.markExecuted(ctx, t.ID, apID)
	if ok, why := o.claimExecution(ctx, *t, apID, tr, plan.ArgsHash); !ok {
		o.code.Abandon(ctx, org, apID, why)
		o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.execution_skipped", Entity: "task", EntityID: t.ID,
			Details: map[string]any{"tool": CodeToolName, "action": CodeActionRun, "approval_id": apID, "reason": why, "auto_run": true}})
		noteTask(t, "code.run: la acción ya se había ejecutado")
		return OutcomeDone
	}
	o.setState(ctx, agent.ID, domain.StateWorking, "Ejecutando código en su escritorio (sin aprobación, modo escritorio): "+t.Title, &tid, 85)
	out := o.code.Execute(ctx, org, CodeExec{ApprovalID: apID, ArgsHash: plan.ArgsHash, Approvers: []string{AutoApprover}, Scope: scope})
	return o.codeFinish(ctx, rs, t, agent, out, apID, []string{AutoApprover}, true)
}
