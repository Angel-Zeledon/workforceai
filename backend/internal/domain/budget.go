package domain

import "time"

// Extra request states used by cost control (additive to docs/SPEC.md).
const (
	// RequestAwaitingConfirmation: the plan exists, the cost estimate exceeded a
	// threshold and the user must confirm before any task runs.
	RequestAwaitingConfirmation RequestStatus = "awaiting_confirmation"
	// RequestPaused: a hard budget cap was reached; resumable by raising the cap.
	RequestPaused RequestStatus = "paused"
)

// Event types added by cost control (additive WS contract).
const (
	EvCostEstimated  = "cost.estimated"
	EvRequestStatus  = "request.status_changed"
	EvBudgetWarning  = "budget.warning"
	EvBudgetExceeded = "budget.exceeded"
	EvBudgetResumed  = "budget.resumed"
)

// BudgetScope says which limit a budget event is about.
type BudgetScope string

const (
	ScopeOrg     BudgetScope = "org"
	ScopeRequest BudgetScope = "request"
	ScopeAgent   BudgetScope = "agent"
)

// UsageKind is the kind of runtime call a usage entry records.
type UsageKind string

const (
	UsageRunTask    UsageKind = "run_task"
	UsageConsult    UsageKind = "consult"
	UsageSynthesize UsageKind = "synthesize"
	// UsagePlan is a planner call (project planning); it belongs to no request.
	UsagePlan UsageKind = "plan"
)

// UsageEntry is one reconciled runtime call: the cost ledger row.
type UsageEntry struct {
	ID           string    `json:"id"`
	RequestID    string    `json:"request_id"`
	TaskID       string    `json:"task_id"`
	AgentID      string    `json:"agent_id"`
	Kind         UsageKind `json:"kind"`
	Model        string    `json:"model"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	CostUSD      float64   `json:"cost_usd"`
	// Tools are the tool requests produced by this call ("tool.action"); the
	// per-tool breakdown splits the call cost among them.
	Tools []string  `json:"tools"`
	TS    time.Time `json:"ts"`
}

// BudgetCap is a hard cap: scope "agent" (per-period) or "request".
type BudgetCap struct {
	Scope   BudgetScope `json:"scope"`
	ScopeID string      `json:"scope_id"`
	CapUSD  float64     `json:"cap_usd"`
}

// CostRange is a min..max estimate. A range, never a single made-up number.
type CostRange struct {
	MinUSD float64 `json:"min_usd"`
	MaxUSD float64 `json:"max_usd"`
}

// TaskEstimate is the range for one planned task.
type TaskEstimate struct {
	TaskID  string  `json:"task_id"`
	Title   string  `json:"title"`
	AgentID string  `json:"agent_id"`
	MinUSD  float64 `json:"min_usd"`
	MaxUSD  float64 `json:"max_usd"`
}

// Estimate bases / confirmation reasons (codes; the UI translates them).
const (
	BasisSimulation     = "simulation"
	BasisTokenHeuristic = "token_heuristic"
	BasisFallback       = "fallback" // runtime without /v1/estimate: coarse backend constants

	ConfirmReasonThreshold = "threshold" // total.max_usd above COST_CONFIRM_THRESHOLD_USD
	ConfirmReasonCap       = "cap"       // total.max_usd above the request budget cap
)

// CostEstimate is the pre-execution estimate of a request (after the plan).
type CostEstimate struct {
	RequestID string         `json:"request_id"`
	Tasks     []TaskEstimate `json:"tasks"`
	Synthesis CostRange      `json:"synthesis"`
	Total     CostRange      `json:"total"`
	Currency  string         `json:"currency"`
	Mode      string         `json:"mode"`
	Model     string         `json:"model"`
	Basis     string         `json:"basis"`
	// ThresholdUSD is the confirmation threshold in force (0 = disabled).
	ThresholdUSD float64 `json:"threshold_usd"`
	// BudgetCapUSD is the hard cap of the request (0 = none).
	BudgetCapUSD         float64 `json:"budget_cap_usd"`
	RequiresConfirmation bool    `json:"requires_confirmation"`
	ConfirmReason        string  `json:"confirm_reason,omitempty"`
}
