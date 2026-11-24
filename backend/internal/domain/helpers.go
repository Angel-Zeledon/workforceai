package domain

import "time"

// Temporary helpers (docs/superpowers/specs/2026-11-23-ayudantes-temporales-design.md): one request split in
// independent parts. A helper is NOT an agent: every part is an ordinary Task of the responsible employee
// (Task.AgentID) carrying a TaskHelper, so it can never have more permissions than that employee.

// TaskHelper marks a task as one part of a helper group. nil = an ordinary task.
type TaskHelper struct {
	GroupID string `json:"group_id"`
	Index   int    `json:"index"` // 1-based
	Total   int    `json:"total"`
	Label   string `json:"label"`
}

// Helper group modes and states.
const (
	HelperModePosition = "position" // the responsible is the employee the person addressed
	HelperModeFree     = "free"     // a request of no department: the assistant coordinates

	HelperProposed = "proposed"
	HelperRunning  = "running"
	HelperJoining  = "joining" // every part is settled, the responsible joins the results
	HelperDone     = "done"
	HelperStopped  = "stopped"
	HelperFailed   = "failed"
	HelperDeclined = "declined"

	// StopReason of a stopped group.
	HelperStopUser = "user"
	HelperStopCap  = "cap"

	// Part states shown to the person.
	HelperPartPending = "pending"
	HelperPartRunning = "running"
	HelperPartDone    = "done"
	HelperPartFailed  = "failed"
	HelperPartStopped = "stopped"
)

// HelperPart is one part of a group. Text and Files are the material of the part (what the helper works on): kept
// in the store, never in the API shape.
type HelperPart struct {
	Index  int       `json:"index"`
	Label  string    `json:"label"`
	TaskID string    `json:"task_id,omitempty"`
	Status string    `json:"status,omitempty"`
	Text   string    `json:"-"`
	Files  []FileRef `json:"-"`
}

// HelperEstimate is the cost range shown before launching.
type HelperEstimate struct {
	MinUSD float64 `json:"min_usd"`
	MaxUSD float64 `json:"max_usd"`
}

// HelperGroup is the set of helpers of one request.
type HelperGroup struct {
	ID           string         `json:"id"`
	RequestID    string         `json:"request_id"`
	OwnerAgentID string         `json:"owner_agent_id"`
	Mode         string         `json:"mode"`
	Count        int            `json:"count"`
	MaxCount     int            `json:"max_count"`
	Parallel     int            `json:"parallel"`
	Parts        []HelperPart   `json:"parts"`
	Estimate     HelperEstimate `json:"estimate"`
	CapUSD       float64        `json:"cap_usd"`
	SpentUSD     float64        `json:"spent_usd"`
	Status       string         `json:"status"`
	AcceptedBy   string         `json:"accepted_by,omitempty"`

	// UnionTaskID is the final task of the responsible that joins the parts.
	UnionTaskID string `json:"union_task_id,omitempty"`
	// StopReason says why a group is stopped (user | cap).
	StopReason string     `json:"stop_reason,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}
