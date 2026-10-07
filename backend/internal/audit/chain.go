// Package audit implements the tamper-evident audit trail: a per-organization
// hash chain, integrity verification, metadata-only scrubbing and the SIEM
// export formats (JSONL and CSV).
//
// Each entry commits to the previous entry of the same organization:
//
//	hash = sha256( canonical({v, org_id, seq, prev_hash, ts, actor, action,
//	                          entity, entity_id, request_id, details}) )
//
// where canonical is the compact JSON of that struct (fields in this order,
// map keys sorted, timestamps as UTC RFC 3339 with microseconds). Changing,
// deleting, reordering or inserting any entry breaks the chain from that point
// on, and truncating the tail is caught against the chain head.
package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// HashVersion is part of the hashed payload so the format can evolve.
const HashVersion = 1

// tsLayout has fixed microsecond precision: PostgreSQL timestamptz keeps
// microseconds, so the value must survive a round trip unchanged.
const tsLayout = "2006-01-02T15:04:05.000000Z07:00"

// FormatTS renders a timestamp the way it is hashed and exported.
func FormatTS(t time.Time) string { return t.UTC().Format(tsLayout) }

type canonEntry struct {
	V         int             `json:"v"`
	OrgID     string          `json:"org_id"`
	Seq       int64           `json:"seq"`
	PrevHash  string          `json:"prev_hash"`
	TS        string          `json:"ts"`
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Entity    string          `json:"entity"`
	EntityID  string          `json:"entity_id"`
	RequestID string          `json:"request_id"`
	Details   json.RawMessage `json:"details"`
}

// NormalizeDetails returns details in canonical form: the value obtained after
// a JSON round trip (map keys sorted on output, numbers as float64). Storing
// and hashing the normalized value guarantees that what a database returns
// hashes to the same digest as what was written.
func NormalizeDetails(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"_unserializable": true}
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

func canonDetails(v any) json.RawMessage {
	b, err := json.Marshal(NormalizeDetails(v))
	if err != nil || len(b) == 0 {
		return json.RawMessage("null")
	}
	return b
}

// Canonical returns the exact bytes that are hashed for e.
func Canonical(e domain.AuditLog) []byte {
	b, _ := json.Marshal(canonEntry{V: HashVersion, OrgID: e.OrgID, Seq: e.Seq, PrevHash: e.PrevHash, TS: FormatTS(e.TS),
		Actor: e.Actor, Action: e.Action, Entity: e.Entity, EntityID: e.EntityID, RequestID: e.RequestID, Details: canonDetails(e.Details)})
	return b
}

// Hash computes the digest of e (Seq and PrevHash must be set).
func Hash(e domain.AuditLog) string {
	sum := sha256.Sum256(Canonical(e))
	return hex.EncodeToString(sum[:])
}

// Seal prepares e to be appended after head: it normalizes the timestamp and
// details, links it to head and computes its hash.
func Seal(orgID string, head domain.AuditHead, e domain.AuditLog) domain.AuditLog {
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	e.TS = e.TS.UTC().Truncate(time.Microsecond)
	// Timestamps strictly increase along the chain, so ordering by time and by
	// sequence always agree (a coarse clock or concurrent writers cannot swap
	// two entries in an export).
	if !head.TS.IsZero() && !e.TS.After(head.TS) {
		e.TS = head.TS.UTC().Truncate(time.Microsecond).Add(time.Microsecond)
	}
	e.OrgID = orgID
	e.Details = NormalizeDetails(e.Details)
	e.Seq = head.Seq + 1
	e.PrevHash = head.Hash
	e.Hash = Hash(e)
	return e
}

// Source gives Verify read access to one organization's chain.
type Source interface {
	// Chain returns up to limit chained entries with seq > after, ascending.
	Chain(ctx context.Context, after int64, limit int) ([]domain.AuditLog, error)
	// Head returns the recorded head of the chain (ok=false: nothing chained yet).
	Head(ctx context.Context) (domain.AuditHead, bool, error)
}

// Report is the outcome of an integrity verification.
type Report struct {
	OK          bool   `json:"ok"`
	Checked     int64  `json:"checked"`
	FirstSeq    int64  `json:"first_seq"`
	LastSeq     int64  `json:"last_seq"`
	HeadSeq     int64  `json:"head_seq"`
	HeadHash    string `json:"head_hash"`
	BrokenAtSeq int64  `json:"broken_at_seq,omitempty"`
	// Reason is one of: gap, prev_hash_mismatch, hash_mismatch, head_mismatch,
	// anchor_mismatch, org_mismatch.
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
	// AnchorChecked is true when an externally stored (seq, hash) pair was verified.
	AnchorChecked bool `json:"anchor_checked"`
}

const verifyPage = 1000

// Verify walks the whole chain and checks every link, then the recorded head.
// anchor (optional) is a (seq, hash) pair the operator stored outside the
// database (for example from a previous export or a WORM bucket): it detects a
// full rewrite of the chain, which a database-only check cannot.
func Verify(ctx context.Context, orgID string, src Source, anchor *domain.AuditHead) (Report, error) {
	var rep Report
	var prev domain.AuditHead // seq 0, hash ""
	after := int64(0)
	for {
		page, err := src.Chain(ctx, after, verifyPage)
		if err != nil {
			return rep, err
		}
		if len(page) == 0 {
			break
		}
		for _, e := range page {
			fail := func(reason, detail string) (Report, error) {
				rep.OK, rep.BrokenAtSeq, rep.Reason, rep.Detail = false, e.Seq, reason, detail
				return rep, nil
			}
			if e.OrgID != "" && e.OrgID != orgID {
				return fail("org_mismatch", "entry belongs to another organization")
			}
			if e.Seq != prev.Seq+1 {
				return fail("gap", fmt.Sprintf("expected seq %d, found %d: entries were removed or inserted", prev.Seq+1, e.Seq))
			}
			if e.PrevHash != prev.Hash {
				return fail("prev_hash_mismatch", "entry does not link to the previous entry")
			}
			chk := e
			chk.OrgID = orgID
			if Hash(chk) != e.Hash {
				return fail("hash_mismatch", "entry content was modified")
			}
			if rep.Checked == 0 {
				rep.FirstSeq = e.Seq
			}
			rep.Checked++
			prev = domain.AuditHead{Seq: e.Seq, Hash: e.Hash}
			rep.LastSeq = e.Seq
			if anchor != nil && e.Seq == anchor.Seq {
				rep.AnchorChecked = true
				if !strings.EqualFold(e.Hash, anchor.Hash) {
					return fail("anchor_mismatch", "entry hash differs from the externally stored anchor")
				}
			}
			after = e.Seq
		}
	}
	rep.HeadSeq, rep.HeadHash = prev.Seq, prev.Hash
	head, ok, err := src.Head(ctx)
	if err != nil {
		return rep, err
	}
	switch {
	case ok && (head.Seq != prev.Seq || head.Hash != prev.Hash):
		rep.Reason = "head_mismatch"
		rep.BrokenAtSeq = prev.Seq + 1
		rep.Detail = fmt.Sprintf("chain ends at seq %d but the recorded head is seq %d: the tail was removed or rewritten", prev.Seq, head.Seq)
		return rep, nil
	case !ok && prev.Seq > 0:
		rep.Reason, rep.BrokenAtSeq, rep.Detail = "head_mismatch", prev.Seq+1, "entries exist but no chain head is recorded"
		return rep, nil
	}
	if anchor != nil && !rep.AnchorChecked {
		rep.Reason, rep.BrokenAtSeq = "anchor_mismatch", anchor.Seq
		rep.Detail = fmt.Sprintf("anchor seq %d is beyond the end of the chain (%d): entries were removed", anchor.Seq, prev.Seq)
		return rep, nil
	}
	rep.OK = true
	return rep, nil
}

// Record is the exported (JSONL/API) form of an entry. It carries everything
// needed to recompute the hash offline.
type Record struct {
	Seq       int64           `json:"seq"`
	ID        string          `json:"id"`
	TS        string          `json:"ts"`
	OrgID     string          `json:"org_id"`
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Entity    string          `json:"entity"`
	EntityID  string          `json:"entity_id"`
	RequestID string          `json:"request_id"`
	Details   json.RawMessage `json:"details"`
	PrevHash  string          `json:"prev_hash"`
	Hash      string          `json:"hash"`
}

// ToRecord converts an entry to its exported form.
func ToRecord(e domain.AuditLog) Record {
	return Record{Seq: e.Seq, ID: e.ID, TS: FormatTS(e.TS), OrgID: e.OrgID, Actor: e.Actor, Action: e.Action, Entity: e.Entity,
		EntityID: e.EntityID, RequestID: e.RequestID, Details: canonDetails(e.Details), PrevHash: e.PrevHash, Hash: e.Hash}
}

// FromRecord is the inverse of ToRecord.
func FromRecord(r Record) (domain.AuditLog, error) {
	ts, err := time.Parse(tsLayout, r.TS)
	if err != nil {
		return domain.AuditLog{}, fmt.Errorf("record seq %d: bad ts %q", r.Seq, r.TS)
	}
	var details any
	if len(r.Details) > 0 {
		if err := json.Unmarshal(r.Details, &details); err != nil {
			return domain.AuditLog{}, fmt.Errorf("record seq %d: bad details", r.Seq)
		}
	}
	return domain.AuditLog{ID: r.ID, TS: ts, OrgID: r.OrgID, Actor: r.Actor, Action: r.Action, Entity: r.Entity, EntityID: r.EntityID,
		RequestID: r.RequestID, Details: details, Seq: r.Seq, PrevHash: r.PrevHash, Hash: r.Hash}, nil
}

// VerifyRecords checks exported records offline. Each hash is recomputed; when
// two consecutive records are adjacent in the chain (seq+1) they must link. A
// gap is only an error in strict mode (a filtered export has gaps by design;
// an unfiltered export must have none).
func VerifyRecords(recs []Record, strict bool) Report {
	var rep Report
	var prev *domain.AuditLog
	for _, r := range recs {
		e, err := FromRecord(r)
		fail := func(reason, detail string) Report {
			rep.OK, rep.BrokenAtSeq, rep.Reason, rep.Detail = false, r.Seq, reason, detail
			return rep
		}
		if err != nil {
			return fail("hash_mismatch", err.Error())
		}
		if Hash(e) != e.Hash {
			return fail("hash_mismatch", "entry content was modified")
		}
		if prev != nil {
			switch {
			case e.Seq <= prev.Seq:
				return fail("gap", "sequence is not increasing")
			case e.Seq == prev.Seq+1 && e.PrevHash != prev.Hash:
				return fail("prev_hash_mismatch", "entry does not link to the previous entry")
			case e.Seq != prev.Seq+1 && strict:
				return fail("gap", fmt.Sprintf("expected seq %d, found %d", prev.Seq+1, e.Seq))
			}
		} else if strict && e.Seq == 1 && e.PrevHash != "" {
			return fail("prev_hash_mismatch", "first entry must have an empty prev_hash")
		}
		if rep.Checked == 0 {
			rep.FirstSeq = e.Seq
		}
		rep.Checked++
		rep.LastSeq, rep.HeadSeq, rep.HeadHash = e.Seq, e.Seq, e.Hash
		cp := e
		prev = &cp
	}
	rep.OK = true
	return rep
}

// ReadJSONL parses an exported JSONL stream.
func ReadJSONL(b []byte) ([]Record, error) {
	var out []Record
	for i, line := range bytes.Split(b, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		out = append(out, r)
	}
	return out, nil
}
