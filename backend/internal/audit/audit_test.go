package audit_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/audit"
	"aiworkforce/backend/internal/domain"
)

// chain is an in-memory audit.Source whose rows a test can tamper with.
type chain struct {
	mu      sync.Mutex
	org     string
	rows    []domain.AuditLog
	head    domain.AuditHead
	hasHead bool
}

func newChain(org string) *chain { return &chain{org: org} }

func (c *chain) add(action string, details any) domain.AuditLog {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := audit.Seal(c.org, c.head, domain.AuditLog{ID: fmt.Sprintf("id-%d", len(c.rows)+1), Actor: "u", Action: action, Entity: "task",
		EntityID: "t1", RequestID: "r1", Details: details, TS: time.Date(2026, 10, 6, 12, 0, len(c.rows), 123456789, time.UTC)})
	c.rows = append(c.rows, e)
	c.head, c.hasHead = domain.AuditHead{Seq: e.Seq, Hash: e.Hash}, true
	return e
}

func (c *chain) Chain(_ context.Context, after int64, limit int) ([]domain.AuditLog, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []domain.AuditLog
	for _, r := range c.rows {
		if r.Seq > after {
			out = append(out, r)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (c *chain) Head(context.Context) (domain.AuditHead, bool, error) { return c.head, c.hasHead, nil }

func filled(n int) *chain {
	c := newChain("org-1")
	for i := 0; i < n; i++ {
		c.add(fmt.Sprintf("action.%d", i), map[string]any{"i": i, "nested": map[string]any{"b": 2, "a": 1}, "tags": []string{"x", "y"}})
	}
	return c
}

func verify(t *testing.T, c *chain, anchor *domain.AuditHead) audit.Report {
	t.Helper()
	rep, err := audit.Verify(context.Background(), c.org, c, anchor)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestChainLinksEveryEntryToThePreviousOne(t *testing.T) {
	c := filled(5)
	for i, r := range c.rows {
		if r.Seq != int64(i+1) {
			t.Fatalf("seq = %d, want %d", r.Seq, i+1)
		}
		if i == 0 && r.PrevHash != "" {
			t.Fatal("first entry must have an empty prev_hash")
		}
		if i > 0 && r.PrevHash != c.rows[i-1].Hash {
			t.Fatalf("entry %d does not link to the previous one", r.Seq)
		}
		if len(r.Hash) != 64 {
			t.Fatalf("hash %q is not a hex sha256", r.Hash)
		}
	}
	rep := verify(t, c, nil)
	if !rep.OK || rep.Checked != 5 || rep.HeadSeq != 5 || rep.HeadHash != c.rows[4].Hash {
		t.Fatalf("report = %+v", rep)
	}
}

func TestEmptyChainVerifies(t *testing.T) {
	if rep := verify(t, newChain("org-1"), nil); !rep.OK || rep.Checked != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestHashIsStableAcrossAJSONRoundTrip(t *testing.T) {
	// What a database returns (JSONB: reordered keys, numbers as text) must hash to the same digest.
	c := newChain("org-1")
	e := c.add("x", map[string]any{"z": 1, "a": []any{1.5, "s", nil, true}, "big": 12345678901234.0, "m": map[string]any{"y": 1, "b": 2}})
	raw, _ := json.Marshal(e.Details)
	var back any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	e2 := e
	e2.Details = back
	if audit.Hash(e2) != e.Hash {
		t.Fatal("hash changed after a JSON round trip")
	}
	// Timestamps keep microseconds only (PostgreSQL precision).
	e3 := e
	e3.TS = e.TS.Add(400 * time.Nanosecond)
	if audit.Hash(e3) != e.Hash {
		t.Fatal("sub-microsecond time must not matter")
	}
}

func TestTamperingIsDetected(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(c *chain)
		reason string
		at     int64
	}{
		{"modified details", func(c *chain) { c.rows[2].Details = map[string]any{"i": 99} }, "hash_mismatch", 3},
		{"modified actor", func(c *chain) { c.rows[1].Actor = "someone-else" }, "hash_mismatch", 2},
		{"modified timestamp", func(c *chain) { c.rows[3].TS = c.rows[3].TS.Add(time.Hour) }, "hash_mismatch", 4},
		{"modified action", func(c *chain) { c.rows[0].Action = "approval.rejected" }, "hash_mismatch", 1},
		{"entry removed from the middle", func(c *chain) { c.rows = append(c.rows[:2], c.rows[3:]...) }, "gap", 4},
		{"first entry removed", func(c *chain) { c.rows = c.rows[1:] }, "gap", 2},
		{"entries swapped", func(c *chain) { c.rows[1], c.rows[2] = c.rows[2], c.rows[1] }, "gap", 3},
		{"tail removed", func(c *chain) { c.rows = c.rows[:3] }, "head_mismatch", 4},
		{"entry replaced and re-hashed", func(c *chain) {
			r := c.rows[2]
			r.Details = map[string]any{"i": 99}
			r.Hash = audit.Hash(r) // a forger recomputes this entry's hash...
			c.rows[2] = r          // ...but the next entry still points at the old one
		}, "prev_hash_mismatch", 4},
		{"entry from another organization", func(c *chain) { c.rows[1].OrgID = "org-2" }, "org_mismatch", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := filled(5)
			tc.mutate(c)
			rep := verify(t, c, nil)
			if rep.OK {
				t.Fatal("tampering went undetected")
			}
			if rep.Reason != tc.reason || rep.BrokenAtSeq != tc.at {
				t.Fatalf("reason=%q at=%d, want %q at %d (%s)", rep.Reason, rep.BrokenAtSeq, tc.reason, tc.at, rep.Detail)
			}
		})
	}
}

func TestFullChainRewriteNeedsAnExternalAnchor(t *testing.T) {
	c := filled(4)
	anchor := &domain.AuditHead{Seq: 2, Hash: c.rows[1].Hash}
	if rep := verify(t, c, anchor); !rep.OK || !rep.AnchorChecked {
		t.Fatalf("honest chain with anchor: %+v", rep)
	}
	// An attacker with full control of the table rewrites every entry consistently, head included.
	forged := newChain("org-1")
	for i := 0; i < 4; i++ {
		forged.add(fmt.Sprintf("action.%d", i), map[string]any{"i": i, "forged": true})
	}
	if rep := verify(t, forged, nil); !rep.OK {
		t.Fatalf("a consistent rewrite verifies against the database alone (documented limit): %+v", rep)
	}
	rep := verify(t, forged, anchor)
	if rep.OK || rep.Reason != "anchor_mismatch" {
		t.Fatalf("the anchor must expose the rewrite: %+v", rep)
	}
	// An anchor beyond the end of the chain means entries were removed.
	if rep := verify(t, filled(2), &domain.AuditHead{Seq: 4, Hash: c.rows[3].Hash}); rep.OK || rep.Reason != "anchor_mismatch" {
		t.Fatalf("anchor past the end: %+v", rep)
	}
}

func TestVerifyIsPagedAndDoesNotMissEntriesBeyondTheFirstPage(t *testing.T) {
	c := filled(2300) // more than two pages of 1000
	if rep := verify(t, c, nil); !rep.OK || rep.Checked != 2300 {
		t.Fatalf("report = %+v", rep)
	}
	c.rows[2200].Details = map[string]any{"changed": true}
	if rep := verify(t, c, nil); rep.OK || rep.BrokenAtSeq != 2201 {
		t.Fatalf("tampering on the third page: %+v", rep)
	}
}

func jsonl(t *testing.T, c *chain) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, r := range c.rows {
		if err := audit.WriteJSONLRecord(&buf, r); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

func TestExportedJSONLVerifiesOfflineAndDetectsEdits(t *testing.T) {
	c := filled(6)
	raw := jsonl(t, c)
	recs, err := audit.ReadJSONL(raw)
	if err != nil || len(recs) != 6 {
		t.Fatalf("read: %d, %v", len(recs), err)
	}
	if rep := audit.VerifyRecords(recs, true); !rep.OK || rep.Checked != 6 {
		t.Fatalf("honest export: %+v", rep)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"seq", "id", "ts", "org_id", "actor", "action", "entity", "entity_id", "request_id", "details", "prev_hash", "hash"} {
			if _, ok := m[k]; !ok {
				t.Fatalf("export line misses %q: %s", k, line)
			}
		}
	}
	edited := strings.Replace(string(raw), `"actor":"u"`, `"actor":"evil"`, 1)
	recs, _ = audit.ReadJSONL([]byte(edited))
	if rep := audit.VerifyRecords(recs, false); rep.OK || rep.Reason != "hash_mismatch" {
		t.Fatalf("edited export: %+v", rep)
	}
	// A deleted line: only strict mode (unfiltered exports) calls a gap an error.
	recs, _ = audit.ReadJSONL(jsonl(t, c))
	recs = append(recs[:2], recs[3:]...)
	if rep := audit.VerifyRecords(recs, false); !rep.OK {
		t.Fatalf("a filtered export has gaps by design: %+v", rep)
	}
	if rep := audit.VerifyRecords(recs, true); rep.OK || rep.Reason != "gap" {
		t.Fatalf("strict mode must catch the gap: %+v", rep)
	}
	// Adjacent entries that do not link are always an error.
	recs, _ = audit.ReadJSONL(jsonl(t, c))
	recs[3].PrevHash = strings.Repeat("0", 64)
	if rep := audit.VerifyRecords(recs, false); rep.OK {
		t.Fatal("broken link in the export went undetected")
	}
}

func TestCSVExportIsSIEMReadyAndSafeForSpreadsheets(t *testing.T) {
	c := newChain("org-1")
	c.add("tool.executed", map[string]any{"tool": "email", "count": 2})
	c.rows[0].Actor = "=cmd|'/c calc'!A1"
	c.rows[0].EntityID = "@SUM(A1)"
	var buf bytes.Buffer
	w := audit.NewCSVWriter(&buf)
	for _, r := range c.rows {
		if err := w.Write(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	if strings.Join(rows[0], ",") != strings.Join(audit.CSVHeader, ",") {
		t.Fatalf("header = %v", rows[0])
	}
	if !strings.HasPrefix(rows[1][4], "'=") || !strings.HasPrefix(rows[1][7], "'@") {
		t.Fatalf("formula cells must be neutralized: %q %q", rows[1][4], rows[1][7])
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(rows[1][9]), &d); err != nil || d["tool"] != "email" {
		t.Fatalf("details column = %q (%v)", rows[1][9], err)
	}
	var empty bytes.Buffer
	ew := audit.NewCSVWriter(&empty)
	_ = ew.Flush()
	if !strings.HasPrefix(empty.String(), "seq,id,ts") {
		t.Fatalf("an empty export still has a header: %q", empty.String())
	}
}

func TestScrubKeepsMetadataAndDropsContentAndSecrets(t *testing.T) {
	in := map[string]any{
		"tool": "email", "action": "send", "status": "succeeded", "input_tokens": 120,
		"args":     map[string]any{"to": "ceo@acme.com", "body": "confidential"},
		"body":     "the quarterly numbers are...",
		"password": "hunter2hunter2", "api_key": "k-1234567890", "access_token": "ya29.abcdefghijklmnopqrstuvwxyz",
		"authorization": "Bearer abcdefghijklmnopqrstuvwxyz0123",
		"note":          "ok, verified with token=sk-live-abcdefghijklmnopqrstu",
		"nested":        map[string]any{"client_secret": "zzz", "count": 3, "text": "hello"},
		"list":          []any{map[string]any{"subject": "Re: salary", "id": "a1"}},
		"long":          strings.Repeat("x", 1000),
	}
	out := audit.Scrub(in).(map[string]any)
	b, _ := json.Marshal(out)
	s := string(b)
	for _, leak := range []string{"confidential", "quarterly", "hunter2", "k-1234567890", "ya29.abc", "Bearer abc", "sk-live-abc", "ceo@acme.com", "salary", `"zzz"`, "hello"} {
		if strings.Contains(s, leak) {
			t.Errorf("%q leaked into the trail: %s", leak, s)
		}
	}
	if out["tool"] != "email" || out["status"] != "succeeded" || out["input_tokens"] != float64(120) {
		t.Errorf("metadata must survive: %v", out)
	}
	if out["nested"].(map[string]any)["count"] != float64(3) {
		t.Errorf("nested metadata lost: %v", out["nested"])
	}
	if l := len([]rune(out["long"].(string))); l > 305 {
		t.Errorf("long strings are truncated, got %d runes", l)
	}
	if audit.Scrub(nil) != nil {
		t.Error("nil details stay nil")
	}
}

func TestQueryHelpers(t *testing.T) {
	q := audit.NormalizeQuery(domain.AuditQuery{Limit: 100000})
	if q.Limit != domain.MaxAuditPage {
		t.Fatalf("limit = %d", q.Limit)
	}
	if audit.NormalizeQuery(domain.AuditQuery{}).Limit != domain.DefaultAuditPage {
		t.Fatal("default page size")
	}
	for pat, want := range map[string]map[string]bool{
		"approval.*":      {"approval.approved": true, "approval.partial": true, "tool.executed": false},
		"tool.executed":   {"tool.executed": true, "tool.executed2": false},
		"":                {"anything": true},
		"policy.decision": {"policy.decision": true, "policy.rules_updated": false},
	} {
		for action, ok := range want {
			if audit.ActionMatches(pat, action) != ok {
				t.Errorf("ActionMatches(%q, %q) != %v", pat, action, ok)
			}
		}
	}
	e := domain.AuditLog{ID: "abc", TS: time.Date(2026, 1, 2, 3, 4, 5, 678901234, time.UTC)}
	cur, err := audit.DecodeCursor(audit.CursorOf(e).Encode())
	if err != nil || cur.ID != "abc" || !cur.TS.Equal(e.TS.Truncate(time.Microsecond)) {
		t.Fatalf("cursor round trip: %+v %v", cur, err)
	}
	if _, err := audit.DecodeCursor("!!!"); err == nil {
		t.Fatal("garbage cursor must be rejected")
	}
	later := domain.AuditLog{ID: "abd", TS: e.TS}
	if !cur.After(later, false) || cur.After(later, true) {
		t.Fatal("cursor ordering on equal timestamps uses the id")
	}
}
