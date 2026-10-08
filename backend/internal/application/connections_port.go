package application

import (
	"context"
	"time"
)

// Ports of the connections/controls feature (docs/architecture/
// integrations-credentials.md). The orchestrator depends only on these
// interfaces; the implementations live in internal/gateway and internal/controls.
// All of them are optional: with none set the orchestrator behaves exactly as
// before (simulation, tool requests recorded as simulated).

// GuardVerdict is the answer of an enforcement-point check.
type GuardVerdict struct {
	Allowed bool
	Code    string // kill_switch_active | agent_paused | read_only_mode | controls_unavailable
}

// ExecutionGuard enforces the kill switch, agent pause and read-only mode at
// the orchestrator's choke points (task start, before runtime calls, before
// tool calls). Implementations must fail closed.
type ExecutionGuard interface {
	// Admit: may agentID start/continue work? (kill switch, pause, drain)
	Admit(ctx context.Context, org, agentID string) GuardVerdict
	// SideEffectsBlocked reports whether side-effect tools are denied for the
	// agent by read-only mode (org or agent). Errors block.
	SideEffectsBlocked(ctx context.Context, org, agentID string) bool
	// ToolBlocked reports whether the per-tool kill switch disabled tool(.action).
	ToolBlocked(ctx context.Context, org, tool, action string) bool
	// IsSideEffect classifies a tool action (unknown actions fail closed: true).
	IsSideEffect(tool, action string) bool
}

// AnomalyObserver receives the signals of the anomaly detection rules
// (implemented by controls.Service). Optional: nil observers are skipped.
type AnomalyObserver interface {
	ObserveSpend(ctx context.Context, org string, usd float64)
	ObserveApprovalRejected(ctx context.Context, org, agentID string)
}

// GatewayRoute says how a tool request is served.
type GatewayRoute int

const (
	// RouteNone: the tool is not backed by a connection; use the legacy path.
	RouteNone GatewayRoute = iota
	// RouteRead: a connection-backed read (executed between runtime turns).
	RouteRead
	// RouteWrite: a connection-backed action with side effects.
	RouteWrite
)

// GatewayCall is a tool request on its way through the Tool Gateway.
type GatewayCall struct {
	Org, AgentID, TaskID, RequestID, OnBehalfOf string
	Tool, Action                                string
	Args                                        map[string]any
	// Autonomy is the agent's autonomy level (suggest|approve_each|rules|autonomous).
	Autonomy string
	// Tainted: the task already read third-party content.
	Tainted bool
	// ReadConnections are the connection ids already read in this task.
	ReadConnections []string
	// InjectionSuspected: a read in this task looked like prompt injection.
	InjectionSuspected bool
	// ReadOnlyRequest: the human marked this request "no external actions".
	ReadOnlyRequest bool
	ApprovalID      string
	// ApprovedArgsHash is set when executing after a human approval; it must
	// equal the hash of the call's tool/action/args.
	ApprovedArgsHash string
}

// GatewayPending describes what a human must approve (structured, no secrets).
type GatewayPending struct {
	ArgsHash       string   `json:"args_hash"`
	Title          string   `json:"title"`
	Details        string   `json:"details"`
	Risk           string   `json:"risk"`
	Reversibility  string   `json:"reversibility"`
	Account        string   `json:"account"`
	Recipients     []string `json:"recipients"`
	ExternalOrigin bool     `json:"external_origin"`
	Tainted        bool     `json:"tainted"`
	HoldSeconds    int      `json:"hold_seconds"`
	Flags          []string `json:"flags"`
	// OutboxID is the id of the outbox item that represents this action.
	OutboxID string `json:"outbox_id,omitempty"`
}

// GatewayOutcome is the redacted result handed back to the orchestrator.
type GatewayOutcome struct {
	Decision   string // allowed | needs_approval | denied
	DenyReason string // stable code
	Status     string // succeeded | failed | scheduled
	Summary    string
	// Blocks are sanitized, delimited <untrusted_data> blocks for the runtime.
	Blocks             []string
	Tainted            bool
	InjectionSuspected bool
	// ConnectionID is internal: it is never forwarded to the runtime.
	ConnectionID string
	Pending      *GatewayPending
	HoldID       string
	HoldUntil    *time.Time
}

// ToolGateway executes connection-backed tools with the credential and
// returns only redacted results.
type ToolGateway interface {
	Route(ctx context.Context, org, agentID, tool, action string) GatewayRoute
	Execute(ctx context.Context, c GatewayCall) GatewayOutcome
	// RegisterApproval remembers the structured context of an approval request.
	RegisterApproval(approvalID string, p GatewayPending)
	// ApprovalResolved tells the gateway how the human decided (a rejection or
	// a timeout closes the outbox item).
	ApprovalResolved(approvalID string, approved bool)
}

// PlanTaskInfo is a task of a plan under review.
type PlanTaskInfo struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	AgentID   string   `json:"agent_id"`
	DependsOn []string `json:"depends_on"`
}

// ReachableConnection is an upper bound of what an agent could touch.
type ReachableConnection struct {
	ConnectionID string   `json:"connection_id"`
	Label        string   `json:"label"`
	AgentID      string   `json:"agent_id"`
	Capabilities []string `json:"capabilities"`
	Writes       bool     `json:"writes"`
}

// PlanPreflight is the deterministic pre-run summary (never invented by the LLM).
type PlanPreflight struct {
	RequestID            string                `json:"request_id"`
	State                string                `json:"state"` // pending | approved | rejected
	Tasks                []PlanTaskInfo        `json:"tasks"`
	ReachableConnections []ReachableConnection `json:"reachable_connections"`
	TouchesWrites        bool                  `json:"touches_writes"`
	ApprovalsExpected    struct {
		Min        int      `json:"min"`
		ReasonKeys []string `json:"reason_keys"`
	} `json:"approvals_expected"`
	Note string `json:"note,omitempty"`
}

// PlanDecision is the human's answer to a plan review.
type PlanDecision struct {
	Approved          bool
	RemoveTaskIDs     []string
	NoExternalActions bool
	Note              string
}

// PlanReviewer implements the mandatory plan review (owner decision 7).
type PlanReviewer interface {
	// Begin decides whether the request needs review. When required it
	// registers the review and returns a channel that yields the decision.
	Begin(ctx context.Context, org, requestID string, tasks []PlanTaskInfo) (pf PlanPreflight, required bool, wait <-chan PlanDecision, err error)
	// Forget drops a review that nobody answered (timeout, cancellation).
	Forget(org, requestID string)
}
