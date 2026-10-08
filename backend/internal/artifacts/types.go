// Package artifacts implements the agent workspaces: typed artifacts (sheet,
// doc, table, board, chart, pdf, form, inbox, agenda) that humans and agents
// edit together (docs/architecture/agent-workspaces.md).
//
// Content is canonical data (aiw.<kind>/1), never code or HTML, validated and
// size-limited here because the client is not trusted. Every change is an
// immutable version; concurrent edits are merged per unit (cell, block, row,
// card...) or rejected with a conflict. Agents are given an attachment mode
// (none|read|propose|edit) and can never approve anything.
package artifacts

import (
	"encoding/json"
	"time"
)

type Kind string

const (
	KindSheet  Kind = "sheet"
	KindDoc    Kind = "doc"
	KindTable  Kind = "table"
	KindBoard  Kind = "board"
	KindChart  Kind = "chart"
	KindPDF    Kind = "pdf"
	KindForm   Kind = "form"
	KindInbox  Kind = "inbox"
	KindAgenda Kind = "agenda"
)

// Kinds is the catalog, in display order.
var Kinds = []Kind{KindSheet, KindDoc, KindTable, KindBoard, KindChart, KindPDF, KindForm, KindInbox, KindAgenda}

func (k Kind) Valid() bool {
	for _, x := range Kinds {
		if x == k {
			return true
		}
	}
	return false
}

func (k Kind) Schema() string { return "aiw." + string(k) + "/1" }

// SuggestedKinds orders the "+" menu per role: a suggestion, never a restriction.
var SuggestedKinds = map[string][]Kind{
	"accounting": {KindSheet, KindDoc, KindChart, KindTable},
	"legal":      {KindDoc, KindTable, KindSheet, KindPDF},
	"sales":      {KindDoc, KindBoard, KindSheet, KindChart},
	"analyst":    {KindChart, KindSheet, KindDoc, KindTable},
	"operations": {KindBoard, KindTable, KindSheet, KindAgenda},
	"assistant":  {KindInbox, KindAgenda, KindDoc, KindForm},
	"hr":         {KindTable, KindBoard, KindDoc, KindForm},
}

const (
	StatusDraft    = "draft"
	StatusInReview = "in_review"
	StatusApproved = "approved"
	StatusSent     = "sent"
	StatusArchived = "archived"

	BuildQueued   = "queued"
	BuildBuilding = "building"
	BuildReady    = "ready_for_review"
	BuildDone     = "done"
	BuildBlocked  = "blocked"

	// Attachment modes of an agent. There is deliberately no "approve".
	ModeNone    = "none"
	ModeRead    = "read"
	ModePropose = "propose"
	ModeEdit    = "edit"

	ActorUser   = "user"
	ActorAgent  = "agent"
	ActorSystem = "system"

	RelEmbeds      = "embeds"
	RelSourceOf    = "source_of"
	RelDerivedFrom = "derived_from"
	RelRefersTo    = "refers_to"
)

func validStatus(s string) bool {
	switch s {
	case StatusDraft, StatusInReview, StatusApproved, StatusSent, StatusArchived:
		return true
	}
	return false
}

// Locked statuses freeze the content: only a human moving the status back unlocks it.
func lockedStatus(s string) bool { return s == StatusApproved || s == StatusSent }

type ActorRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type Attachment struct {
	AgentID string `json:"agent_id"`
	Mode    string `json:"mode"`
}

// Meta is the public descriptor of an artifact (lists and events).
type Meta struct {
	ID                string       `json:"id"`
	Kind              Kind         `json:"kind"`
	SchemaVersion     string       `json:"schema_version"`
	Title             string       `json:"title"`
	Status            string       `json:"status"`
	HeadVersion       int          `json:"head_version"`
	CreatedBy         ActorRef     `json:"created_by"`
	LastAuthor        ActorRef     `json:"last_author"`
	TaskID            *string      `json:"task_id,omitempty"`
	RequestID         *string      `json:"request_id,omitempty"`
	CustomerID        *string      `json:"customer_id,omitempty"`
	ProjectID         *string      `json:"project_id,omitempty"`
	DeliverableID     *string      `json:"deliverable_id,omitempty"`
	Progress          int          `json:"progress"`
	BuildState        string       `json:"build_state"`
	Locale            string       `json:"locale"`
	DependsOnArtifact []string     `json:"depends_on_artifacts"`
	PendingProposals  int          `json:"pending_proposals"`
	Tainted           bool         `json:"tainted"`
	Locked            bool         `json:"locked"`
	SizeBytes         int          `json:"size_bytes"`
	Attachments       []Attachment `json:"attachments"`
	CreatedAt         time.Time    `json:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at"`
}

// Artifact is the meta plus the head content.
type Artifact struct {
	Meta
	Content json.RawMessage `json:"content"`
	Version int             `json:"version"`
}

type VersionInfo struct {
	ArtifactID  string    `json:"artifact_id"`
	Version     int       `json:"version"`
	BaseVersion *int      `json:"base_version"`
	Author      ActorRef  `json:"author"`
	Source      string    `json:"source"`
	Summary     string    `json:"summary"`
	TaskID      *string   `json:"task_id,omitempty"`
	ContentHash string    `json:"content_hash,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Version is one immutable version with its content.
type Version struct {
	VersionInfo
	Content json.RawMessage `json:"content,omitempty"`
}

type Anchor struct {
	Sheet string `json:"sheet,omitempty"`
	Range string `json:"range,omitempty"`
	BID   string `json:"bid,omitempty"`
	Card  string `json:"card,omitempty"`
	Page  int    `json:"page,omitempty"`
}

type Link struct {
	ID       string  `json:"id"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	Relation string  `json:"relation"`
	Anchor   *Anchor `json:"anchor,omitempty"`
	Alias    *string `json:"alias,omitempty"`
	Stale    bool    `json:"stale,omitempty"`

	// Internal bookkeeping (not part of the contract).
	Auto          bool `json:"-"`
	SyncedVersion int  `json:"-"`
}

type Template struct {
	ID           string            `json:"id"`
	Kind         Kind              `json:"kind"`
	Title        map[string]string `json:"title"`
	SuggestedFor []string          `json:"suggested_for"`
}

type KindInfo struct {
	Kind          Kind   `json:"kind"`
	SchemaVersion string `json:"schema_version"`
	Suggested     bool   `json:"suggested,omitempty"`
}

type Deliverable struct {
	DeliverableID string `json:"deliverable_id"`
	Title         string `json:"title"`
	AgentID       string `json:"agent_id"`
	Status        string `json:"status"`
	Progress      int    `json:"progress"`
	BuildState    string `json:"build_state"`
	Artifacts     []Meta `json:"artifacts"`
}

type ProjectWorkspace struct {
	ProjectID    string        `json:"project_id"`
	Name         string        `json:"name"`
	Status       string        `json:"status"`
	Deliverables []Deliverable `json:"deliverables"`
	Links        []Link        `json:"links"`
}

// ---- inputs ----

type CreateInput struct {
	Kind       Kind            `json:"kind"`
	Title      string          `json:"title"`
	Content    json.RawMessage `json:"content,omitempty"`
	TemplateID string          `json:"template_id,omitempty"`
	AgentID    string          `json:"agent_id,omitempty"`
	CustomerID string          `json:"customer_id,omitempty"`
	TaskID     string          `json:"task_id,omitempty"`
	Locale     string          `json:"locale,omitempty"`
}

type PatchInput struct {
	Title         *string `json:"title,omitempty"`
	Status        *string `json:"status,omitempty"`
	ProjectID     *string `json:"project_id,omitempty"`
	DeliverableID *string `json:"deliverable_id,omitempty"`
}

type SaveInput struct {
	BaseVersion int             `json:"base_version"`
	Content     json.RawMessage `json:"content"`
	Summary     string          `json:"summary,omitempty"`

	source string // internal: overrides the version source (accept_proposal)
}

// SaveResult is the answer of a version save.
type SaveResult struct {
	Version int             `json:"version"`
	Merged  bool            `json:"merged"`
	Content json.RawMessage `json:"content,omitempty"`
}

// Conflict is returned (and rendered as HTTP 409) when a save cannot be merged.
type Conflict struct {
	HeadVersion int            `json:"head_version"`
	Conflicts   []UnitConflict `json:"conflicts"`
}

type UnitConflict struct {
	Unit         string          `json:"unit"`
	Mine         json.RawMessage `json:"mine,omitempty"`
	Theirs       json.RawMessage `json:"theirs,omitempty"`
	TheirsAuthor *ActorRef       `json:"theirs_author,omitempty"`
}

func (c *Conflict) Error() string {
	return "conflict: the artifact changed since your base version (head " + itoa(c.HeadVersion) + ")"
}

// Actor is who performs an operation. Agents are checked against their attachment.
type Actor struct {
	Kind string // user|agent|system
	ID   string
	// CanApprove: the human holds the approve capability (admin/owner). Never true for agents.
	CanApprove bool
	// CanDelete: the human may archive artifacts they did not create (admin+).
	CanDelete bool
}

func (a Actor) Ref() ActorRef { return ActorRef{Kind: a.Kind, ID: a.ID} }
func (a Actor) IsAgent() bool { return a.Kind == ActorAgent }
