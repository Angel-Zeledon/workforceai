package gateway

import (
	"context"
	"strings"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/connections"
)

// Native tools (application.NativeTool) are served by the gateway without a
// connection: same controls, same approval binding, same execution ledger and
// audit as a connection-backed tool, but the capability check is the tool's own
// (for the drive: its zones and the agent's tool labels).

// AddNative registers a native tool.
func (g *Gateway) AddNative(t application.NativeTool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.natives = append(g.natives, t)
}

func (g *Gateway) nativeFor(tool, action string) (application.NativeTool, bool, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, n := range g.natives {
		if strings.EqualFold(n.Name(), tool) {
			if side, ok := n.SideEffect(strings.ToLower(action)); ok {
				return n, side, true
			}
		}
	}
	return nil, false, false
}

func (g *Gateway) denyNative(ctx context.Context, c application.GatewayCall, code string) application.GatewayOutcome {
	g.Audit(ctx, c.Org, c.AgentID, "tool.native_denied", "tool", c.Tool, map[string]any{"tool": c.Tool, "action": c.Action, "reason": code, "task_id": c.TaskID})
	return application.GatewayOutcome{Decision: "denied", DenyReason: code}
}

// executeNative runs a native tool call.
func (g *Gateway) executeNative(ctx context.Context, c application.GatewayCall, nt application.NativeTool, side bool) application.GatewayOutcome {
	approved := c.ApprovedArgsHash != ""
	if !approved {
		g.Controls.ObserveToolRequest(ctx, c.Org, c.AgentID)
	}
	if approved {
		if prev, ok := g.approvedOutcome(c.ApprovalID); ok && c.ApprovalID != "" {
			return prev
		}
		if c.ApprovedArgsHash != ArgsHash(c.Tool, c.Action, c.Args) {
			return g.denyNative(ctx, c, CodeApprovalMismatch)
		}
	}
	// Controls first: kill switch, agent pause, read-only mode, per-tool switch.
	if v := g.Controls.CheckTool(ctx, c.Org, c.AgentID, c.Tool, c.Action, side); !v.Allowed {
		return g.denyNative(ctx, c, v.Code)
	}
	if side && c.ReadOnlyRequest {
		return g.denyNative(ctx, c, connections.CodeReadOnlyMode)
	}
	as := nt.Assess(ctx, c)
	if as.DenyCode != "" {
		return g.denyNative(ctx, c, as.DenyCode)
	}
	if side && g.hasSecret(c.Args) {
		g.Emit(ctx, c.Org, "security.alert", map[string]any{"kind": "secret_detected", "agent_id": c.AgentID, "tool": c.Tool, "action": c.Action})
		return g.denyNative(ctx, c, connections.CodeSecretDetected)
	}
	needs, flags := as.NeedsApproval, append([]string(nil), as.Pending.Flags...)
	if side {
		// A task that read something that looked like an injection never changes
		// the drive on its own; neither does an agent that must ask for everything.
		if c.InjectionSuspected {
			needs, flags = true, appendOnce(flags, "injection_suspected")
		}
		switch effectiveAutonomy(c.Autonomy, "") {
		case "suggest", "approve_each":
			needs, flags = true, appendOnce(flags, "autonomy")
		}
	}
	if needs && !approved {
		p := as.Pending
		p.Flags, p.ArgsHash, p.Tainted = flags, ArgsHash(c.Tool, c.Action, c.Args), c.Tainted
		if p.Risk == "" {
			p.Risk = "medium"
		}
		g.Audit(ctx, c.Org, c.AgentID, "tool.native_held", "tool", c.Tool, map[string]any{"tool": c.Tool, "action": c.Action, "flags": flags, "task_id": c.TaskID})
		return application.GatewayOutcome{Decision: "needs_approval", Pending: &p, Tainted: c.Tainted}
	}
	if approved && c.ApprovalID != "" && g.Executions != nil {
		claimed, _, err := g.Executions.ClaimExecution(ctx, c.Org, application.ApprovalExecution{ApprovalID: c.ApprovalID, TaskID: c.TaskID,
			Tool: c.Tool, Action: c.Action, ArgsHash: c.ApprovedArgsHash})
		if err != nil {
			g.Log.Warn("gateway: claim approved execution", "approval", c.ApprovalID, "err", err)
			return g.denyNative(ctx, c, CodeExecutionUnavailable)
		}
		if !claimed {
			return application.GatewayOutcome{Decision: "allowed", Status: application.StatusAlreadyExecuted, Summary: "already executed"}
		}
	}
	res := nt.Run(ctx, c, approved)
	out := application.GatewayOutcome{Decision: "allowed", Status: res.Status, Summary: res.Summary, Blocks: res.Blocks,
		Tainted: res.Tainted, InjectionSuspected: res.InjectionSuspected}
	if res.Status != "succeeded" {
		out.Status, out.DenyReason = "failed", res.Code
	}
	if res.InjectionSuspected {
		g.Emit(ctx, c.Org, "security.alert", map[string]any{"kind": "prompt_injection_suspected", "agent_id": c.AgentID, "tool": c.Tool})
		g.Audit(ctx, c.Org, c.AgentID, "security.injection_suspected", "tool", c.Tool, map[string]any{"tool": c.Tool, "task_id": c.TaskID})
	}
	if approved && c.ApprovalID != "" {
		if out.Status == "succeeded" {
			g.rememberApproved(c.ApprovalID, out)
		}
		if g.Executions != nil {
			st := out.Status
			if err := g.Executions.FinishExecution(ctx, c.Org, c.ApprovalID, st, ""); err != nil {
				g.Log.Warn("gateway: finish approved execution", "approval", c.ApprovalID, "err", err)
			}
		}
	}
	return out
}
