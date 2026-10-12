// Package memory is the long-term memory of the agents (docs/architecture/memory.md):
// what the organization already did (finished task outputs, reports, the files
// in the drive and the lessons agents declared) is indexed and retrieved when
// a new task needs it, so work done weeks ago is not lost in a large project.
//
// Retrieval goes through the Retriever interface. Today the only
// implementation is full-text (Postgres FTS, Spanish first); a semantic
// retriever over pgvector will be a second Retriever combined with Multi, with
// no change in the callers (the orchestrator, the memory.search tool, the API).
//
// Permissions: drive files are retrieved only under the folders the agent may
// read (drive.AgentFS.ReadableRoots, the same scope as fs.search); documents
// are scoped to the organization (Row-Level Security) and notes are private to
// the agent that wrote them. Everything handed to a model is sanitized,
// redacted and delimited as untrusted data.
package memory

import (
	"context"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Kinds of memory.
const (
	KindTask   = "task"
	KindReport = "report"
	KindFile   = "file"
	KindNote   = "note"
)

// Scopes of a document.
const (
	ScopeOrg   = "org"   // organization knowledge: task outputs, reports
	ScopeAgent = "agent" // private to the agent that wrote it: its notes
)

// Limits (the budget of what reaches a prompt).
const (
	MaxItems        = 6    // memories per task
	MaxChars        = 2500 // characters of snippets per task
	MaxSnippetChars = 420
	MaxNotes        = 4   // notes per finished task
	MaxNoteChars    = 280 // characters per note
	maxQueryTerms   = 14
	maxBodyChars    = 12000
)

// Doc is an indexed document (task output, report or note).
type Doc struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"` // task | report | note
	Ref         string    `json:"ref"`  // task:<id> | report:<id> | memory:<task id>.<n>
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	AgentID     string    `json:"agent_id"`
	Scope       string    `json:"scope"`
	ProjectID   string    `json:"project_id,omitempty"`
	ProjectName string    `json:"project_name,omitempty"`
	RequestID   string    `json:"request_id,omitempty"`
	SourceRef   string    `json:"source_ref,omitempty"` // a note points to the task it came from
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Suppressed  bool      `json:"-"`
}

// Item is one retrieved memory: a document or a drive file.
type Item struct {
	ID        string    `json:"id,omitempty"` // document id ("" for files)
	Ref       string    `json:"ref"`          // task:<id> | report:<id> | file:<path> | memory:<id>
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Snippet   string    `json:"snippet"`
	AgentID   string    `json:"agent_id,omitempty"`
	ProjectID string    `json:"-"`
	Project   string    `json:"project,omitempty"`
	SourceRef string    `json:"source_ref,omitempty"`
	Path      string    `json:"path,omitempty"` // files
	At        time.Time `json:"at"`
	Score     float64   `json:"score"`
	// Foreign: a file someone else wrote (it may hold third-party text).
	Foreign bool `json:"foreign,omitempty"`
	// Deletable: a person may remove it from the agent's memory (files are
	// managed in the drive).
	Deletable bool `json:"deletable"`

	cover float64 // share of the query terms in the text (ranking)
}

// Query is what a Retriever is asked.
type Query struct {
	AgentID string
	// Text is free text (task title + description + request); Terms, when set, wins.
	Text  string
	Terms []string
	Limit int
	// ExcludeRequest leaves out the documents of a request (they reach the agent as dependencies).
	ExcludeRequest string
	// PreferProject / PreferPath rank the same project (and its drive folder) higher.
	PreferProject string
	PreferPath    string
	// OnlyAgent restricts to what the agent itself produced (the Memory tab list).
	OnlyAgent bool
}

// Retriever finds the memories relevant to a query for one agent of an
// organization. It must never return what the agent may not read.
type Retriever interface {
	Retrieve(ctx context.Context, org string, q Query) ([]Item, error)
}

// SearchParams is the query of a DocStore.
type SearchParams struct {
	Terms          []string
	AgentID        string
	ExcludeRequest string
	OnlyAgent      bool // only documents produced by AgentID
	Limit          int
}

// DocHit is a document with its text-search score in [0,1).
type DocHit struct {
	Doc   Doc
	Score float64
}

// DocStore persists documents. Every call is scoped to one organization
// (Row-Level Security in Postgres).
type DocStore interface {
	// Upsert inserts or updates by ref. A suppressed document stays suppressed.
	Upsert(ctx context.Context, org string, d Doc) error
	// Search returns the documents any of the terms matches, best first, that
	// the agent may see (organization scope or its own notes), never suppressed.
	Search(ctx context.Context, org string, p SearchParams) ([]DocHit, error)
	// List returns the documents an agent produced, newest first, notes first.
	List(ctx context.Context, org, agentID string, limit int) ([]Doc, error)
	GetByRef(ctx context.Context, org, ref string) (Doc, error)
	Get(ctx context.Context, org, id string) (Doc, error)
	// Suppress hides a document from every retrieval (a person deleted it).
	Suppress(ctx context.Context, org, id, by string, at time.Time) error
	// Clear removes every document of the organization (demo reset only).
	Clear(ctx context.Context, org string) error
}

// FileSource retrieves the drive files an agent may read.
type FileSource interface {
	Files(ctx context.Context, org, agentID string, terms []string, limit int) ([]Item, error)
}

// ErrNotFound is returned when a document does not exist (or is hidden).
var ErrNotFound = domain.ErrNotFound
