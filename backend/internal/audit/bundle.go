package audit

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Verification is the tamper-chain result that travels with an export: the check of the organization's whole
// chain (as stored) and the offline check of the exported records themselves.
type Verification struct {
	// Chain is audit.Verify over the full stored chain at export time.
	Chain Report `json:"chain"`
	// Export is VerifyRecords over the exported entries: each hash recomputed, adjacent entries linked. For a
	// filtered export gaps are expected, so only recomputation and adjacent links are checked.
	Export     Report `json:"export"`
	VerifiedAt string `json:"verified_at"`
}

// Document is the JSON form of an export: filters, verification result and the records.
type Document struct {
	GeneratedAt  string            `json:"generated_at"`
	OrgID        string            `json:"org_id"`
	Filters      map[string]string `json:"filters"`
	Entries      int               `json:"entries"`
	Capped       bool              `json:"capped"`
	HashVersion  int               `json:"hash_version"`
	Verification Verification      `json:"verification"`
	Records      []Record          `json:"records,omitempty"`
}

// BuildDocument assembles the document and verifies recs offline (filtered exports are not strict).
func BuildDocument(org string, filters map[string]string, recs []Record, chain Report, capped bool, now time.Time) Document {
	return Document{
		GeneratedAt: FormatTS(now), OrgID: org, Filters: filters, Entries: len(recs), Capped: capped, HashVersion: HashVersion,
		Verification: Verification{Chain: chain, Export: VerifyRecords(recs, false), VerifiedAt: FormatTS(now)},
		Records:      recs,
	}
}

const bundleReadme = `Audit export
============
audit.csv          the entries (seq, ts, actor, action, entity, details, prev_hash, hash).
verification.json  result of verifying the tamper-evident hash chain when the export was made:
                   "chain" is the whole stored chain, "export" the exported entries recomputed offline.

Each entry commits to the previous one: hash = sha256(canonical JSON of the entry fields + prev_hash).
Any modified, removed or reordered entry breaks the chain from that point on. Keep head_seq/head_hash from
this file outside the system to detect a complete rewrite later (GET /audit/verify?anchor_seq=&anchor_hash=).
`

// WriteZip writes a self-contained archive: the CSV export, the verification (document without records) and a
// short README.
func WriteZip(w io.Writer, csvData []byte, doc Document) error {
	zw := zip.NewWriter(w)
	doc.Records = nil
	vb, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{"audit.csv", csvData}, {"verification.json", vb}, {"README.txt", []byte(bundleReadme)}} {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: time.Now().UTC()})
		if err != nil {
			return fmt.Errorf("zip %s: %w", f.name, err)
		}
		if _, err := fw.Write(f.data); err != nil {
			return err
		}
	}
	return zw.Close()
}
