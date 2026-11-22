// Package agenthistory keeps the configuration history of every agent (docs/architecture/agent-history.md): each
// change, by any route, leaves an immutable version with a full snapshot of what is configurable, who changed it,
// when and which fields changed; any version can be restored as a NEW version, through the same services, rules and
// permissions as changing those fields by hand, and never to anything freer (autonomy, approval permissions,
// skills that no longer exist, a spend cap above its department).
//
// The version is a snapshot, not a diff each handler must remember to write: Service.Record composes it from the
// sources (Source), compares it with the latest one and appends only when something changed. Two common hooks call
// it (the HTTP middleware of the API and the event Observer), so no handler writes history itself.
package agenthistory

import (
	"encoding/json"
	"time"

	"aiworkforce/backend/internal/policy"
)

// Config is the snapshot of what is configurable on one agent. Slices are sorted so that two snapshots of the
// same configuration marshal to the same bytes. Timestamps and "who touched it last" are left out on purpose: they
// change on every save and would make every save look like a change.
type Config struct {
	Name       string `json:"name"`
	Title      string `json:"title"`
	Role       string `json:"role"`
	Department string `json:"department"`

	Instructions string   `json:"instructions"`
	Creativity   string   `json:"creativity"`
	Tags         []string `json:"tags"` // custom skill tags of the work profile
	Tone         string   `json:"tone"`

	Autonomy    string       `json:"autonomy"`
	Permissions *Permissions `json:"permissions"` // nil: the agent still uses the previous behaviour (no level chosen)

	Skills    []SkillRef `json:"skills"`     // assigned skills of the library
	BudgetUSD float64    `json:"budget_usd"` // explicit monthly cap, 0 = none of its own

	Paused    bool `json:"paused"`
	ReadOnly  bool `json:"read_only"`
	Rehearsal bool `json:"rehearsal"`

	Tools []string `json:"tools"` // tools of the agent (a job description can only narrow the role's)
	Job   *Job     `json:"job"`

	Access  *Access       `json:"access"`  // who owns and may use the agent (offices)
	Folders []FolderGrant `json:"folders"` // what the agent may do in folders of the drive

	// Secrets holds only a fingerprint per secret field (never the value): the history can say "it changed".
	Secrets map[string]string `json:"secrets,omitempty"`
}

// Permissions are the approval permissions of the agent (org_settings.rules.permissions.agents[id]).
type Permissions struct {
	Exceptions  []Exception `json:"exceptions"`
	FloorLifted []string    `json:"floor_lifted"`
}

// Exception is one category treated differently for the agent.
type Exception struct {
	Category string        `json:"category"`
	Mode     string        `json:"mode"`
	If       *policy.AskIf `json:"if,omitempty"`
}

// SkillRef is an assigned skill.
type SkillRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Job is the job description (organization model) without its department and budget, which have their own fields.
type Job struct {
	Responsibilities []string        `json:"responsibilities"`
	ReportsTo        string          `json:"reports_to"` // "kind:id" or ""
	Escalation       string          `json:"escalation"`
	Schedule         json.RawMessage `json:"schedule,omitempty"`
}

// Access is who owns the agent and who may use it.
type Access struct {
	Owner      string   `json:"owner"` // "kind:id"
	Visibility string   `json:"visibility"`
	Shares     []string `json:"shares"`
}

// FolderGrant is the access of the agent to one folder.
type FolderGrant struct {
	Path  string `json:"path"`
	Level string `json:"level"`
}

// Fields lists the fields of Config in display order (the JSON names).
var Fields = []string{"name", "title", "role", "department", "instructions", "creativity", "tags", "tone", "autonomy",
	"permissions", "skills", "budget_usd", "paused", "read_only", "rehearsal", "tools", "job", "access", "folders"}

// Actor kinds.
const (
	ActorPerson = "person"
	ActorSystem = "system"
)

// Version is one immutable entry of the history.
type Version struct {
	AgentID   string    `json:"agent_id"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Actor     string    `json:"actor"`      // user id, or "system"
	ActorKind string    `json:"actor_kind"` // person | system
	ActorName string    `json:"actor_name,omitempty"`
	// Source says how the change arrived: the route pattern ("PUT /agents/{id}/tone"), an event or cause
	// ("trust.auto_demotion", "agent.created"), "baseline" (how it was when the history started) or "restore".
	Source       string   `json:"source"`
	Fields       []string `json:"fields"` // what changed against the previous version (empty for the first)
	RestoredFrom *int     `json:"restored_from,omitempty"`
	Config       Config   `json:"config"`
}

// Change is one field of a version against the previous one.
type Change struct {
	Field  string          `json:"field"`
	From   json.RawMessage `json:"from,omitempty"`
	To     json.RawMessage `json:"to,omitempty"`
	Secret bool            `json:"secret,omitempty"` // only "it changed": the values are never kept
}

// Detail is a version with its changes against the previous one.
type Detail struct {
	Version  Version  `json:"version"`
	Previous *int     `json:"previous,omitempty"`
	Changes  []Change `json:"changes"`
}

// Cause is who and what produced a change.
type Cause struct {
	Actor     string
	ActorName string
	Source    string
	// RestoredFrom is the version a restore brought back.
	RestoredFrom *int
}
