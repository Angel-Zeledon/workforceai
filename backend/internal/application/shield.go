package application

import (
	"context"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// Visible shield (B10, docs/architecture/shield.md). When content an agent read
// looks like it tries to give the agent orders (prompt injection), the office
// shows it and the audit trail records it. The Tool Gateway does this for the
// reads it serves (gateway.injectionAlert); this is the same signal for the
// reads the orchestrator serves itself (the web tool).
//
// What the signal carries is ids only (agent, task, request, tool, action): the
// third-party text is never copied into the event or the audit entry. The text
// already went to the runtime as delimited untrusted data, and the task is
// marked suspected, so every write it asks for needs a human.

// EvSecurityAlert is the realtime event of a security signal (03-eventos-realtime.md).
const EvSecurityAlert = "security.alert"

// AuditInjectionSuspected is the audit action the security log lists.
const AuditInjectionSuspected = "security.injection_suspected"

func (o *Orchestrator) injectionAlert(ctx context.Context, rs *run, t *domain.Task, agent domain.Agent, tool, action string) {
	action = strings.ToLower(strings.TrimSpace(action))
	o.rec.Emit(ctx, Action{Type: EvSecurityAlert, AgentID: agent.ID, Entity: "task", EntityID: t.ID, SkipAudit: true,
		Payload: map[string]any{"kind": "prompt_injection_suspected", "agent_id": agent.ID, "tool": tool, "action": action,
			"task_id": t.ID, "request_id": rs.req.ID, "source": tool, "treated_as_data": true}})
	o.rec.Audit(ctx, domain.AuditLog{Actor: agent.ID, Action: AuditInjectionSuspected, Entity: "tool", EntityID: tool,
		Details: map[string]any{"tool": tool, "action": action, "task_id": t.ID, "request_id": rs.req.ID, "source": tool, "treated_as_data": true}})
}
