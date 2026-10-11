package application

import (
	"context"
	"time"
)

// V2 (agent work profile, thought audit, real-time intervention). All of it is
// additive: an organization that never edits a profile, never reads a trace and
// never steers a task behaves exactly as before.

// ---- work profile ----

// Creativity levels of an agent's work profile. The runtime maps each one to a
// bounded sampling temperature per provider; "medium" is the legacy behaviour.
const (
	CreativityLow    = "low"
	CreativityMedium = "medium"
	CreativityHigh   = "high"
)

// Limits of the work profile (sent to the UI so it can validate before saving).
const (
	MaxInstructionsRunes = 2000
	MaxSkills            = 12
	MaxSkillRunes        = 32
	MaxSteerNoteRunes    = 500
	MaxSteerNotesSent    = 5
)

// ProfileSnapshot is the editable content of a work profile. Tone is the
// per-agent regional tone override ("" = the organization tone).
type ProfileSnapshot struct {
	Instructions string   `json:"instructions"`
	Creativity   string   `json:"creativity"`
	Skills       []string `json:"skills"`
	Tone         string   `json:"tone"`
}

// AgentProfile is the current work profile of one agent. Version 0 means it was
// never edited (the defaults apply and nothing extra is sent to the runtime).
type AgentProfile struct {
	AgentID string `json:"agent_id"`
	ProfileSnapshot
	Version   int        `json:"version"`
	UpdatedBy string     `json:"updated_by"`
	UpdatedAt *time.Time `json:"updated_at"`
}

// ProfileChange is one changed field of a version: what it was and what it is now.
type ProfileChange struct {
	From any `json:"from"`
	To   any `json:"to"`
}

// ProfileVersion is one immutable entry of the profile history.
type ProfileVersion struct {
	AgentID      string                   `json:"agent_id"`
	Version      int                      `json:"version"`
	Author       string                   `json:"author"`
	CreatedAt    time.Time                `json:"created_at"`
	RestoredFrom *int                     `json:"restored_from"`
	Diff         map[string]ProfileChange `json:"diff"`
	Snapshot     ProfileSnapshot          `json:"snapshot"`
}

// ---- thought audit ----

// TraceEvent is something that happened around a runtime call: a policy
// decision on a tool request, an approval, a review verdict, a retry, a human
// steer note, an "apply now" restart or a reassignment.
type TraceEvent struct {
	TS   time.Time      `json:"ts"`
	Type string         `json:"type"` // tool_decision|approval|review|retry|steer|apply_now|reassign
	Data map[string]any `json:"data"`
}

// TraceTool is one tool request the agent made, with what the policy layer decided.
type TraceTool struct {
	Tool           string `json:"tool"`
	Action         string `json:"action"`
	Risk           string `json:"risk"`
	Decision       string `json:"decision"` // allow|require_approval|deny|"" (not decided)
	RuleID         string `json:"rule_id,omitempty"`
	Reason         string `json:"reason,omitempty"`
	ApprovalStatus string `json:"approval_status,omitempty"`
}

// TraceOutput is a short summary of the structured output (never the whole output).
type TraceOutput struct {
	Summary         string  `json:"summary"`
	Confidence      float64 `json:"confidence"`
	Findings        int     `json:"findings"`
	Recommendations int     `json:"recommendations"`
}

// PromptSegment is a slice of the prompt: plain text the runtime wrote, or an
// "untrusted" block of external content that was delimited as data.
type PromptSegment struct {
	Kind      string `json:"kind"` // text|untrusted
	Label     string `json:"label,omitempty"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

// TracePrompt is the redacted prompt of a call. Absent when the organization
// turned prompt storage off.
type TracePrompt struct {
	System   string          `json:"system"`
	Segments []PromptSegment `json:"segments"`
}

// Trace is the record of ONE runtime call of an agent on a task.
type Trace struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	TaskID    string    `json:"task_id"`
	RequestID string    `json:"request_id"`
	Kind      string    `json:"kind"` // run-task|rework|read-followup
	Attempt   int       `json:"attempt"`
	CreatedAt time.Time `json:"created_at"`

	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	LatencyMS    int     `json:"latency_ms"`
	Status       string  `json:"status"` // ok|error
	Error        string  `json:"error,omitempty"`

	// Rationale is the summary the agent itself reported. It is NOT the model's
	// hidden chain of thought, which is never available.
	Rationale         string       `json:"rationale"`
	OutputSummary     TraceOutput  `json:"output_summary"`
	ToolRequests      []TraceTool  `json:"tool_requests"`
	Events            []TraceEvent `json:"events"`
	SteerNotesApplied int          `json:"steer_notes_applied"`
	PromptStored      bool         `json:"prompt_stored"`
	Truncated         bool         `json:"truncated"`
	Prompt            *TracePrompt `json:"prompt,omitempty"`
}

// ---- intervention ----

// SteerNote is a note a human sent to steer a task (additional guidance, never authority).
type SteerNote struct {
	ID          string     `json:"id"`
	TaskID      string     `json:"task_id"`
	AgentID     string     `json:"agent_id"`
	Note        string     `json:"note"`
	Author      string     `json:"author"`
	CreatedAt   time.Time  `json:"created_at"`
	DeliveredAt *time.Time `json:"delivered_at"`
}

// TraceSettings is the per-organization trace policy.
type TraceSettings struct {
	StorePrompts  bool `json:"store_prompts"`
	RetentionDays int  `json:"retention_days"`
}

// V2Store is the persistence port of the V2 features. A separate port (like
// ConfigStore) so it evolves on its own; the memory and Postgres stores
// implement it, every method is org-scoped and Postgres applies RLS.
type V2Store interface {
	GetAgentProfile(ctx context.Context, orgID, agentID string) (AgentProfile, bool, error)
	// SaveAgentProfile atomically upserts the current profile and appends v to the history.
	SaveAgentProfile(ctx context.Context, orgID string, p AgentProfile, v ProfileVersion) error
	// ListProfileVersions returns the history of an agent, newest first.
	ListProfileVersions(ctx context.Context, orgID, agentID string) ([]ProfileVersion, error)
	GetProfileVersion(ctx context.Context, orgID, agentID string, version int) (ProfileVersion, error)
	// ListAgentProfiles returns every edited profile of the organization (routing hints).
	ListAgentProfiles(ctx context.Context, orgID string) ([]AgentProfile, error)

	// AddTrace stores t; when t.Attempt is 0 the store numbers it (1 + traces of the task).
	AddTrace(ctx context.Context, orgID string, t Trace) error
	// AppendTraceEvent appends an event to the latest trace of a task (false: the task has none).
	AppendTraceEvent(ctx context.Context, orgID, taskID string, e TraceEvent) (bool, error)
	// ListTraces returns the traces of an agent newest first, optionally only those before a time.
	ListTraces(ctx context.Context, orgID, agentID string, limit int, before time.Time) ([]Trace, error)
	GetTrace(ctx context.Context, orgID, agentID, id string) (Trace, error)
	// PurgeTraces deletes the traces created before t and returns how many.
	PurgeTraces(ctx context.Context, orgID string, t time.Time) (int, error)
	GetTraceStorePrompts(ctx context.Context, orgID string) (bool, error)
	SetTraceStorePrompts(ctx context.Context, orgID string, on bool) error

	AddSteerNote(ctx context.Context, orgID string, n SteerNote) error
	// ListSteerNotes returns the notes of a task, oldest first.
	ListSteerNotes(ctx context.Context, orgID, taskID string) ([]SteerNote, error)
	MarkSteerDelivered(ctx context.Context, orgID string, ids []string, at time.Time) error
}
