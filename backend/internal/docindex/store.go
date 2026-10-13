package docindex

import (
	"context"
	"time"

	"aiworkforce/backend/internal/drive"
)

// Index states of a file (doc_index_state.status).
const (
	StatusPending  = "pending"
	StatusIndexing = "indexing"
	StatusIndexed  = "indexed"
	StatusSkipped  = "skipped"
	StatusFailed   = "failed"
)

// Reasons of skipped / failed / keyword-only states (stable codes).
const (
	ReasonUnsupported  = "unsupported_type" // binary or a format without text extraction
	ReasonNoText       = "no_text"          // extraction gave nothing (e.g. a scanned PDF)
	ReasonTooLarge     = "too_large"        // more chunks than the per-file cap
	ReasonOrgLimit     = "org_limit"        // the organization reached its chunk cap
	ReasonDeleted      = "deleted"          // in the trash
	ReasonError        = "error"            // read or store failure (retried)
	ReasonEmbedOff     = "embeddings_off"   // no embeddings provider configured
	ReasonEmbedPolicy  = "embeddings_policy"
	ReasonEmbedCap     = "embeddings_daily_cap"
	ReasonEmbedBudget  = "embeddings_budget"
	ReasonEmbedFailure = "embeddings_failed"
)

// Job is a file version claimed for indexing.
type Job struct {
	FileID   string
	Version  int
	Attempts int
}

// StoredChunk is a chunk with its (optional) embedding, as written.
type StoredChunk struct {
	Chunk
	Lang  string    // text search configuration of the chunk
	Vec   []float32 // nil = keyword only
	Model string    // embedding model of Vec
}

// Outcome is what indexing a job produced.
type Outcome struct {
	Status   string
	Reason   string
	Chunks   int
	Embedded int
	Model    string
}

// KeywordQuery is the full-text leg.
type KeywordQuery struct {
	Query string
	Lang  string // stemming configuration of the query (plus 'simple' always)
	// Under is the readable scope (nil = everything, empty = nothing).
	Under []string
	Limit int
}

// Candidate is a chunk with its vector, for the semantic leg.
type Candidate struct {
	Key Key
	Vec []float32
}

// CandidateQuery selects the semantic candidates: chunks with an embedding of
// Model, inside Under, preferring the ones that share a word with the query
// (OrTerms), then the most recent, capped at Limit.
type CandidateQuery struct {
	Under   []string
	Model   string
	OrTerms []string
	Limit   int
}

// Passage is a chunk resolved for display (path from the live file).
type Passage struct {
	Key       Key
	Path      string
	Name      string
	Page      int
	Line      int
	Start     int
	End       int
	Heading   string
	Text      string
	UpdatedBy drive.Principal
	Rank      float64 // keyword rank (keyword leg only)
}

// StatusCounts is the index state of a folder subtree.
type StatusCounts struct {
	Pending  int `json:"pending"`
	Indexing int `json:"indexing"`
	Indexed  int `json:"indexed"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
	// Unindexed: live files without any state yet (backfill not reached them).
	Unindexed int `json:"unindexed"`
	Chunks    int `json:"chunks"`
	Embedded  int `json:"embedded"`
	// TooLarge counts files skipped as too large (shown to people).
	TooLarge int `json:"too_large"`
}

// Settings of the organization.
type Settings struct {
	Enabled   bool       `json:"enabled"`
	UpdatedBy string     `json:"updated_by,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// Store persists the index. Every call is scoped to one organization (RLS in
// Postgres). Searches only ever see the CURRENT version of LIVE files (not in
// the trash, folder not in the trash) and apply Under before ranking.
type Store interface {
	// Settings / SetSettings: indexing on/off (default on when never set).
	Settings(ctx context.Context, org string) (Settings, error)
	SetSettings(ctx context.Context, org string, s Settings) error

	// Enqueue marks the current version of a file as pending.
	Enqueue(ctx context.Context, org, fileID string, version int) error
	// Reconcile marks pending every live file whose state is missing or stale
	// (another version), and removes chunks and state of files in the trash.
	// It returns how many files were (re)queued. limit bounds one pass.
	Reconcile(ctx context.Context, org string, limit int) (int, error)
	// Claim takes up to n pending jobs (or expired leases) with a lease, safe
	// with several replicas (FOR UPDATE SKIP LOCKED in Postgres).
	Claim(ctx context.Context, org string, n int, lease time.Duration) ([]Job, error)
	// Finish replaces the chunks of the file with the given ones and records the
	// outcome, only if the state still names this version (a newer write
	// re-queued it meanwhile: the result is dropped).
	Finish(ctx context.Context, org string, job Job, chunks []StoredChunk, out Outcome) error
	// Drop removes the chunks and the state of a file (trash, or index off).
	Drop(ctx context.Context, org, fileID string) error
	// PreviousVectors returns hash -> vector of the file's chunks embedded with model (reuse).
	PreviousVectors(ctx context.Context, org, fileID, model string) (map[string][]float32, error)
	// RequeueUnembedded marks pending up to limit indexed files that have
	// chunks without an embedding of model (embeddings were turned on later).
	RequeueUnembedded(ctx context.Context, org, model string, limit int) (int, error)
	// ChunkCount is the number of chunks of the organization.
	ChunkCount(ctx context.Context, org string) (int, error)

	Keyword(ctx context.Context, org string, q KeywordQuery) ([]Passage, error)
	Candidates(ctx context.Context, org string, q CandidateQuery) ([]Candidate, error)
	// Passages resolves keys to live, current, in-scope passages (others are dropped).
	Passages(ctx context.Context, org string, keys []Key, under []string) ([]Passage, error)
	Status(ctx context.Context, org string, under []string) (StatusCounts, error)
	// DropAll removes the whole index of the organization (indexing turned off).
	DropAll(ctx context.Context, org string) error

	// EmbedUsage / AddEmbedUsage: tokens embedded today (UTC day), for the daily cap.
	EmbedUsage(ctx context.Context, org string, day time.Time) (int, error)
	AddEmbedUsage(ctx context.Context, org string, day time.Time, tokens int, usd float64) error
}
