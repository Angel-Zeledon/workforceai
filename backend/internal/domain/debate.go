package domain

import "time"

// Team debate (B5, docs/architecture/team-debates.md): before a high-impact decision, 2 to 4 agents exchange a
// bounded number of short turns (positions, objections) and the assistant closes with a synthesis that names the
// recommended option and the dissent. The whole transcript is MODEL OUTPUT: untrusted, shown as such, and
// advisory. A debate never approves, rejects or executes anything: the decision it informs still goes through the
// normal approvals. One row per debate, scoped by organization.

// Debate triggers.
const (
	DebateTriggerPlanner = "planner_decision" // the planner flagged a task of the plan as a high-impact decision
	DebateTriggerUser    = "user_request"     // a person asked for it ("/debatir", [[debate]] marker or POST /debates)
)

// Turn kinds.
const (
	DebateTurnPosition  = "position"
	DebateTurnObjection = "objection"
	DebateTurnSynthesis = "synthesis"
)

// Debate statuses.
const (
	DebateDone   = "done"
	DebateFailed = "failed"
)

// DebateTurn is one short intervention. AgentID is a participant (positions, objections) or the assistant
// (the synthesis, always the last turn).
type DebateTurn struct {
	Seq     int    `json:"seq"`
	AgentID string `json:"agent_id"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
}

// DebateSynthesis is the assistant's closing: the option the debate leans to, why, and who disagreed and why.
// It is a recommendation for the human, never a decision.
type DebateSynthesis struct {
	Choice    string   `json:"choice"`
	Rationale string   `json:"rationale"`
	Dissent   []string `json:"dissent"`
}

type Debate struct {
	ID        string `json:"id"`
	Topic     string `json:"topic"`
	Trigger   string `json:"trigger"`
	RequestID string `json:"request_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	// Options the debaters weighed (given by the person, or proposed by the runtime when none was given).
	Options []string `json:"options"`
	// Participants are the debating agents (2..4), in speaking order; the assistant moderates and is not listed.
	Participants []string        `json:"participants"`
	Turns        []DebateTurn    `json:"turns"`
	Synthesis    DebateSynthesis `json:"synthesis"`
	Status       string          `json:"status"`
	// FailCode is set when Status is "failed" (unavailable, blocked, budget_exceeded).
	FailCode  string    `json:"fail_code,omitempty"`
	MaxTurns  int       `json:"max_turns"`
	Locale    string    `json:"locale,omitempty"`
	Model     string    `json:"model,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	CostUSD   float64   `json:"cost_usd"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Advisory is always true: the debate informs a decision, it never takes one.
	Advisory bool `json:"advisory"`
	// Untrusted is always true: every turn is model output and is shown as such.
	Untrusted bool `json:"untrusted"`
}
