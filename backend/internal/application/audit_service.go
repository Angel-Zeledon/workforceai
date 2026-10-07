package application

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"aiworkforce/backend/internal/audit"
	"aiworkforce/backend/internal/domain"
)

// Export formats of the audit trail.
const (
	ExportJSONL = "jsonl"
	ExportCSV   = "csv"
)

// maxExportEntries bounds one export; a larger range must be split by date.
const maxExportEntries = 500_000

// AuditService serves the audit trail: filtered queries, SIEM exports and
// integrity verification. Reading and exporting are themselves audited.
type AuditService struct {
	Store AuditStore
	Rec   *Recorder
	Cfg   Config
}

func (s *AuditService) org(ctx context.Context) string { return OrgFrom(ctx, s.Cfg.OrgID) }

// Query returns one page of the trail of the caller's organization.
func (s *AuditService) Query(ctx context.Context, q domain.AuditQuery) (domain.AuditPage, error) {
	return s.Store.QueryAudit(ctx, s.org(ctx), q)
}

// ExportResult says what an export wrote.
type ExportResult struct {
	Entries  int
	Format   string
	HeadSeq  int64
	HeadHash string
	Capped   bool
}

// Export streams the entries matching q (oldest first, every field a SIEM
// needs plus the hash chain) to w. The export is recorded before it starts, so
// even an aborted download leaves a trace.
func (s *AuditService) Export(ctx context.Context, q domain.AuditQuery, format string, w io.Writer) (ExportResult, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = ExportJSONL
	}
	if format != ExportJSONL && format != ExportCSV {
		return ExportResult{}, fmt.Errorf("%w: format must be jsonl or csv", domain.ErrInvalid)
	}
	org := s.org(ctx)
	q.Limit, q.Cursor, q.Desc = domain.MaxAuditPage, "", false
	// The export is a snapshot of the chain as it is now: the entry recording the
	// export itself (written next) must not enter the export it describes.
	head, _, err := s.Store.AuditHead(ctx, org)
	if err != nil {
		return ExportResult{}, err
	}
	q.UpToSeq = &head.Seq
	s.Rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "user"), Action: "audit.exported", Entity: "audit", EntityID: org,
		Details: map[string]any{"format": format, "actor_filter": q.Actor, "action_filter": q.Action, "request_filter": q.RequestID,
			"entity_filter": q.Entity, "from": fmtTime(q.From), "to": fmtTime(q.To)}})
	res := ExportResult{Format: format}
	var csvw *audit.CSVWriter
	if format == ExportCSV {
		csvw = audit.NewCSVWriter(w)
	}
	for {
		page, err := s.Store.QueryAudit(ctx, org, q)
		if err != nil {
			return res, err
		}
		for _, e := range page.Items {
			var werr error
			if csvw != nil {
				werr = csvw.Write(e)
			} else {
				werr = audit.WriteJSONLRecord(w, e)
			}
			if werr != nil {
				return res, werr
			}
			res.Entries++
			if e.Seq > res.HeadSeq {
				res.HeadSeq, res.HeadHash = e.Seq, e.Hash
			}
			if res.Entries >= maxExportEntries {
				res.Capped = true
				break
			}
		}
		if res.Capped || page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	if csvw != nil {
		return res, csvw.Flush()
	}
	return res, nil
}

// Verify checks the integrity of the organization's chain. A broken chain is
// reported (never silently repaired) and audited as audit.integrity_failed.
func (s *AuditService) Verify(ctx context.Context, anchor *domain.AuditHead) (audit.Report, error) {
	org := s.org(ctx)
	rep, err := audit.Verify(ctx, org, chainSource{s.Store, org}, anchor)
	if err != nil {
		return rep, err
	}
	action := "audit.verified"
	if !rep.OK {
		action = "audit.integrity_failed"
	}
	s.Rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "system"), Action: action, Entity: "audit", EntityID: org,
		Details: map[string]any{"ok": rep.OK, "checked": rep.Checked, "head_seq": rep.HeadSeq, "reason": rep.Reason, "broken_at_seq": rep.BrokenAtSeq,
			"anchor_checked": rep.AnchorChecked}})
	return rep, nil
}

type chainSource struct {
	st  AuditStore
	org string
}

func (c chainSource) Chain(ctx context.Context, after int64, limit int) ([]domain.AuditLog, error) {
	return c.st.AuditChain(ctx, c.org, after, limit)
}

func (c chainSource) Head(ctx context.Context) (domain.AuditHead, bool, error) {
	return c.st.AuditHead(ctx, c.org)
}

// VerifyChain verifies one organization's chain straight from a store (used by
// the "ctl audit-verify" command, without an API or a Recorder).
func VerifyChain(ctx context.Context, st AuditStore, orgID string, anchor *domain.AuditHead) (audit.Report, error) {
	return audit.Verify(ctx, orgID, chainSource{st, orgID}, anchor)
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return audit.FormatTS(*t)
}
