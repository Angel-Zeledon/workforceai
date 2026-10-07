package domain

import "time"

// AuditQuery filters the audit trail. Zero values mean "no filter".
type AuditQuery struct {
	From      *time.Time // inclusive
	To        *time.Time // exclusive
	Actor     string     // exact match
	Action    string     // exact, or a prefix when it ends with "*" ("approval.*")
	Entity    string     // exact match
	RequestID string     // exact match
	UpToSeq   *int64     // only chained entries with seq <= *UpToSeq (nil = no bound); entries from before the chain are always included
	Limit     int        // page size (default 100, max 1000)
	Cursor    string     // opaque, from AuditPage.NextCursor
	Desc      bool       // newest first (default: oldest first, the export order)
}

// AuditPage is one page of audit entries.
type AuditPage struct {
	Items      []AuditLog `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

// AuditHead is the last chained entry of an organization.
type AuditHead struct {
	Seq  int64     `json:"seq"`
	Hash string    `json:"hash"`
	TS   time.Time `json:"-"` // timestamp of the head entry: appends keep timestamps strictly increasing
}

// MaxAuditPage is the largest page a caller may request.
const MaxAuditPage = 1000

// DefaultAuditPage is the page size when none is given.
const DefaultAuditPage = 100
