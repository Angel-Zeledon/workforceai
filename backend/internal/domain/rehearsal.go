package domain

import "time"

// Rehearsal (B9, "Ensayo general"): agents show what they WOULD have sent or paid without doing it, so a person can
// validate their judgement before giving them more autonomy. Spec: docs/architecture/rehearsal.md.

// AgentRehearsal is the per-agent rehearsal switch. While Enabled, no side-effect tool call of the agent is executed
// (the Tool Gateway refuses it, fail closed); each one is recorded as a RehearsalAction instead. Reads still run.
type AgentRehearsal struct {
	AgentID   string    `json:"agent_id"`
	Enabled   bool      `json:"enabled"`
	SetBy     string    `json:"set_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Would-decisions of a rehearsed action: what the real run would have done with it.
const (
	WouldAllow   = "allowed"        // it would have run without asking anyone
	WouldApprove = "needs_approval" // it would have waited for a human approval
	WouldDeny    = "denied"         // a control or rule would have refused it
)

// Human verdicts on a rehearsed action (never set by an agent).
const (
	VerdictWouldApprove = "would_approve"
	VerdictWouldReject  = "would_reject"
)

// RehearsalAction is one "would have done" entry: a side-effect tool call that a rehearsal recorded and did NOT
// execute. It holds metadata and a short sanitized preview of the arguments (secrets redacted, strings truncated),
// never a credential.
type RehearsalAction struct {
	ID        string `json:"id"`
	RequestID string `json:"request_id"`
	TaskID    string `json:"task_id"`
	AgentID   string `json:"agent_id"`
	Tool      string `json:"tool"`
	Action    string `json:"action"`
	// Source says which path recorded it: gateway (connection or native tool), simulated (legacy tool) or code (sandbox run).
	Source string `json:"source"`
	// Scope is why it was rehearsed: request (the request ran in rehearsal) or agent (the agent's switch).
	Scope         string            `json:"scope"`
	WouldDecision string            `json:"would_decision"`
	DenyReason    string            `json:"deny_reason,omitempty"`
	Risk          string            `json:"risk"`
	Reversibility string            `json:"reversibility,omitempty"`
	Account       string            `json:"account,omitempty"`
	Recipients    []string          `json:"recipients"`
	Amount        *float64          `json:"amount,omitempty"`
	Flags         []string          `json:"flags"`
	Preview       map[string]string `json:"preview"`
	ArgsHash      string            `json:"args_hash"`
	Verdict       string            `json:"verdict"`
	VerdictBy     string            `json:"verdict_by,omitempty"`
	VerdictAt     *time.Time        `json:"verdict_at,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
}

// RehearsalQuery filters rehearsed actions (empty fields match everything). Newest first.
type RehearsalQuery struct {
	RequestID string
	AgentID   string
	Limit     int
}
