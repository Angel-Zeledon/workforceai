package application

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/sanitize"
)

// This file wires the web tool into the orchestrator (docs/architecture/agent-web.md). A web request is a
// READ: it runs between runtime turns, like the connection reads, and its result goes back to the model only as
// delimited untrusted data; the task becomes tainted. It is never an action with an approval, but it passes
// every control: the agent grant, kill switch / pause, read-only mode, the deny rules of the policy, the
// organization setting and quotas (inside the runner) and the budget.

const (
	// UsageWeb is the ledger kind of the paid part of web calls (search provider requests).
	UsageWeb domain.UsageKind = "web"

	maxWebPerRound = 4
	webRounds      = 3
)

func isWebTool(tr ToolRequest) bool { return strings.EqualFold(strings.TrimSpace(tr.Tool), WebToolName) }

// webGate applies the controls that do not depend on the organization's web setting. "" means allowed.
func (o *Orchestrator) webGate(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, tr ToolRequest) string {
	org := o.org(ctx)
	switch {
	case o.web == nil:
		return "web_unavailable"
	case !slices.Contains(agent.Tools, WebToolName):
		return "web_tool_not_granted"
	}
	if g := o.conn.guard; g != nil {
		if v := g.Admit(ctx, org, agent.ID); !v.Allowed {
			return v.Code
		}
		if g.ToolBlocked(ctx, org, WebToolName, tr.Action) {
			return "tool_disabled"
		}
		// Read-only mode also stops the web: nothing leaves the company while the organization is read-only.
		if rs.readOnlyRequest() || g.SideEffectsBlocked(ctx, org, agent.ID) {
			return "read_only_mode"
		}
	}
	tr.Risk = "low"
	pv := o.decideTool(ctx, rs, t, agent, tr)
	if pv.Denied() {
		return "policy:" + pv.RuleID
	}
	if pv.NeedsApproval() && !strings.HasPrefix(pv.RuleID, "baseline") && pv.RuleID != "" {
		return "policy_requires_approval:" + pv.RuleID // an org rule asks for approval; reads have none: fail closed
	}
	return ""
}

// webReads serves the web requests of a task between runtime turns and hands the (untrusted) results to the
// runtime in a follow-up call. Other tool requests are left in the response untouched.
func (o *Orchestrator) webReads(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, in RunTaskRequest, resp RunTaskResponse) RunTaskResponse {
	var notes []string
	fin := func() RunTaskResponse {
		resp.Output.Evidence = append(resp.Output.Evidence, notes...)
		return resp
	}
	org := o.org(ctx)
	for round := 0; round < webRounds; round++ {
		var reads, rest []ToolRequest
		for _, tr := range resp.ToolRequests {
			if isWebTool(tr) {
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
		deny := func(tr ToolRequest, reason, notice string) {
			o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.denied", Entity: "task", EntityID: t.ID,
				Details: map[string]any{"tool": WebToolName, "action": tr.Action, "reason": reason}})
			notes = append(notes, fmt.Sprintf("web.%s no disponible (%s)", tr.Action, reason))
			if notice == "" {
				notice = "web." + tr.Action + " was not executed: " + reason
			}
			blocks = append(blocks, sanitize.Wrap("notice", "web."+tr.Action, "backend", notice))
		}
		for i, tr := range reads {
			if i >= maxWebPerRound {
				break
			}
			if reason := o.webGate(ctx, rs, t, agent, tr); reason != "" {
				deny(tr, reason, "")
				continue
			}
			out := o.web.Run(ctx, WebCall{Org: org, AgentID: agent.ID, TaskID: t.ID, Action: strings.ToLower(strings.TrimSpace(tr.Action)), Args: tr.Args})
			if out.CostUSD > 0 {
				entry := domain.UsageEntry{TaskID: t.ID, AgentID: agent.ID, Kind: UsageWeb, Tools: []string{"web." + tr.Action}}
				if _, err := o.budget.Record(ctx, rs.req.ID, entry, out.CostUSD); err != nil {
					o.log.Warn("record web cost", "err", err)
				}
			}
			if out.Status == "ok" {
				blocks = append(blocks, out.Block)
				rs.ext.mu.Lock()
				taint.tainted = taint.tainted || out.Tainted
				taint.suspected = taint.suspected || out.InjectionSuspected
				rs.ext.mu.Unlock()
				o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.executed", Entity: "task", EntityID: t.ID,
					Details: map[string]any{"tool": WebToolName, "action": tr.Action, "status": "succeeded", "summary": out.Summary}})
				continue
			}
			reason := out.DenyReason
			if reason == "" {
				reason = out.Status
			}
			deny(tr, reason, out.Notice)
		}
		if len(reads) > maxWebPerRound {
			notes = append(notes, fmt.Sprintf("web: %d solicitudes de más se descartaron", len(reads)-maxWebPerRound))
		}
		if len(blocks) == 0 {
			resp.ToolRequests = rest
			return fin()
		}
		in2 := in
		in2.ExternalContent = append(append([]string{}, in.ExternalContent...), blocks...)
		in2.LoadedSkills = append([]TrustedBlock{}, in.LoadedSkills...)
		res, err := o.reserveOrPause(ctx, rs, agent.ID, t.ID, domain.UsageRunTask)
		if err != nil {
			resp.ToolRequests = rest
			return fin()
		}
		var next RunTaskResponse
		err = o.call(ctx, "run-task", func(c context.Context) (err error) {
			next, err = o.rtRunTask(c, rs, "web-followup", in2)
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
	// Web requests left after the last round are dropped (the model asked for too much).
	var keep []ToolRequest
	for _, tr := range resp.ToolRequests {
		if !isWebTool(tr) {
			keep = append(keep, tr)
		}
	}
	resp.ToolRequests = keep
	return fin()
}

// webSkipped records a web request that reached the action loop (it is only ever served by webReads).
func (o *Orchestrator) webSkipped(ctx context.Context, t *domain.Task, agent domain.Agent, tr ToolRequest) {
	o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: "tool.read_skipped", Entity: "task", EntityID: t.ID,
		Details: map[string]any{"tool": WebToolName, "action": tr.Action}})
}
