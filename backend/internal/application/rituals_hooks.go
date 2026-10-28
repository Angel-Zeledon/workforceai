package application

import "context"

// AdmitRitual is the execution guard as the rituals (B4, docs/architecture/rituals.md) read it: kill switch,
// controls availability, agent pause and operating hours. A stand-up or a retro has no external effects, so the
// caller treats read-only mode as allowed.
func (o *Orchestrator) AdmitRitual(ctx context.Context, agentID string) GuardVerdict {
	return o.chatAdmit(ctx, agentID)
}
