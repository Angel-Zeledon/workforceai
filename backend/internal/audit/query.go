package audit

import (
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// NormalizeQuery applies page-size defaults and limits.
func NormalizeQuery(q domain.AuditQuery) domain.AuditQuery {
	switch {
	case q.Limit <= 0:
		q.Limit = domain.DefaultAuditPage
	case q.Limit > domain.MaxAuditPage:
		q.Limit = domain.MaxAuditPage
	}
	q.Actor, q.Action, q.Entity, q.RequestID = strings.TrimSpace(q.Actor), strings.TrimSpace(q.Action), strings.TrimSpace(q.Entity), strings.TrimSpace(q.RequestID)
	return q
}

// ActionMatches implements the "type" filter: exact, or prefix when pattern
// ends with "*".
func ActionMatches(pattern, action string) bool {
	if pattern == "" {
		return true
	}
	if p, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(action, p)
	}
	return pattern == action
}

// Match reports whether e satisfies the filters of q (cursor and paging
// excluded). Used by the in-memory store; the Postgres store builds SQL.
func Match(q domain.AuditQuery, e domain.AuditLog) bool {
	if q.From != nil && e.TS.Before(*q.From) {
		return false
	}
	if q.To != nil && !e.TS.Before(*q.To) {
		return false
	}
	if q.UpToSeq != nil && e.Seq > *q.UpToSeq {
		return false
	}
	if q.Actor != "" && e.Actor != q.Actor {
		return false
	}
	if q.Entity != "" && e.Entity != q.Entity {
		return false
	}
	if q.RequestID != "" && e.RequestID != q.RequestID {
		return false
	}
	return ActionMatches(q.Action, e.Action)
}

// Cursor is the keyset position of a page: entries are ordered by (ts, id).
type Cursor struct {
	TS time.Time
	ID string
}

// Encode renders the cursor as an opaque token.
func (c Cursor) Encode() string {
	raw := strconv.FormatInt(c.TS.UnixMicro(), 10) + "|" + c.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor parses a token produced by Encode.
func DecodeCursor(s string) (Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: bad cursor", domain.ErrInvalid)
	}
	us, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return Cursor{}, fmt.Errorf("%w: bad cursor", domain.ErrInvalid)
	}
	n, err := strconv.ParseInt(us, 10, 64)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: bad cursor", domain.ErrInvalid)
	}
	return Cursor{TS: time.UnixMicro(n).UTC(), ID: id}, nil
}

// After reports whether e comes strictly after c in the requested order.
func (c Cursor) After(e domain.AuditLog, desc bool) bool {
	ets := e.TS.UTC().Truncate(time.Microsecond)
	cmp := ets.Compare(c.TS)
	if cmp == 0 {
		cmp = strings.Compare(e.ID, c.ID)
	}
	if desc {
		return cmp < 0
	}
	return cmp > 0
}

// CursorOf returns the cursor that resumes after e.
func CursorOf(e domain.AuditLog) Cursor {
	return Cursor{TS: e.TS.UTC().Truncate(time.Microsecond), ID: e.ID}
}

// ---- exports ----

// WriteJSONLRecord writes one entry as a JSON line.
func WriteJSONLRecord(w io.Writer, e domain.AuditLog) error {
	b, err := json.Marshal(ToRecord(e))
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// CSVHeader is the column order of the CSV export.
var CSVHeader = []string{"seq", "id", "ts", "org_id", "actor", "action", "entity", "entity_id", "request_id", "details", "prev_hash", "hash"}

// CSVWriter writes the CSV export. Cells that spreadsheets would interpret as
// formulas are prefixed with a quote (CSV injection).
type CSVWriter struct {
	w       *csv.Writer
	started bool
}

// NewCSVWriter wraps w.
func NewCSVWriter(w io.Writer) *CSVWriter { return &CSVWriter{w: csv.NewWriter(w)} }

// Write appends one entry (the header goes first).
func (c *CSVWriter) Write(e domain.AuditLog) error {
	if !c.started {
		c.started = true
		if err := c.w.Write(CSVHeader); err != nil {
			return err
		}
	}
	r := ToRecord(e)
	return c.w.Write([]string{strconv.FormatInt(r.Seq, 10), safeCell(r.ID), r.TS, safeCell(r.OrgID), safeCell(r.Actor), safeCell(r.Action),
		safeCell(r.Entity), safeCell(r.EntityID), safeCell(r.RequestID), safeCell(string(bytes.TrimSpace(r.Details))), r.PrevHash, r.Hash})
}

// Flush writes buffered data (and the header when no entry was written).
func (c *CSVWriter) Flush() error {
	if !c.started {
		c.started = true
		_ = c.w.Write(CSVHeader)
	}
	c.w.Flush()
	return c.w.Error()
}

func safeCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
