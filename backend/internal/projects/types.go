// Package projects implements "projects": whole workflows with parallel work
// (docs/architecture/workflow-visualization.md). A project is a plan of
// objectives, workflow groups and nodes. Launching it submits the leaf nodes as
// ONE orchestrator request (real tasks with dependencies and parallelism), so
// budget caps, approvals, the kill switch / read-only mode / agent pauses, the
// policy engine and the audit trail are the ones the rest of the product has.
// The project layer adds what the orchestrator does not know: the plan itself
// (drafts, templates, estimates), pause/resume/cancel of the whole project,
// human gates and the aggregated views the frontend consumes.
package projects

import (
	"time"

	"aiworkforce/backend/internal/domain"
)

// Node kinds, states and statuses: the exact vocabulary of frontend/src/lib/projects/types.ts.
const (
	KindTask      = "task"
	KindSubtask   = "subtask"
	KindGroup     = "group"
	KindMilestone = "milestone"
	KindGate      = "gate"
	KindWait      = "wait"

	StateDraft            = "draft"
	StatePending          = "pending"
	StateReady            = "ready"
	StateRunning          = "running"
	StateAwaitingApproval = "awaiting_approval"
	StatePaused           = "paused"
	StateBlocked          = "blocked"
	StateDone             = "done"
	StateFailed           = "failed"
	StateCancelled        = "cancelled"

	StatusDraft        = "draft"
	StatusRunning      = "running"
	StatusPaused       = "paused"
	StatusWaitingHuman = "waiting_human"
	StatusDone         = "done"
	StatusFailed       = "failed"
	StatusCancelled    = "cancelled"

	ControlActive    = "active"
	ControlPaused    = "paused"
	ControlCancelled = "cancelled"

	// ActionExtendBudget is the approval raised when the project hits its cap.
	ActionExtendBudget = "extend_budget"
)

// ---- JSON contract (what the frontend reads) ----

type Objective struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	TitleKey    string            `json:"title_key,omitempty"`
	TitleParams map[string]string `json:"title_params,omitempty"`
	Position    int               `json:"position"`
}

// Node is the API view of one node: static definition + live state.
type Node struct {
	ID              string            `json:"id"`
	ProjectID       string            `json:"project_id"`
	ObjectiveID     string            `json:"objective_id"`
	ParentID        *string           `json:"parent_id"`
	Kind            string            `json:"kind"`
	Title           string            `json:"title"`
	TitleKey        string            `json:"title_key,omitempty"`
	TitleParams     map[string]string `json:"title_params,omitempty"`
	AgentID         *string           `json:"agent_id"`
	State           string            `json:"state"`
	Attempt         int               `json:"attempt"`
	MaxAttempts     int               `json:"max_attempts"`
	Progress        int               `json:"progress"`
	DependsOn       []string          `json:"depends_on"`
	DelegationDepth int               `json:"delegation_depth"`
	DelegationChain []string          `json:"delegation_chain"`
	DagLevel        int               `json:"dag_level"`
	WBSPath         string            `json:"wbs_path"`
	Complexity      string            `json:"complexity"`
	EstCostUSD      float64           `json:"est_cost_usd"`
	EstSeconds      float64           `json:"est_seconds"`
	CostUSD         float64           `json:"cost_usd"`
	PlanStartMS     *int64            `json:"plan_start_ms"`
	PlanEndMS       *int64            `json:"plan_end_ms"`
	StartedAt       *time.Time        `json:"started_at"`
	FinishedAt      *time.Time        `json:"finished_at"`
	Rev             int               `json:"rev"`
	ApprovalAction  string            `json:"approval_action,omitempty"`
	Error           *string           `json:"error,omitempty"`
	// TaskID is the orchestrator task behind a launched leaf node (additive).
	TaskID string `json:"task_id,omitempty"`
	// Skipped: a human skipped this failed node (W2); SkipReason is theirs.
	Skipped    bool   `json:"skipped,omitempty"`
	SkipReason string `json:"skip_reason,omitempty"`
	// Acceptance are the checkable criteria of the node and Review the verdict
	// of its quality review (Q1, additive).
	Acceptance []string       `json:"acceptance,omitempty"`
	Review     *domain.Review `json:"review,omitempty"`
}

type Approval struct {
	ID         string     `json:"id"`
	ProjectID  string     `json:"project_id"`
	NodeID     string     `json:"node_id"`
	AgentID    *string    `json:"agent_id"`
	Action     string     `json:"action"`
	Title      string     `json:"title"`
	Details    string     `json:"details"`
	Risk       string     `json:"risk"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
}

type BudgetPolicy struct {
	Mode       string    `json:"mode"`
	WarnAt     []float64 `json:"warn_at"`
	OnHard     string    `json:"on_hard"`
	ReservePct int       `json:"reserve_pct"`
}

func defaultBudgetPolicy() BudgetPolicy {
	return BudgetPolicy{Mode: "hard", WarnAt: []float64{0.5, 0.8, 0.95}, OnHard: "pause_and_ask", ReservePct: 10}
}

type Estimate struct {
	Basis      string `json:"basis"`
	Model      string `json:"model"`
	Confidence string `json:"confidence"`
	Total      struct {
		P50USD float64 `json:"p50_usd"`
		P90USD float64 `json:"p90_usd"`
		Calls  int     `json:"calls"`
	} `json:"total"`
	Duration struct {
		P50Seconds       float64 `json:"p50_seconds"`
		P90Seconds       float64 `json:"p90_seconds"`
		HumanWaitSeconds float64 `json:"human_wait_seconds"`
	} `json:"duration"`
	ByObjective []EstObjective `json:"by_objective"`
	ByAgent     []EstAgent     `json:"by_agent"`
	Warnings    []EstWarning   `json:"warnings"`
}

type EstObjective struct {
	ID     string  `json:"id"`
	P50USD float64 `json:"p50_usd"`
	P90USD float64 `json:"p90_usd"`
	Nodes  int     `json:"nodes"`
}
type EstAgent struct {
	AgentID string  `json:"agent_id"`
	P50USD  float64 `json:"p50_usd"`
	Calls   int     `json:"calls"`
}
type EstWarning struct {
	Key    string         `json:"key"`
	Params map[string]any `json:"params,omitempty"`
}

type Summary struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	NameKey       string     `json:"name_key,omitempty"`
	Goal          string     `json:"goal"`
	Status        string     `json:"status"`
	Control       string     `json:"control"`
	TemplateID    *string    `json:"template_id"`
	BudgetUSD     float64    `json:"budget_usd"`
	SpentUSD      float64    `json:"spent_usd"`
	TasksDone     int        `json:"tasks_done"`
	TasksTotal    int        `json:"tasks_total"`
	Running       int        `json:"running"`
	Awaiting      int        `json:"awaiting"`
	Failed        int        `json:"failed"`
	Light         string     `json:"light"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	DeadlineAt    *time.Time `json:"deadline_at"`
	ObjectivesCnt int        `json:"objectives_count"`
	// RequestID is the orchestrator request behind a launched project (additive).
	RequestID string `json:"request_id,omitempty"`
	// BudgetWarnPct is the highest budget early-warning threshold already crossed
	// (percent of the approved budget, e.g. 80); 0 when none (additive, W5).
	BudgetWarnPct float64 `json:"budget_warn_pct,omitempty"`
}

type Detail struct {
	Project          Summary           `json:"project"`
	Objectives       []Objective       `json:"objectives"`
	Nodes            []Node            `json:"nodes"`
	Approvals        []Approval        `json:"approvals"`
	Budget           BudgetPolicy      `json:"budget"`
	Estimate         *Estimate         `json:"estimate"`
	StructureVersion int               `json:"structure_version"`
	MaxParallel      int               `json:"max_parallel"`
	Planning         *PlanningProgress `json:"planning"`
	// Planner says how the draft plan was produced and whether the planner
	// failed or degraded (additive; absent for template projects).
	Planner *PlannerInfo `json:"planner,omitempty"`
	// Quality is the quality-review setting of the project (additive, Q1).
	Quality *Quality `json:"quality,omitempty"`
}

// PlanningProgress is the progress of a draft whose planner is still running.
type PlanningProgress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// Planner modes and statuses (PlannerInfo).
const (
	PlannerHierarchical = "hierarchical" // phases, then tasks per phase
	PlannerFlat         = "flat"         // one planner call (single workflow)
	PlannerGeneric      = "generic"      // the generic template: the planner did not produce a plan

	PlannerOK       = "ok"
	PlannerRunning  = "running"  // the draft is still being planned
	PlannerDegraded = "degraded" // a plan exists but not the one asked for (fallback or failed phases)
	PlannerFailed   = "failed"   // no plan: the generic template was used
)

// PlannerInfo is the visible outcome of the planner of a project created from a goal.
type PlannerInfo struct {
	Mode   string `json:"mode"`
	Status string `json:"status"`
	// Code is a stable reason of a failure or degradation: planner_unavailable,
	// planner_timeout, planner_invalid, budget_exceeded, no_runtime, no_agents,
	// planner_interrupted, phases_failed.
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	// FellBackFrom is the mode that failed before this one produced the plan.
	FellBackFrom string   `json:"fell_back_from,omitempty"`
	Phases       int      `json:"phases,omitempty"`
	Tasks        int      `json:"tasks,omitempty"`
	FailedPhases []string `json:"failed_phases,omitempty"`
	Calls        int      `json:"calls,omitempty"`
	CostUSD      float64  `json:"cost_usd,omitempty"`
}

// Health is GET /projects/{id}/health.
type Health struct {
	ProjectID  string    `json:"project_id"`
	ComputedAt time.Time `json:"computed_at"`
	Light      string    `json:"light"`
	Progress   struct {
		TasksDone   int     `json:"tasks_done"`
		TasksTotal  int     `json:"tasks_total"`
		WeightedPct float64 `json:"weighted_pct"`
	} `json:"progress"`
	Schedule struct {
		DeadlineAt     *time.Time `json:"deadline_at"`
		EtaP50         *time.Time `json:"eta_p50"`
		EtaP90         *time.Time `json:"eta_p90"`
		SlipSecondsP50 *int64     `json:"slip_seconds_p50"`
		// CalibrationFactor is observed/estimated duration of the finished agent
		// nodes (median); the remaining estimate is multiplied by it. Omitted
		// until CalibrationSamples >= minCalibrationSamples (additive, W5).
		CalibrationFactor  float64 `json:"calibration_factor,omitempty"`
		CalibrationSamples int     `json:"calibration_samples,omitempty"`
	} `json:"schedule"`
	CriticalPath struct {
		LengthSeconds  float64  `json:"length_seconds"`
		NodeIDs        []string `json:"node_ids"`
		BlockedOnHuman int      `json:"blocked_on_human"`
	} `json:"critical_path"`
	Budget struct {
		LimitUSD                float64 `json:"limit_usd"`
		SpentUSD                float64 `json:"spent_usd"`
		BurnUSDPerH             float64 `json:"burn_usd_per_h"`
		ForecastAtCompletionUSD float64 `json:"forecast_at_completion_usd"`
		ForecastP90USD          float64 `json:"forecast_p90_usd"`
		Warning                 string  `json:"warning"`
	} `json:"budget"`
	WaitingHuman struct {
		Count            int     `json:"count"`
		OldestAgeSeconds float64 `json:"oldest_age_seconds"`
		BlockingCritical int     `json:"blocking_critical"`
	} `json:"waiting_human"`
	Risks       []Risk        `json:"risks"`
	ByObjective []HealthByObj `json:"by_objective"`
}

type Risk struct {
	Kind    string   `json:"kind"`
	NodeIDs []string `json:"node_ids"`
	AgentID string   `json:"agent_id,omitempty"`
	Count   int      `json:"count"`
}
type HealthByObj struct {
	ID       string  `json:"id"`
	Pct      float64 `json:"pct"`
	State    string  `json:"state"`
	SpentUSD float64 `json:"spent_usd"`
}

// Issue is one finding of the validation of a draft.
type Issue struct {
	Severity string `json:"severity"` // error|warn
	NodeID   string `json:"node_id,omitempty"`
	Code     string `json:"code"` // cycle|no_agent|unknown_agent|empty_title|no_nodes|dangling_dependency
}

// ---- templates ----

type TemplateParam struct {
	Key      string `json:"key"`
	LabelKey string `json:"label_key"`
	Label    string `json:"label,omitempty"`
	Default  string `json:"default"`
}
type TemplateApproval struct {
	Action string `json:"action"`
	Risk   string `json:"risk"`
}
type TemplateDelegate struct {
	Agent      string `json:"agent"`
	TitleKey   string `json:"title_key"`
	Title      string `json:"title,omitempty"`
	Complexity string `json:"complexity,omitempty"`
}
type TemplateNode struct {
	Key         string            `json:"key"`
	Kind        string            `json:"kind,omitempty"`
	TitleKey    string            `json:"title_key"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Agent       string            `json:"agent,omitempty"`
	Complexity  string            `json:"complexity,omitempty"`
	Acceptance  []string          `json:"acceptance,omitempty"`
	Deps        []string          `json:"deps,omitempty"`
	Secs        float64           `json:"secs,omitempty"`
	Approval    *TemplateApproval `json:"approval,omitempty"`
	Delegate    *TemplateDelegate `json:"delegate,omitempty"`
}
type TemplateWorkflow struct {
	Key      string         `json:"key"`
	TitleKey string         `json:"title_key"`
	Title    string         `json:"title,omitempty"`
	Nodes    []TemplateNode `json:"nodes"`
}
type TemplateObjective struct {
	Key       string             `json:"key"`
	TitleKey  string             `json:"title_key"`
	Title     string             `json:"title,omitempty"`
	Workflows []TemplateWorkflow `json:"workflows"`
}
type Template struct {
	ID             string              `json:"id"`
	Key            string              `json:"key"`
	Version        int                 `json:"version"`
	NameKey        string              `json:"name_key,omitempty"`
	Name           string              `json:"name"`
	DescriptionKey string              `json:"description_key,omitempty"`
	Description    string              `json:"description"`
	Params         []TemplateParam     `json:"params"`
	Objectives     []TemplateObjective `json:"objectives"`
	Builtin        bool                `json:"builtin"`
	BudgetHint     *BudgetHint         `json:"budget_hint,omitempty"`
}
type BudgetHint struct {
	P50USD float64 `json:"p50_usd"`
	P90USD float64 `json:"p90_usd"`
	Basis  string  `json:"basis"`
}

// ---- requests ----

type NewProject struct {
	Goal       string            `json:"goal"`
	TemplateID string            `json:"template_id,omitempty"`
	Params     map[string]string `json:"params,omitempty"`
	BudgetUSD  float64           `json:"budget_usd,omitempty"`
	Locale     string            `json:"locale,omitempty"`
}

type LaunchBody struct {
	ApprovedBudgetUSD      float64 `json:"approved_budget_usd"`
	AcknowledgeUnderbudget bool    `json:"acknowledge_underbudget,omitempty"`
	MaxParallel            int     `json:"max_parallel,omitempty"`
}

type PlanOp struct {
	// Op is "update" (a node; ID required) or "set_quality" (the project; Fields.Review).
	Op     string `json:"op"`
	ID     string `json:"id"`
	Fields struct {
		Title   *string `json:"title,omitempty"`
		AgentID *string `json:"agent_id,omitempty"`
		// Acceptance replaces the acceptance criteria of a node (Q1); [] clears them.
		Acceptance *[]string `json:"acceptance,omitempty"`
		// Review sets quality.review: off|low_confidence|always (op "set_quality").
		Review *string `json:"review,omitempty"`
	} `json:"fields"`
}

// BatchDecision is POST /approvals/batch.
type BatchDecision struct {
	Filter struct {
		ProjectID string `json:"project_id"`
		Action    string `json:"action"`
	} `json:"filter"`
	Decision      string `json:"decision"`
	ExpectedCount int    `json:"expected_count"`
	IncludeHigh   bool   `json:"include_high"`
}

// ---- persisted record ----

// NodeDef is the static definition of a node (the live state is derived from
// the orchestrator tasks at read time).
type NodeDef struct {
	ID              string            `json:"id"`
	Key             string            `json:"key"`
	ObjectiveID     string            `json:"objective_id"`
	ParentID        *string           `json:"parent_id"`
	Kind            string            `json:"kind"`
	Title           string            `json:"title"`
	TitleKey        string            `json:"title_key,omitempty"`
	TitleParams     map[string]string `json:"title_params,omitempty"`
	Description     string            `json:"description,omitempty"`
	AgentID         *string           `json:"agent_id"`
	DependsOn       []string          `json:"depends_on"`
	DelegationDepth int               `json:"delegation_depth"`
	DelegationChain []string          `json:"delegation_chain"`
	DagLevel        int               `json:"dag_level"`
	WBSPath         string            `json:"wbs_path"`
	Complexity      string            `json:"complexity"`
	EstCostUSD      float64           `json:"est_cost_usd"`
	EstSeconds      float64           `json:"est_seconds"`
	PlanStartMS     *int64            `json:"plan_start_ms"`
	PlanEndMS       *int64            `json:"plan_end_ms"`
	ApprovalAction  string            `json:"approval_action,omitempty"`
	ApprovalRisk    string            `json:"approval_risk,omitempty"`
	TaskID          string            `json:"task_id,omitempty"`
	// Acceptance: checkable criteria the output is reviewed against (Q1).
	Acceptance []string `json:"acceptance,omitempty"`
}

func (n NodeDef) isGroup() bool { return n.Kind == KindGroup }

// human reports the nodes a person (not an agent) resolves.
func (n NodeDef) human() bool {
	return n.Kind == KindGate || n.Kind == KindMilestone || n.Kind == KindWait
}

// Record is what the store persists for one project.
type Record struct {
	ID               string            `json:"id"`
	OrgID            string            `json:"org_id"`
	Name             string            `json:"name"`
	NameKey          string            `json:"name_key,omitempty"`
	Goal             string            `json:"goal"`
	Status           string            `json:"status"`
	Control          string            `json:"control"`
	TemplateID       *string           `json:"template_id"`
	Params           map[string]string `json:"params,omitempty"`
	Locale           string            `json:"locale"`
	BudgetUSD        float64           `json:"budget_usd"`
	Budget           BudgetPolicy      `json:"budget"`
	MaxParallel      int               `json:"max_parallel"`
	Objectives       []Objective       `json:"objectives"`
	Nodes            []NodeDef         `json:"nodes"`
	Estimate         *Estimate         `json:"estimate"`
	StructureVersion int               `json:"structure_version"`
	RequestID        string            `json:"request_id,omitempty"`
	// Decisions maps a gate task id to "approved" or "rejected".
	Decisions  map[string]string `json:"decisions,omitempty"`
	Error      string            `json:"error,omitempty"`
	Planner    *PlannerInfo      `json:"planner,omitempty"`
	CreatedBy  string            `json:"created_by,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	StartedAt  *time.Time        `json:"started_at"`
	FinishedAt *time.Time        `json:"finished_at"`
	DeadlineAt *time.Time        `json:"deadline_at"`
	// BudgetWarned lists the early-warning thresholds (fractions of BudgetUSD)
	// already alerted, so a restart never repeats an alert (W5).
	BudgetWarned []float64 `json:"budget_warned,omitempty"`
	// Quality is the quality-review setting (Q1); nil means review off.
	Quality *Quality `json:"quality,omitempty"`
}
