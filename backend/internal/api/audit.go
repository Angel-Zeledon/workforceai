package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/audit"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// Audit and approval-rules API (additive, v1). See docs/architecture/08-api.md
// section 14. The audit trail is read-only over HTTP: there is no endpoint that
// writes, changes or deletes an entry.

func (s *server) mountAudit(r chi.Router) {
	if s.Audit != nil {
		read := s.can(auth.PermAuditRead)
		r.With(read).Get("/audit", s.queryAudit)
		r.With(read).Get("/audit/export", s.exportAudit)
		r.With(read).Get("/audit/verify", s.verifyAudit)
	}
	if s.OrgConfig != nil {
		r.With(s.can(auth.PermApprovalsRead)).Get("/policy/rules", s.getPolicyRules)
		r.With(s.can(auth.PermPolicyManage)).Put("/policy/rules", s.putPolicyRules)
	}
}

// parseTime accepts RFC 3339 or a plain date (UTC midnight).
func parseTime(v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			t = t.UTC()
			return &t, nil
		}
	}
	return nil, fmt.Errorf("%w: %q is not an RFC 3339 timestamp or a YYYY-MM-DD date", domain.ErrInvalid, v)
}

func auditQuery(r *http.Request) (domain.AuditQuery, error) {
	v := r.URL.Query()
	q := domain.AuditQuery{Actor: v.Get("actor"), Action: v.Get("type"), Entity: v.Get("entity"), RequestID: v.Get("request_id"), Cursor: v.Get("cursor")}
	if q.Action == "" {
		q.Action = v.Get("action") // alias
	}
	var err error
	if q.From, err = parseTime(v.Get("from")); err != nil {
		return q, err
	}
	if q.To, err = parseTime(v.Get("to")); err != nil {
		return q, err
	}
	if q.From != nil && q.To != nil && !q.From.Before(*q.To) {
		return q, fmt.Errorf("%w: from must be before to", domain.ErrInvalid)
	}
	if l := v.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 {
			return q, fmt.Errorf("%w: limit must be a positive integer", domain.ErrInvalid)
		}
		q.Limit = n
	}
	switch strings.ToLower(v.Get("order")) {
	case "", "asc":
	case "desc":
		q.Desc = true
	default:
		return q, fmt.Errorf("%w: order must be asc or desc", domain.ErrInvalid)
	}
	return q, nil
}

func (s *server) queryAudit(w http.ResponseWriter, r *http.Request) {
	q, err := auditQuery(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	// The default order of the API is newest first (what a screen shows); the
	// export is always oldest first (what a SIEM and the hash chain expect).
	if r.URL.Query().Get("order") == "" {
		q.Desc = true
	}
	page, err := s.Audit.Query(r.Context(), q)
	if err != nil {
		s.fail(w, err)
		return
	}
	items := make([]audit.Record, 0, len(page.Items))
	for _, e := range page.Items {
		items = append(items, audit.ToRecord(e))
	}
	out := map[string]any{"items": items}
	if page.NextCursor != "" {
		out["next_cursor"] = page.NextCursor
	}
	writeJSON(w, 200, out)
}

func (s *server) exportAudit(w http.ResponseWriter, r *http.Request) {
	q, err := auditQuery(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = application.ExportJSONL
	}
	var ctype, ext string
	switch format {
	case application.ExportJSONL:
		ctype, ext = "application/x-ndjson", "jsonl"
	case application.ExportCSV:
		ctype, ext = "text/csv; charset=utf-8", "csv"
	default:
		s.fail(w, fmt.Errorf("%w: format must be jsonl or csv", domain.ErrInvalid))
		return
	}
	// A large export outlives the default write timeout of normal REST handlers.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Minute))
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="audit-%s.%s"`, time.Now().UTC().Format("20060102T150405Z"), ext))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	if _, err := s.Audit.Export(r.Context(), q, format, w); err != nil {
		// Headers are already sent when streaming started; log and cut the stream.
		s.Log.Error("audit export failed", "err", err)
	}
}

func (s *server) verifyAudit(w http.ResponseWriter, r *http.Request) {
	var anchor *domain.AuditHead
	sq, sh := r.URL.Query().Get("anchor_seq"), r.URL.Query().Get("anchor_hash")
	if sq != "" || sh != "" {
		n, err := strconv.ParseInt(sq, 10, 64)
		if err != nil || n < 1 || sh == "" {
			s.fail(w, fmt.Errorf("%w: anchor_seq and anchor_hash must be given together", domain.ErrInvalid))
			return
		}
		anchor = &domain.AuditHead{Seq: n, Hash: strings.ToLower(sh)}
	}
	rep, err := s.Audit.Verify(r.Context(), anchor)
	if err != nil {
		s.fail(w, err)
		return
	}
	// 200 with ok=false: the verification ran; the trail is what does not verify.
	writeJSON(w, 200, rep)
}

func (s *server) getPolicyRules(w http.ResponseWriter, r *http.Request) {
	v, err := s.OrgConfig.Rules(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	v.AlwaysApprove = list(v.AlwaysApprove)
	writeJSON(w, 200, v)
}

func (s *server) putPolicyRules(w http.ResponseWriter, r *http.Request) {
	var body application.RulesUpdate
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.OrgConfig.UpdateRules(r.Context(), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	v.AlwaysApprove = list(v.AlwaysApprove)
	writeJSON(w, 200, v)
}
