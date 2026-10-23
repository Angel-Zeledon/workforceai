package gateway

import (
	"context"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/connections"
	"aiworkforce/backend/internal/domain"
)

// Rehearsal enforcement (B9, docs/architecture/rehearsal.md). This is the
// barrier that makes it impossible for a rehearsal to reach the outside world:
// a side-effect call that belongs to a rehearsal is answered with what WOULD
// have happened (same controls, grants, limits, secret scan and approval
// reasons as a real call) and is never executed, scheduled, held or put in the
// outbox, approved or not. The check fails closed: when the rehearsal policy
// cannot answer, the call is treated as rehearsed.

// CodeRehearsal is the deny code of an outbox approval or a held send stopped
// because the agent (or its request) is in rehearsal.
const CodeRehearsal = "rehearsal_mode"

// rehearsing reports whether c must not execute a side effect.
func (g *Gateway) rehearsing(ctx context.Context, c application.GatewayCall) bool {
	if c.Rehearsal {
		return true
	}
	if g.Rehearsal == nil {
		return false
	}
	on, err := g.Rehearsal.Rehearsing(ctx, c.Org, c.AgentID, c.RequestID)
	if err != nil {
		g.Log.Warn("gateway: rehearsal state unknown, refusing the write", "agent", c.AgentID, "err", err)
		return true // fail closed
	}
	return on
}

func (g *Gateway) rehearsed(ctx context.Context, c application.GatewayCall, p application.RehearsalPreview, connID string) application.GatewayOutcome {
	if p.Recipients == nil {
		p.Recipients = []string{}
	}
	if p.Flags == nil {
		p.Flags = []string{}
	}
	g.Audit(ctx, c.Org, c.AgentID, "tool.rehearsed", "tool", c.Tool, map[string]any{"tool": c.Tool, "action": strings.ToLower(c.Action),
		"would_decision": p.WouldDecision, "reason": p.DenyReason, "task_id": c.TaskID, "approved": c.ApprovedArgsHash != "", "recipients": len(p.Recipients)})
	return application.GatewayOutcome{Decision: application.DecisionRehearsed, Status: "not_executed", ConnectionID: connID, Rehearsal: &p,
		Summary: "rehearsal: not executed (would be " + p.WouldDecision + ")"}
}

// rehearseConnection previews a connection-backed write. Pure: no usage row,
// no outbox entry, no hold, no anomaly counters.
func (g *Gateway) rehearseConnection(ctx context.Context, c application.GatewayCall, t target) application.GatewayOutcome {
	p := application.RehearsalPreview{Risk: t.spec.Risk, Reversibility: t.spec.Reversibility}
	deny := func(code, connID string) application.GatewayOutcome {
		p.WouldDecision, p.DenyReason = domain.WouldDeny, code
		return g.rehearsed(ctx, c, p, connID)
	}
	if v := g.Controls.CheckTool(ctx, c.Org, c.AgentID, c.Tool, c.Action, true); !v.Allowed {
		return deny(v.Code, "")
	}
	cd, code := g.resolve(ctx, c, t)
	if code != "" {
		return deny(code, "")
	}
	p.Account = cd.conn.AccountLabel
	if code := g.validate(ctx, c, t, cd, false); code != "" {
		return deny(code, cd.conn.ID)
	}
	if g.hasSecret(c.Args) {
		return deny(connections.CodeSecretDetected, cd.conn.ID)
	}
	rcpt := recipientsOf(c.Args)
	p.Recipients = rcpt
	if reasons := g.approvalReasons(c, t, cd, rcpt); len(reasons) > 0 {
		card := g.buildPending(c, t, cd, rcpt, reasons)
		p.WouldDecision, p.Flags, p.Reversibility = domain.WouldApprove, card.Flags, card.Reversibility
		return g.rehearsed(ctx, c, p, cd.conn.ID)
	}
	p.WouldDecision = domain.WouldAllow
	return g.rehearsed(ctx, c, p, cd.conn.ID)
}

// rehearseNative previews a native write (Assess is pure; Run is never called).
func (g *Gateway) rehearseNative(ctx context.Context, c application.GatewayCall, nt application.NativeTool) application.GatewayOutcome {
	p := application.RehearsalPreview{}
	deny := func(code string) application.GatewayOutcome {
		p.WouldDecision, p.DenyReason = domain.WouldDeny, code
		return g.rehearsed(ctx, c, p, "")
	}
	if v := g.Controls.CheckTool(ctx, c.Org, c.AgentID, c.Tool, c.Action, true); !v.Allowed {
		return deny(v.Code)
	}
	if c.ReadOnlyRequest {
		return deny(connections.CodeReadOnlyMode)
	}
	as := nt.Assess(ctx, c)
	p.Risk, p.Reversibility, p.Account, p.Recipients = as.Pending.Risk, as.Pending.Reversibility, as.Pending.Account, as.Pending.Recipients
	if as.DenyCode != "" {
		return deny(as.DenyCode)
	}
	if g.hasSecret(c.Args) {
		return deny(connections.CodeSecretDetected)
	}
	needs, flags := as.NeedsApproval, append([]string(nil), as.Pending.Flags...)
	if c.InjectionSuspected {
		needs, flags = true, appendOnce(flags, "injection_suspected")
	}
	switch effectiveAutonomy(c.Autonomy, "") {
	case "suggest", "approve_each":
		needs, flags = true, appendOnce(flags, "autonomy")
	}
	p.Flags = flags
	if p.Risk == "" {
		p.Risk = "medium"
	}
	if needs {
		p.WouldDecision = domain.WouldApprove
	} else {
		p.WouldDecision = domain.WouldAllow
	}
	return g.rehearsed(ctx, c, p, "")
}
