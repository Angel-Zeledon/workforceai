package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/audit"
	"aiworkforce/backend/internal/domain"
)

// AuditSource is the audit service (application.AuditService implements it).
type AuditSource interface {
	Export(ctx context.Context, q domain.AuditQuery, format string, w io.Writer) (application.ExportResult, error)
	Verify(ctx context.Context, anchor *domain.AuditHead) (audit.Report, error)
}

// Audit export formats.
const (
	AuditCSV  = "csv"  // streamed; the verification travels in X-Audit-* response headers
	AuditJSON = "json" // one document: filters, verification and records
	AuditZip  = "zip"  // audit.csv + verification.json + README.txt
)

// maxBuffered bounds the exports that are assembled in memory (json, zip). Larger ranges use csv (streamed).
const maxBuffered = 48 << 20

// ErrTooLarge means the range is too big for the buffered formats.
var ErrTooLarge = errors.New("governance: export too large for this format: narrow the dates or use csv")

type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.Len()+len(p) > l.max {
		return 0, ErrTooLarge
	}
	return l.Buffer.Write(p)
}

// AuditExport is what the HTTP layer needs to answer: the content type, the file name and the headers.
type AuditExport struct {
	ContentType string
	Filename    string
	Headers     map[string]string
}

// ExportAudit writes the audit export in the requested format. Headers are computed before the first byte is
// written; begin is called once with them so the caller can send the response head.
func ExportAudit(ctx context.Context, src AuditSource, org string, q domain.AuditQuery, format string, now time.Time, w io.Writer, begin func(AuditExport)) error {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = AuditCSV
	}
	stamp := now.UTC().Format("20060102T150405Z")
	filters := map[string]string{"actor": q.Actor, "type": q.Action, "entity": q.Entity, "request_id": q.RequestID}
	if q.From != nil {
		filters["from"] = audit.FormatTS(*q.From)
	}
	if q.To != nil {
		filters["to"] = audit.FormatTS(*q.To)
	}
	head := func(rep audit.Report) map[string]string {
		return map[string]string{
			"X-Audit-Chain-OK": strconv.FormatBool(rep.OK), "X-Audit-Chain-Checked": strconv.FormatInt(rep.Checked, 10),
			"X-Audit-Head-Seq": strconv.FormatInt(rep.HeadSeq, 10), "X-Audit-Head-Hash": rep.HeadHash, "X-Audit-Chain-Reason": rep.Reason,
		}
	}
	switch format {
	case AuditCSV:
		rep, err := src.Verify(ctx, nil)
		if err != nil {
			return err
		}
		begin(AuditExport{ContentType: "text/csv; charset=utf-8", Filename: "audit-" + stamp + ".csv", Headers: head(rep)})
		_, err = src.Export(ctx, q, application.ExportCSV, w)
		return err
	case AuditJSON, AuditZip:
		lb := &limitedBuffer{max: maxBuffered}
		res, err := src.Export(ctx, q, application.ExportJSONL, lb)
		if err != nil {
			return err
		}
		rep, err := src.Verify(ctx, nil)
		if err != nil {
			return err
		}
		recs, err := audit.ReadJSONL(lb.Bytes())
		if err != nil {
			return fmt.Errorf("audit export: %w", err)
		}
		doc := audit.BuildDocument(org, filters, recs, rep, res.Capped, now)
		var out bytes.Buffer
		ctype, name := "application/json", "audit-"+stamp+".json"
		if format == AuditJSON {
			enc := json.NewEncoder(&out)
			enc.SetIndent("", " ")
			if err := enc.Encode(doc); err != nil {
				return err
			}
		} else {
			ctype, name = "application/zip", "audit-"+stamp+".zip"
			var csvBuf bytes.Buffer
			cw := audit.NewCSVWriter(&csvBuf)
			for _, r := range recs {
				e, err := audit.FromRecord(r)
				if err != nil {
					return err
				}
				if err := cw.Write(e); err != nil {
					return err
				}
			}
			if err := cw.Flush(); err != nil {
				return err
			}
			if err := audit.WriteZip(&out, csvBuf.Bytes(), doc); err != nil {
				return err
			}
		}
		h := head(rep)
		h["X-Audit-Export-OK"] = strconv.FormatBool(doc.Verification.Export.OK)
		begin(AuditExport{ContentType: ctype, Filename: name, Headers: h})
		_, err = w.Write(out.Bytes())
		return err
	}
	return fmt.Errorf("%w: format must be csv, json or zip", ErrInvalid)
}
