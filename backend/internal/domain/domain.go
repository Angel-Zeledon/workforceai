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
	// Department is a plain string tag (the department NAME, or null): the organization model
	// (internal/orgchart) keeps it in sync with the department the agent belongs to.
	Department *string `json:"department"`

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
	// MemoryNotes (M1) are the short lessons/facts the agent wants to remember
	// for later work. Optional: old payloads and outputs without it stay valid.
	MemoryNotes []string `json:"memory_notes,omitempty"`
	// Review is the verdict of the quality review (Q1); omitted when the task
	// was not reviewed, so old payloads stay valid.
	Review *Review `json:"review,omitempty"`
	// Citations (B6, evidence chain) tie the figures of the output to where they
	// came from. The agent proposes value, label and ref; the backend checks every
	// ref against what the task really saw and fills kind, status and source
	// (docs/architecture/evidence-chain.md). Omitted when there are none.
	Citations []Citation `json:"citations,omitempty"`
}

// Citation statuses (B6). Only the backend sets them: whatever the runtime
// sends in status/source is discarded and recomputed.
const (
	CitationVerified   = "verified"
	CitationUnverified = "unverified"
)

// Citation is one figure of an output and its source.
type Citation struct {
	Value string `json:"value"`           // the figure as written ("$50,000", "24%")
	Label string `json:"label,omitempty"` // what the figure is ("real margin")
	// Ref is one of task:<id> | report:<id> | memory:<id> | file:<path or file id> |
	// cell:<artifact id>!<sheet>!<A1> | conn:<tool>:<item id>.
	Ref    string `json:"ref"`
	Kind   string `json:"kind,omitempty"`   // task|report|memory|file|cell|conn (from the ref)
	Status string `json:"status,omitempty"` // verified|unverified (set by the backend)
	// Reason says why a citation is unverified: malformed | unknown_kind |
	// not_seen | not_found | value_mismatch.
	Reason string `json:"reason,omitempty"`
	// ValueChecked: the backend found this exact figure in the source (always
	// true for a verified cell). False on a verified citation means the source
	// was really read but the figure itself could not be matched (derived figure,
	// or a source whose content the backend does not keep).
	ValueChecked bool            `json:"value_checked,omitempty"`
	Source       *CitationSource `json:"source,omitempty"` // only when verified
}

// CitationSource is what the backend resolved for a verified citation: enough
// to open it (task, artifact cell, drive file version) or to describe it
// (connector item metadata). It never carries third-party content beyond what
// the task already stored.
type CitationSource struct {
	TaskID     string     `json:"task_id,omitempty"`
	Title      string     `json:"title,omitempty"`
	AgentID    string     `json:"agent_id,omitempty"`
	ArtifactID string     `json:"artifact_id,omitempty"`
	Sheet      string     `json:"sheet,omitempty"`
	Cell       string     `json:"cell,omitempty"`
	CellValue  string     `json:"cell_value,omitempty"`
	FileID     string     `json:"file_id,omitempty"`
	Path       string     `json:"path,omitempty"`
	Version    int        `json:"version,omitempty"`
	Provider   string     `json:"provider,omitempty"`
	Tool       string     `json:"tool,omitempty"`
	ItemID     string     `json:"item_id,omitempty"`
	ReadAt     *time.Time `json:"read_at,omitempty"`
}

// Review verdicts (Q1).
const (
	VerdictPass   = "pass"
	VerdictRework = "rework"
	VerdictFail   = "fail"
)

// CriterionResult is the reviewer's judgement of one acceptance criterion.
type CriterionResult struct {
	Criterion string `json:"criterion"`
	Met       bool   `json:"met"`
	Note      string `json:"note,omitempty"`
}

// Review is the persisted outcome of the quality review of a task output.
// Reworks counts the extra runs the review caused; Rework exhausted means the
// last output still had a "rework" verdict when the bound was reached.
type Review struct {
	Verdict   string            `json:"verdict"` // pass|rework|fail
	Reasons   []string          `json:"reasons"`
	Criteria  []CriterionResult `json:"criteria,omitempty"`
	Evidence  []string          `json:"evidence,omitempty"`
	Reviewer  string            `json:"reviewer"` // agent id of the reviewer ("assistant" = neutral reviewer)
	Reworks   int               `json:"reworks"`
	Exhausted bool              `json:"rework_exhausted,omitempty"`
	At        time.Time         `json:"at"`
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
	// Files are the drive files this message refers to: what an agent produced
	// during the task, or what the user attached. References only, never content.
	Files []FileRef `json:"files,omitempty"`
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

	// Standing approvals (docs/architecture/06-permisos-autonomia.md). Tool and
	// ProjectID say what the approval is about; StandingEligible is set by the
	// server only for internal, reversible actions of a project, and is the only
	// case where the approver may choose "always allow this kind of action in
	// this project". The server re-checks it when the choice is made.
	Tool             string `json:"tool,omitempty"`
	ProjectID        string `json:"project_id,omitempty"`
	StandingEligible bool   `json:"standing_eligible,omitempty"`

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
	// Audience (additive) limits delivery to the sockets of these user ids inside the
	// organization (private chat messages); empty = the whole organization. The hub strips it.
	Audience []string `json:"audience,omitempty"`
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
	EvInitiativeRaised = "initiative.raised"
	// EvAutonomyChanged: an agent's autonomy level changed (a human accepted a promotion or a demotion, or the
	// trust ladder demoted it automatically); EvTrustProposal: a promotion or demotion was suggested or resolved
	// (docs/architecture/trust-ladder.md, additive).
	EvAutonomyChanged  = "agent.autonomy_changed"
	EvTrustProposal    = "trust.proposal"
	EvMessageSent      = "message.sent"
	EvApprovalRequest  = "approval.requested"
	EvApprovalResolved = "approval.resolved"
	// EvApprovalProgress: a double-approval request received its first approval.
	EvApprovalProgress = "approval.progress"
	// EvApprovalReminder: a project approval is still pending (it never expires).
	EvApprovalReminder = "approval.reminder"
	// EvTaskRetrying: a task failed transiently and starts another attempt, or a
	// human re-queued it (payload.manual).
	EvTaskRetrying = "task.retrying"
	// EvTaskReviewed: the quality review judged a task output (Q1).
	EvTaskReviewed = "task.reviewed"
	// EvTaskSteered: a human sent a steer note to a task; EvTaskReassigned: a
	// human moved a pending task to another agent (V2, additive).
	EvTaskSteered    = "task.steered"
	EvTaskReassigned = "task.reassigned"
	EvReportCreated  = "report.created"
	EvActivityLogged = "activity.logged"
	EvError          = "error"
	EvMetricsUpdated = "metrics.updated"
	EvHello          = "hello"
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
