package application

import (
	"context"

	"aiworkforce/backend/internal/domain"
)

// Budget of a skill test run (F8): one agent call that belongs to no request. It counts against the agent cap and the
// organization limit exactly like a chat reply (the ledger is the source of truth), sized from the real payload.

// EstimateAgentCall is the worst-case cost of one run-task call with this payload, the number shown to the person
// BEFORE they press "Probar".
func (o *Orchestrator) EstimateAgentCall(payload any) float64 {
	return callEstimateUSD("", domain.UsageRunTask, payload)
}

// ReserveAgentCall holds budget for one agent call outside any request. A non-nil Exceeded means it must not run.
func (o *Orchestrator) ReserveAgentCall(ctx context.Context, agentID string, payload any) (*Reservation, *Exceeded) {
	return o.chatReserve(ctx, agentID, domain.UsageRunTask, payload)
}

// RecordAgentCall writes the real usage of the call to the ledger and releases its reservation (also when the call
// failed: it records nothing then).
func (o *Orchestrator) RecordAgentCall(ctx context.Context, agentID string, u Usage, res *Reservation) {
	o.chatRecordUsage(ctx, agentID, u, res)
}
