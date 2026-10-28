package domain

import "time"

// EventQuery filters the persisted event log (the events every Recorder.Emit saves). It is read by the
// away summary (docs/architecture/away-summary.md); From is inclusive and To exclusive.
type EventQuery struct {
	From  time.Time
	To    time.Time
	Types []string // exact type names; empty = every type
	Limit int      // at most this many rows (the store caps it)
	Desc  bool     // newest first (default: oldest first)
}

// MaxEventQuery is the largest page an EventQuery may return.
const MaxEventQuery = 5000

// SpendRow is the spend of one agent in a time window, summed from the cost ledger (usage entries).
// AgentID is "" for calls that belong to no agent (planning, synthesis).
type SpendRow struct {
	AgentID string  `json:"agent_id"`
	CostUSD float64 `json:"cost_usd"`
	Calls   int     `json:"calls"`
}
