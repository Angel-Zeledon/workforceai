// Package inboxes implements data inboxes (docs/architecture/data-inboxes.md): a generic INBOUND endpoint any
// software can push data to (a script, an ERP export job, an automation webhook, curl) without the platform
// integrating that software.
//
// Rules that never change:
//   - an inbox is a bearer token for ONE destination folder; only the SHA-256 of the token is stored and the
//     plaintext is shown once (at creation and at each rotation), like the client portal links;
//   - only humans with org:manage create, edit, rotate and revoke inboxes; agents have no tool for it;
//   - every push becomes a NEW file (or a new version of a file the SAME inbox wrote before) in the drive,
//     attributed to the inbox principal (drive.PrincipalInbox). A file a person or an agent wrote is never
//     overwritten: a name clash gets a unique suffix;
//   - what arrives is untrusted external DATA. It is never interpreted here, and because its author is not the
//     agent that later reads it, fs.read marks it as authored by others (tainted): any external action that
//     follows needs a human approval (internal/drive/gwtool.go);
//   - read-only mode and the kill switch refuse pushes; every push is audited with metadata only (inbox, size,
//     type, file ids), never the content.
package inboxes

import (
	"context"
	"errors"
	"time"
)

// Content kinds an inbox may accept.
const (
	KindJSON = "json"  // application/json bodies and .json / .jsonl files
	KindCSV  = "csv"   // text/csv bodies and .csv / .tsv files
	KindFile = "files" // any other file type the drive allows (raw bodies or multipart file parts)
)

// AllKinds is the order shown in the UI.
var AllKinds = []string{KindJSON, KindCSV, KindFile}

// Inbox states (derived, never stored).
const (
	StatusActive  = "active"
	StatusRevoked = "revoked"
)

// Limits.
const (
	MaxName             = 80
	DefaultMaxBytes     = 5 << 20
	MinMaxBytes         = 1 << 10
	MaxParts            = 20 // files in one multipart push
	MaxActive           = 50 // active inboxes per organization
	DefaultRatePerMin   = 30
	MaxRatePerMin       = 600
	MaxIdempotencyKey   = 200
	DefaultPushesListed = 20
	MaxPushesListed     = 100
	tokenBytes          = 32
	// TokenPrefix makes a leaked token recognizable by secret scanners and people ("aiwi" = AI Workforce inbox).
	TokenPrefix = "aiwi_"
)

// Errors with their own HTTP mapping (internal/api/inboxes.go). ErrUnauthorized covers unknown, revoked and
// mismatched tokens alike: the caller cannot tell them apart.
var (
	ErrUnauthorized     = errors.New("inboxes: unauthorized")
	ErrKindNotAllowed   = errors.New("inboxes: content kind not allowed")
	ErrTooLarge         = errors.New("inboxes: payload too large")
	ErrBadPayload       = errors.New("inboxes: bad payload")
	ErrReadOnly         = errors.New("inboxes: read-only mode")
	ErrKillSwitch       = errors.New("inboxes: kill switch active")
	ErrInProgress       = errors.New("inboxes: a push with this idempotency key is in progress")
	ErrCustomerArchived = errors.New("inboxes: the inbox customer is archived or gone")
)

// Inbox is one data inbox.
type Inbox struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	// Folder is where pushes land. Without a customer it is an absolute drive path (/Shared/... or
	// /Projects/...). With a customer it is RELATIVE to that customer's folder (/Clients/<name>), resolved at
	// every push, so renaming the customer's folder never breaks the inbox nor moves data outside it.
	Folder     string     `json:"folder"`
	CustomerID string     `json:"customer_id"`
	Allowed    []string   `json:"allowed"`
	MaxBytes   int64      `json:"max_bytes"`
	RatePerMin int        `json:"rate_per_min"`
	TokenHash  string     `json:"-"`
	TokenHint  string     `json:"token_hint"` // the last 4 characters, to tell tokens apart; never enough to use one
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	RotatedAt  *time.Time `json:"rotated_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	RevokedBy  string     `json:"revoked_by,omitempty"`
	LastPushAt *time.Time `json:"last_push_at"`
	PushCount  int64      `json:"push_count"`
}

// Status derives the state of the inbox.
func (i Inbox) Status() string {
	if i.RevokedAt != nil {
		return StatusRevoked
	}
	return StatusActive
}

// Allows reports whether the inbox accepts a content kind.
func (i Inbox) Allows(kind string) bool {
	for _, k := range i.Allowed {
		if k == kind {
			return true
		}
	}
	return false
}

// View is an inbox as the admin UI sees it (never the token).
type View struct {
	Inbox
	Status string `json:"status"`
	// FolderPath is the absolute destination (the customer's folder resolved); "" when it cannot be resolved.
	FolderPath string `json:"folder_path"`
	// PushPath is the path of the public endpoint, relative to the API origin ("/api/v1/inbox/<id>").
	PushPath string `json:"push_path"`
}

// Created is returned at creation and at rotation: Token is the only copy of the secret.
type Created struct {
	View
	Token string `json:"token"`
}

// Input creates or edits an inbox. Folder and CustomerID are fixed after creation (a new destination is a new
// inbox, so the history of where data went stays readable).
type Input struct {
	Name       string   `json:"name"`
	Folder     string   `json:"folder"`
	CustomerID string   `json:"customer_id"`
	Allowed    []string `json:"allowed"`
	MaxBytes   int64    `json:"max_bytes"`
	RatePerMin int      `json:"rate_per_min"`
}

// PushedFile is one file a push wrote.
type PushedFile struct {
	FileID  string `json:"file_id"`
	Path    string `json:"path"`
	Name    string `json:"name"`
	Version int    `json:"version"`
	Size    int64  `json:"size"`
	Mime    string `json:"mime"`
	Kind    string `json:"kind"`
	// Created is false when the push added a version to a file this same inbox wrote before.
	Created bool `json:"created"`
	// Unchanged: the content equals the current version of that file, nothing new was stored.
	Unchanged bool `json:"unchanged"`
}

// Push states.
const (
	PushPending = "pending" // reserved by an idempotency key, being written
	PushDone    = "done"
)

// Push is one accepted push (metadata only, never content).
type Push struct {
	ID      string `json:"id"`
	InboxID string `json:"inbox_id"`
	// IdemKey is the SHA-256 of the caller's X-Idempotency-Key ("" = none). The key itself is not stored: it may
	// carry the caller's own identifiers.
	IdemKey    string       `json:"-"`
	Status     string       `json:"status"`
	Size       int64        `json:"size"`
	Files      []PushedFile `json:"files"`
	ReceivedAt time.Time    `json:"received_at"`
}

// Result is what the public endpoint answers.
type Result struct {
	PushID     string       `json:"push_id"`
	InboxID    string       `json:"inbox_id"`
	ReceivedAt time.Time    `json:"received_at"`
	Size       int64        `json:"size"`
	Files      []PushedFile `json:"files"`
	// Replayed is true when the idempotency key was seen before: nothing new was written, this is the first result.
	Replayed bool `json:"replayed"`
	// IgnoredFields counts multipart form fields without a file (they are not stored).
	IgnoredFields int `json:"ignored_fields,omitempty"`
}

// Store persists inboxes and pushes. Every method but InboxByHash is scoped to org (Row-Level Security in
// Postgres, migration 803).
type Store interface {
	CreateInbox(ctx context.Context, org string, i Inbox) error
	// UpdateInbox writes name, allowed, max_bytes, rate_per_min and updated_at.
	UpdateInbox(ctx context.Context, org string, i Inbox) error
	GetInbox(ctx context.Context, org, id string) (Inbox, error)
	// ListInboxes returns every inbox of org, newest first.
	ListInboxes(ctx context.Context, org string) ([]Inbox, error)
	// SetToken replaces the token hash (rotation).
	SetToken(ctx context.Context, org, id, hash, hint string, at time.Time) error
	RevokeInbox(ctx context.Context, org, id, by string, at time.Time) error
	// InboxByHash finds an inbox before the tenant is known (the hash is the only key).
	InboxByHash(ctx context.Context, hash string) (org string, i Inbox, err error)

	// ReservePush inserts a pending push. With a non-empty IdemKey it fails with domain.ErrConflict when the
	// inbox already has a push with that key.
	ReservePush(ctx context.Context, org string, p Push) error
	// CompletePush stores the result of a reserved push and bumps the inbox counters.
	CompletePush(ctx context.Context, org string, p Push) error
	// ReleasePush drops a pending push that failed (its key may be retried).
	ReleasePush(ctx context.Context, org, inboxID, id string) error
	// PushByKey is the push of an idempotency key (domain.ErrNotFound when none).
	PushByKey(ctx context.Context, org, inboxID, idemKey string) (Push, error)
	// Pushes lists the completed pushes of an inbox, newest first.
	Pushes(ctx context.Context, org, inboxID string, limit int) ([]Push, error)
}
