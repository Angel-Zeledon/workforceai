// Package application holds the use cases: orchestrator, scheduler and approvals.
package application

import (
	"context"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Store is the persistence port. Every method is scoped by org id.
type Store interface {
	// Seed upserts the organization and inserts missing agents (idempotent).
	Seed(ctx context.Context, org domain.Organization, agents []domain.Agent) error
	// Reset removes execution data and sets agents back to idle.
	Reset(ctx context.Context, orgID string) error

	ListAgents(ctx context.Context, orgID string) ([]domain.Agent, error)
	GetAgent(ctx context.Context, orgID, id string) (domain.Agent, error)
	UpdateAgentState(ctx context.Context, orgID, id string, state domain.AgentState, activity string, taskID *string, progress int) error

	CreateTask(ctx context.Context, orgID string, t domain.Task) error
	GetTask(ctx context.Context, orgID, id string) (domain.Task, error)
	UpdateTask(ctx context.Context, orgID string, t domain.Task) error
	ListTasks(ctx context.Context, orgID, agentID, status string) ([]domain.Task, error)
	ListTasksByRequest(ctx context.Context, orgID, requestID string) ([]domain.Task, error)

	CreateRequest(ctx context.Context, orgID string, r domain.Request) error
	GetRequest(ctx context.Context, orgID, id string) (domain.Request, error)
	UpdateRequest(ctx context.Context, orgID string, r domain.Request) error
	ListRequests(ctx context.Context, orgID string) ([]domain.Request, error)
	// AddCost atomically adds usage cost to a request and (optionally) a task.
	AddCost(ctx context.Context, orgID, requestID, taskID string, usd float64) error
	OrgCost(ctx context.Context, orgID string) (float64, error)

	// Cost ledger and hard caps (cost control).
	AddUsage(ctx context.Context, orgID string, u domain.UsageEntry) error
	ListUsage(ctx context.Context, orgID string) ([]domain.UsageEntry, error)
	// SetBudgetCap upserts a cap for scope "agent" or "request"; capUSD <= 0 removes it.
	SetBudgetCap(ctx context.Context, orgID string, scope domain.BudgetScope, scopeID string, capUSD float64) error
	ListBudgetCaps(ctx context.Context, orgID string) ([]domain.BudgetCap, error)

	CreateConversation(ctx context.Context, orgID string, c domain.Conversation) error
	GetConversation(ctx context.Context, orgID, id string) (domain.Conversation, error)
	ListConversations(ctx context.Context, orgID string) ([]domain.Conversation, error)
	AddMessage(ctx context.Context, orgID string, m domain.Message) error
	ListMessages(ctx context.Context, orgID, conversationID string) ([]domain.Message, error)

	CreateApproval(ctx context.Context, orgID string, a domain.Approval) error
	GetApproval(ctx context.Context, orgID, id string) (domain.Approval, error)
	UpdateApproval(ctx context.Context, orgID string, a domain.Approval) error
	ListApprovals(ctx context.Context, orgID, status string) ([]domain.Approval, error)

	CreateReport(ctx context.Context, orgID string, r domain.Report) error
	GetReport(ctx context.Context, orgID, id string) (domain.Report, error)
	ListReports(ctx context.Context, orgID string) ([]domain.Report, error)

	SetMemory(ctx context.Context, orgID, agentID string, m domain.Memory) error
	ListMemory(ctx context.Context, orgID, agentID string) ([]domain.Memory, error)

	SaveEvent(ctx context.Context, orgID string, e domain.Event) error
	AddActivity(ctx context.Context, orgID string, a domain.ActivityItem) error
	// ListActivity returns newest first; agentID "" means all agents.
	ListActivity(ctx context.Context, orgID, agentID string, limit int) ([]domain.ActivityItem, error)
	AddAudit(ctx context.Context, orgID string, a domain.AuditLog) error
}

// AgentWriter adds agents to an organization (hiring from a role template).
// Both the memory and the Postgres stores implement it.
type AgentWriter interface {
	CreateAgent(ctx context.Context, orgID string, a domain.Agent) error
}

// AuditStore is the read side of the tamper-evident audit trail (the write
// side is Store.AddAudit, which chains each entry to the previous one of the
// organization). Both the memory and the Postgres stores implement it.
type AuditStore interface {
	QueryAudit(ctx context.Context, orgID string, q domain.AuditQuery) (domain.AuditPage, error)
	// AuditChain returns chained entries with seq > after, ascending.
	AuditChain(ctx context.Context, orgID string, after int64, limit int) ([]domain.AuditLog, error)
	AuditHead(ctx context.Context, orgID string) (domain.AuditHead, bool, error)
}

// Publisher fans events out (Redis pub/sub -> WS hub).
type Publisher interface {
	Publish(ctx context.Context, e domain.Event) error
}

// Locker provides a simple distributed lock.
type Locker interface {
	// Lock returns ok=false when the key is already held.
	Lock(ctx context.Context, key string, ttl time.Duration) (unlock func(), ok bool, err error)
}

// Runtime is the port to the agent-runtime (Python / CrewAI).
type Runtime interface {
	Plan(ctx context.Context, in PlanRequest) (PlanResponse, error)
	RunTask(ctx context.Context, in RunTaskRequest) (RunTaskResponse, error)
	Consult(ctx context.Context, in ConsultRequest) (ConsultResponse, error)
	Synthesize(ctx context.Context, in SynthesizeRequest) (SynthesizeResponse, error)
	Health(ctx context.Context) (mode string, err error)
}

// Estimator is the optional runtime capability behind POST /v1/estimate. The
// orchestrator type-asserts it, so runtimes without it keep working (the
// backend then falls back to coarse constants, basis "fallback").
type Estimator interface {
	Estimate(ctx context.Context, in EstimateRequest) (EstimateResponse, error)
}

// ---- Runtime DTOs (contract Go -> agent-runtime) ----

type EstimateTaskIn struct {
	ID          string   `json:"id"`
	Key         string   `json:"key,omitempty"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	AgentID     string   `json:"agent_id"`
	DependsOn   []string `json:"depends_on"`
}

type EstimateRequest struct {
	RequestText string           `json:"request_text"`
	Tasks       []EstimateTaskIn `json:"tasks"`
}

type EstimateTaskOut struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	AgentID string  `json:"agent_id"`
	MinUSD  float64 `json:"min_usd"`
	MaxUSD  float64 `json:"max_usd"`
}

type EstimateResponse struct {
	Mode      string            `json:"mode"`
	Model     string            `json:"model"`
	Basis     string            `json:"basis"`
	Currency  string            `json:"currency"`
	Tasks     []EstimateTaskOut `json:"tasks"`
	Synthesis domain.CostRange  `json:"synthesis"`
	Total     domain.CostRange  `json:"total"`
}

type PlanAgent struct {
	ID               string   `json:"id"`
	Role             string   `json:"role"`
	Title            string   `json:"title"`
	Responsibilities []string `json:"responsibilities"`
	// Profile of the role template: lets the planner give a task to the owner of its area.
	Topic    string   `json:"topic,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
	Area     string   `json:"area,omitempty"`
}

type PlanRequest struct {
	RequestText string      `json:"request_text"`
	Agents      []PlanAgent `json:"agents"`
	BudgetUSD   float64     `json:"budget_usd"`
	// Locale ("es"|"en") and Tone (regional style) are optional and omitted
	// when the organization has not configured them (runtime defaults apply).
	Locale string `json:"locale,omitempty"`
	Tone   string `json:"tone,omitempty"`
}

type PlannedTask struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	AgentID     string   `json:"agent_id"`
	DependsOn   []string `json:"depends_on"`
	// Reason is the optional human explanation of why this agent got the task.
	Reason string `json:"reason,omitempty"`
}

type PlanResponse struct {
	Objectives          []string      `json:"objectives"`
	Tasks               []PlannedTask `json:"tasks"`
	ClarifyingQuestions []string      `json:"clarifying_questions"`
	// MaxDepth, when above Config.MaxDepth, raises the dependency-chain limit
	// for this plan only (long project workflows). Never sent by the runtime.
	MaxDepth int `json:"-"`
	// Usage is the optional cost of the planning call (additive; old runtimes omit it).
	Usage *Usage `json:"usage,omitempty"`
}

// ---- hierarchical planning of large projects (W4) ----

// PlanPhase is one phase of the roadmap: a rough size and the phases it waits for.
type PlanPhase struct {
	Key       string   `json:"key"`
	Title     string   `json:"title"`
	Goal      string   `json:"goal,omitempty"`
	Size      string   `json:"size"` // S|M|L|XL
	DependsOn []string `json:"depends_on"`
}

type PlanPhasesRequest struct {
	RequestText string      `json:"request_text"`
	Agents      []PlanAgent `json:"agents"`
	BudgetUSD   float64     `json:"budget_usd"`
	MaxPhases   int         `json:"max_phases,omitempty"`
	Locale      string      `json:"locale,omitempty"`
	Tone        string      `json:"tone,omitempty"`
}

type PlanPhasesResponse struct {
	Objectives          []string    `json:"objectives"`
	Phases              []PlanPhase `json:"phases"`
	ClarifyingQuestions []string    `json:"clarifying_questions"`
	Usage               *Usage      `json:"usage,omitempty"`
}

type PhaseBrief struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

type PlanPhaseRequest struct {
	RequestText string       `json:"request_text"`
	Phase       PlanPhase    `json:"phase"`
	OtherPhases []PhaseBrief `json:"other_phases,omitempty"`
	Agents      []PlanAgent  `json:"agents"`
	TargetTasks int          `json:"target_tasks"`
	MaxTasks    int          `json:"max_tasks"`
	BudgetUSD   float64      `json:"budget_usd"`
	Locale      string       `json:"locale,omitempty"`
	Tone        string       `json:"tone,omitempty"`
}

// PhaseTask is a task of ONE phase: keys are local to the phase.
type PhaseTask struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	AgentID     string   `json:"agent_id"`
	DependsOn   []string `json:"depends_on"`
	Complexity  string   `json:"complexity"` // S|M|L|XL
	Reason      string   `json:"reason,omitempty"`
}

type PlanPhaseResponse struct {
	Tasks []PhaseTask `json:"tasks"`
	Usage *Usage      `json:"usage,omitempty"`
}

// HierarchicalPlanner is the optional runtime capability behind big-project
// planning (POST /v1/plan-phases, /v1/plan-phase). A Runtime that does not
// implement it makes the projects service use the flat planner.
type HierarchicalPlanner interface {
	PlanPhases(ctx context.Context, in PlanPhasesRequest) (PlanPhasesResponse, error)
	PlanPhase(ctx context.Context, in PlanPhaseRequest) (PlanPhaseResponse, error)
}

type RunTaskInfo struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	AgentID     string `json:"agent_id"`
}

type RunAgentInfo struct {
	ID               string   `json:"id"`
	Role             string   `json:"role"`
	Title            string   `json:"title"`
	Persona          string   `json:"persona"`
	Responsibilities []string `json:"responsibilities"`
	Tools            []string `json:"tools"`
	Area             string   `json:"area,omitempty"`
}

type DependencyOutput struct {
	TaskID  string                  `json:"task_id"`
	AgentID string                  `json:"agent_id"`
	Output  domain.StructuredOutput `json:"output"`
}

type MemoryEntry struct {
	Scope string `json:"scope"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

type RunContext struct {
	RequestText       string             `json:"request_text"`
	DependencyOutputs []DependencyOutput `json:"dependency_outputs"`
	Memory            []MemoryEntry      `json:"memory"`
}

type RunTaskRequest struct {
	Task            RunTaskInfo  `json:"task"`
	Agent           RunAgentInfo `json:"agent"`
	Context         RunContext   `json:"context"`
	ExternalContent []string     `json:"external_content,omitempty"`
	Locale          string       `json:"locale,omitempty"`
	Tone            string       `json:"tone,omitempty"`
}

type ConsultRequestItem struct {
	ToAgentID string `json:"to_agent_id"`
	Question  string `json:"question"`
}

type ToolRequest struct {
	Tool   string         `json:"tool"`
	Action string         `json:"action"`
	Args   map[string]any `json:"args"`
	Risk   string         `json:"risk"`
}

type Usage struct {
	Model        string  `json:"model"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	DurationMS   int     `json:"duration_ms"`
}

type RunTaskResponse struct {
	Output       domain.StructuredOutput `json:"output"`
	Consults     []ConsultRequestItem    `json:"consults"`
	ToolRequests []ToolRequest           `json:"tool_requests"`
	Usage        Usage                   `json:"usage"`
}

type ConsultRequest struct {
	FromAgentID string `json:"from_agent_id"`
	ToAgentID   string `json:"to_agent_id"`
	Question    string `json:"question"`
	Context     string `json:"context"`
	Locale      string `json:"locale,omitempty"`
	Tone        string `json:"tone,omitempty"`
}

type ConsultResponse struct {
	Answer string `json:"answer"`
	Usage  Usage  `json:"usage"`
}

type SynthOutput struct {
	TaskID  string                  `json:"task_id"`
	AgentID string                  `json:"agent_id"`
	Title   string                  `json:"title"`
	Output  domain.StructuredOutput `json:"output"`
}

type SynthesizeRequest struct {
	RequestText string        `json:"request_text"`
	Outputs     []SynthOutput `json:"outputs"`
	Locale      string        `json:"locale,omitempty"`
	Tone        string        `json:"tone,omitempty"`
}

type SynthesizeResponse struct {
	Title    string           `json:"title"`
	Summary  string           `json:"summary"`
	Sections []domain.Section `json:"sections"`
	Usage    Usage            `json:"usage"`
}

// RequestApprovalLister is an optional Store capability: the approvals of one
// request (plus extra task ids) without loading every approval of the org.
// Callers fall back to ListApprovals when the store does not implement it.
type RequestApprovalLister interface {
	ListApprovalsByRequest(ctx context.Context, orgID, requestID string, extraTaskIDs []string) ([]domain.Approval, error)
}
