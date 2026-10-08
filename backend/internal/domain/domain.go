// Package domain holds the core entities and JSON contract of the backend.
package domain

import (
	"errors"
	"time"
)

// DemoOrgID is the fixed organization used in Phase 1 (no auth).
const DemoOrgID = "00000000-0000-0000-0000-000000000001"

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid input")
	ErrConflict = errors.New("conflict")
	// ErrForbidden: the caller is authenticated but may not do this (HTTP 403).
	ErrForbidden = errors.New("forbidden")
)

type AgentState string

const (
	StateIdle             AgentState = "idle"
	StateThinking         AgentState = "thinking"
	StateWorking          AgentState = "working"
	StateWaiting          AgentState = "waiting"
	StateTalking          AgentState = "talking"
	StateReviewing        AgentState = "reviewing"
	StateBlocked          AgentState = "blocked"
	StateAwaitingApproval AgentState = "awaiting_approval"
	StateCompleted        AgentState = "completed"
	StateError            AgentState = "error"
)

type Organization struct {
	ID        string
	Name      string
	Slug      string
	BudgetUSD float64
}

type AgentMetrics struct {
	TasksCompleted int     `json:"tasks_completed"`
	TasksPending   int     `json:"tasks_pending"`
	AvgSeconds     float64 `json:"avg_seconds"`
	CostUSD        float64 `json:"cost_usd"`
}

type Agent struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Role          string       `json:"role"`
	Title         string       `json:"title"`
	Description   string       `json:"description"`
	State         AgentState   `json:"state"`
	Activity      string       `json:"activity"`
	CurrentTaskID *string      `json:"current_task_id"`
	Progress      int          `json:"progress"`
	Tools         []string     `json:"tools"`
	Permissions   []string     `json:"permissions"`
	Autonomy      string       `json:"autonomy"` // suggest|approve_each|rules|autonomous
	Metrics       AgentMetrics `json:"metrics"`

	// Internal data sent to the runtime; not part of the public API.
	Persona          string   `json:"-"`
	Responsibilities []string `json:"-"`
}

type TaskStatus string

const (
	TaskPending          TaskStatus = "pending"
	TaskRunning          TaskStatus = "running"
	TaskBlocked          TaskStatus = "blocked"
	TaskAwaitingApproval TaskStatus = "awaiting_approval"
	TaskDone             TaskStatus = "done"
	TaskFailed           TaskStatus = "failed"
)

type StructuredOutput struct {
	Summary         string         `json:"summary"`
	Findings        []string       `json:"findings"`
	Metrics         map[string]any `json:"metrics"`
	Hypotheses      []string       `json:"hypotheses"`
	Evidence        []string       `json:"evidence"`
	Recommendations []string       `json:"recommendations"`
	Confidence      float64        `json:"confidence"`
	SuggestedTasks  []string       `json:"suggested_tasks"`
}

// Normalize makes sure no collection serializes as null.
func (o *StructuredOutput) Normalize() {
	if o.Findings == nil {
		o.Findings = []string{}
	}
	if o.Metrics == nil {
		o.Metrics = map[string]any{}
	}
	if o.Hypotheses == nil {
		o.Hypotheses = []string{}
	}
	if o.Evidence == nil {
		o.Evidence = []string{}
	}
	if o.Recommendations == nil {
		o.Recommendations = []string{}
	}
	if o.SuggestedTasks == nil {
		o.SuggestedTasks = []string{}
	}
}

type Task struct {
	ID           string            `json:"id"`
	RequestID    string            `json:"request_id"`
	WorkflowID   *string           `json:"workflow_id"`
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	AgentID      string            `json:"agent_id"`
	Status       TaskStatus        `json:"status"`
	DependsOn    []string          `json:"depends_on"`
	ParentTaskID *string           `json:"parent_task_id"`
	CreatedAt    time.Time         `json:"created_at"`
	StartedAt    *time.Time        `json:"started_at"`
	FinishedAt   *time.Time        `json:"finished_at"`
	Output       *StructuredOutput `json:"output"`
	// AssignedReason says, in plain language, why this agent got the task
	// (optional: tasks created before the chat layer have none).
	AssignedReason string `json:"assigned_reason,omitempty"`

	Depth   int     `json:"-"` // delegation depth, 1-based
	CostUSD float64 `json:"-"`
}

type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	From           string    `json:"from"`
	To             string    `json:"to"`
	Kind           string    `json:"kind"` // chat|delegation|consult|answer
	Text           string    `json:"text"`
	TaskID         *string   `json:"task_id"`
	TS             time.Time `json:"ts"`

	// Chat layer (docs/architecture/chat-routing.md); all optional and omitted
	// for the messages of a request conversation. TurnID groups the replies to
	// one user message, ReplyTo is the user message they answer and RequestID is
	// the request a task turn created.
	TurnID    string  `json:"turn_id,omitempty"`
	ReplyTo   *string `json:"reply_to,omitempty"`
	RequestID *string `json:"request_id,omitempty"`
}

type Conversation struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Participants  []string  `json:"participants"`
	RequestID     *string   `json:"request_id"`
	LastMessageAt time.Time `json:"last_message_at"`
}

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
)

type Approval struct {
	ID         string         `json:"id"`
	TaskID     string         `json:"task_id"`
	AgentID    string         `json:"agent_id"`
	Action     string         `json:"action"`
	Title      string         `json:"title"`
	Details    string         `json:"details"`
	Risk       string         `json:"risk"`
	Status     ApprovalStatus `json:"status"`
	Note       string         `json:"note,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	ResolvedAt *time.Time     `json:"resolved_at"`

	// Governance (policy engine). RequiredApprovals is 1 unless the
	// organization configured double approval for this kind of action (then 2:
	// two distinct humans). RequiredRole is the minimum role of every approver
	// ("" = anyone allowed to decide). RequestedBy is the human on whose
	// behalf the agent acts; with double approval (or NoSelfApproval) that
	// person can never approve. Decisions lists the approvals received so far.
	RequiredApprovals int                `json:"required_approvals"`
	RequiredRole      string             `json:"required_role,omitempty"`
	RequestedBy       string             `json:"requested_by,omitempty"`
	NoSelfApproval    bool               `json:"no_self_approval,omitempty"`
	PolicyRule        string             `json:"policy_rule,omitempty"`
	Decisions         []ApprovalDecision `json:"decisions"`

	// Context is optional structured information for the approval card of a
	// connection-backed action (account, recipients, reversibility, hold
	// window, taint). It carries no secrets or message content and is not
	// persisted: it is set when the approval is requested and re-attached by
	// GET /approvals while the process that created it is alive.
	Context map[string]any `json:"context,omitempty"`
}

// ApprovalDecision is one human approval counted towards the quorum.
type ApprovalDecision struct {
	By   string    `json:"by"`
	Role string    `json:"role,omitempty"`
	Note string    `json:"note,omitempty"`
	TS   time.Time `json:"ts"`
}

type Section struct {
	Heading string `json:"heading"`
	Body    string `json:"body"`
}

type Report struct {
	ID           string    `json:"id"`
	RequestID    string    `json:"request_id"`
	Title        string    `json:"title"`
	Summary      string    `json:"summary"`
	Sections     []Section `json:"sections"`
	Contributors []string  `json:"contributors"`
	CostUSD      float64   `json:"cost_usd"`
	CreatedAt    time.Time `json:"created_at"`
}

type ActivityItem struct {
	ID      string    `json:"id"`
	TS      time.Time `json:"ts"`
	AgentID *string   `json:"agent_id"`
	Kind    string    `json:"kind"`
	Text    string    `json:"text"`
}

type RequestStatus string

const (
	RequestPlanning         RequestStatus = "planning"
	RequestRunning          RequestStatus = "running"
	RequestAwaitingApproval RequestStatus = "awaiting_approval"
	RequestDone             RequestStatus = "done"
	RequestFailed           RequestStatus = "failed"
)

// Plan is what the runtime planner returned for a request.
type Plan struct {
	Objectives          []string `json:"objectives"`
	ClarifyingQuestions []string `json:"clarifying_questions"`
}

type Request struct {
	ID        string        `json:"id"`
	Text      string        `json:"text"`
	Status    RequestStatus `json:"status"`
	CreatedAt time.Time     `json:"created_at"`
	ReportID  *string       `json:"report_id"`
	CostUSD   float64       `json:"cost_usd"`

	Plan Plan `json:"-"`
}

type Memory struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Scope string `json:"scope"`
}

type Metrics struct {
	TasksActive      int     `json:"tasks_active"`
	TasksDone        int     `json:"tasks_done"`
	ApprovalsPending int     `json:"approvals_pending"`
	CostUSD          float64 `json:"cost_usd"`
	BudgetUSD        float64 `json:"budget_usd"`
	Errors           int     `json:"errors"`
	RequestsTotal    int     `json:"requests_total"`
}

// Event is the WebSocket frame.
type Event struct {
	ID      string    `json:"id"`
	Type    string    `json:"type"`
	TS      time.Time `json:"ts"`
	OrgID   string    `json:"org_id"`
	AgentID string    `json:"agent_id,omitempty"`
	Payload any       `json:"payload"`
}

// Event type names (WS contract).
const (
	EvRequestReceived  = "request.received"
	EvAgentCreated     = "agent.created"
	EvRequestCompleted = "request.completed"
	// EvRequestResumed: a request interrupted by a restart continues (additive, A1).
	EvRequestResumed = "request.resumed"
	// EvRequestQueued / EvRequestDequeued: a runtime call of the request waits
	// for a free slot of its organization (MAX_PARALLEL_PER_ORG) and gets it
	// (additive, A1 step 6).
	EvRequestQueued    = "request.queued"
	EvRequestDequeued  = "request.dequeued"
	EvPlanCreated      = "plan.created"
	EvAgentState       = "agent.state_changed"
	EvTaskCreated      = "task.created"
	EvTaskStarted      = "task.started"
	EvTaskCompleted    = "task.completed"
	EvTaskFailed       = "task.failed"
	EvTaskBlocked      = "task.blocked"
	EvMessageSent      = "message.sent"
	EvApprovalRequest  = "approval.requested"
	EvApprovalResolved = "approval.resolved"
	// EvApprovalProgress: a double-approval request received its first approval.
	EvApprovalProgress = "approval.progress"
	EvReportCreated    = "report.created"
	EvActivityLogged   = "activity.logged"
	EvError            = "error"
	EvMetricsUpdated   = "metrics.updated"
	EvHello            = "hello"
)

// AuditLog is one entry of the append-only audit trail. Details carries
// metadata only (never secrets or content). Seq, PrevHash and Hash are set by
// the store when the entry is appended: every entry commits to the previous
// one of the same organization (see package audit).
type AuditLog struct {
	ID        string
	TS        time.Time
	Actor     string
	Action    string
	Entity    string
	EntityID  string
	Details   any
	OrgID     string
	RequestID string
	Seq       int64
	PrevHash  string
	Hash      string
}
