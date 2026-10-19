package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/webhooks"
)

// Env adapts the host router: tenancy, permissions, JSON decoding and the audit trail. The package never
// reads the organization from a request body or a query string.
type Env struct {
	Org    func(r *http.Request) string
	Actor  func(r *http.Request) string
	Decode func(w http.ResponseWriter, r *http.Request, dst any) error
	Fail   func(w http.ResponseWriter, err error)
	// Audit writes one audit entry (metadata only) for org.
	Audit func(ctx context.Context, org, actor, action, entity, entityID string, details map[string]any)
	// Guards. Admin: owner or admin. Owner: owner only. Metrics: may read cost. AuditRead: may read the audit trail.
	Admin, Owner, Metrics, AuditRead func(http.Handler) http.Handler
	// WithOrg binds an organization and an actor to a context (public API requests).
	WithOrg func(ctx context.Context, org, actor string) context.Context
}

// Handler serves the administration endpoints (Mount) and the public API (PublicRouter).
type Handler struct {
	Env       Env
	Tokens    *TokenService
	Admin     AdminStore
	Reports   ReportStore
	Retention RetentionStore
	Purger    *Purger
	Webhooks  *webhooks.Service // optional
	Audit     AuditSource       // optional
	// Policies returns the read-only policy overview shown in the console (approval rules, model policy...).
	Policies func(ctx context.Context, org string) (map[string]any, error)
	Now      func() time.Time
	// OnLimitChange (optional) is called after a department limit changed (wakes the calls paused on it).
	OnLimitChange func()
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) fail(w http.ResponseWriter, err error) {
	status := 0
	switch {
	case errors.Is(err, ErrInvalid), errors.Is(err, webhooks.ErrInvalid), errors.Is(err, domain.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, ErrNotFound), errors.Is(err, webhooks.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrTokenLimit), errors.Is(err, webhooks.ErrLimit):
		status = http.StatusConflict
	case errors.Is(err, webhooks.ErrNoEncryption):
		status = http.StatusServiceUnavailable
	case errors.Is(err, ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
	}
	if status == 0 {
		h.Env.Fail(w, err)
		return
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (h *Handler) audit(r *http.Request, action, entity, id string, details map[string]any) {
	if h.Env.Audit != nil {
		h.Env.Audit(r.Context(), h.Env.Org(r), h.Env.Actor(r), action, entity, id, details)
	}
}

// Mount registers the console endpoints under /governance.
func (h *Handler) Mount(r chi.Router) {
	e := h.Env
	r.Route("/governance", func(r chi.Router) {
		r.With(e.Admin).Get("/overview", h.overview)
		r.With(e.Admin).Get("/policies", h.policies)
		r.With(e.Admin).Put("/agents/{id}/department", h.putDepartment)
		r.With(e.Admin).Put("/departments/limit", h.putDepartmentLimit)
		r.With(e.Metrics).Get("/reports/usage", h.usageReport)
		if h.Audit != nil {
			r.With(e.AuditRead).Get("/audit/export", h.auditExport)
		}
		r.With(e.Admin).Get("/retention", h.getRetention)
		r.With(e.Owner).Put("/retention", h.putRetention)
		r.With(e.Admin).Post("/retention/preview", h.previewRetention)
		r.With(e.Owner).Post("/retention/run", h.runRetention)
		r.With(e.Admin).Get("/tokens", h.listTokens)
		r.With(e.Admin).Post("/tokens", h.createToken)
		r.With(e.Admin).Delete("/tokens/{id}", h.revokeToken)
		if h.Webhooks != nil {
			r.With(e.Admin).Get("/webhooks", h.listWebhooks)
			r.With(e.Admin).Post("/webhooks", h.createWebhook)
			r.With(e.Admin).Delete("/webhooks/{id}", h.deleteWebhook)
			r.With(e.Admin).Put("/webhooks/{id}/active", h.setWebhookActive)
			r.With(e.Admin).Post("/webhooks/{id}/test", h.testWebhook)
			r.With(e.Admin).Get("/webhooks/{id}/deliveries", h.webhookDeliveries)
		}
	})
}

// ---- overview, departments, limits ----

func monthStart(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	org := h.Env.Org(r)
	agents, err := h.Admin.AgentSpend(r.Context(), org, monthStart(h.now()))
	if err != nil {
		h.fail(w, err)
		return
	}
	limits, err := h.Admin.DepartmentLimits(r.Context(), org)
	if err != nil {
		h.fail(w, err)
		return
	}
	pol, err := h.Retention.GetPolicy(r.Context(), org)
	if err != nil {
		h.fail(w, err)
		return
	}
	toks, err := h.Tokens.Store.List(r.Context(), org)
	if err != nil {
		h.fail(w, err)
		return
	}
	active := 0
	for _, t := range toks {
		if t.Active(h.now()) {
			active++
		}
	}
	hooks := 0
	if h.Webhooks != nil {
		if eps, err := h.Webhooks.Store.ListEndpoints(r.Context(), org); err == nil {
			hooks = len(eps)
		}
	}
	writeJSON(w, 200, map[string]any{
		"month_start": monthStart(h.now()), "agents": agents, "departments": Overview(agents, limits), "retention": pol,
		"tokens_active": active, "webhooks": hooks,
	})
}

func (h *Handler) policies(w http.ResponseWriter, r *http.Request) {
	org := h.Env.Org(r)
	out := map[string]any{}
	if h.Policies != nil {
		p, err := h.Policies(r.Context(), org)
		if err != nil {
			h.fail(w, err)
			return
		}
		for k, v := range p {
			out[k] = v
		}
	}
	pol, err := h.Retention.GetPolicy(r.Context(), org)
	if err != nil {
		h.fail(w, err)
		return
	}
	out["retention"] = pol
	out["scopes"] = AllScopes
	out["webhook_events"] = webhooks.AllEvents
	writeJSON(w, 200, out)
}

func (h *Handler) putDepartment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Department string `json:"department"`
	}
	if err := h.Env.Decode(w, r, &body); err != nil {
		h.fail(w, err)
		return
	}
	d, err := NormalizeDepartment(body.Department)
	if err != nil {
		h.fail(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.Admin.SetDepartment(r.Context(), h.Env.Org(r), id, d); err != nil {
		h.fail(w, err)
		return
	}
	h.audit(r, "governance.department_set", "agent", id, map[string]any{"department": d})
	writeJSON(w, 200, map[string]any{"agent_id": id, "department": d})
}

func (h *Handler) putDepartmentLimit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Department string  `json:"department"`
		MonthlyUSD float64 `json:"monthly_usd"` // 0 removes the limit
	}
	if err := h.Env.Decode(w, r, &body); err != nil {
		h.fail(w, err)
		return
	}
	d, err := NormalizeDepartment(body.Department)
	if err != nil || d == "" {
		h.fail(w, fmt.Errorf("%w: department is required", ErrInvalid))
		return
	}
	if body.MonthlyUSD < 0 || body.MonthlyUSD > 1e9 || body.MonthlyUSD != body.MonthlyUSD {
		h.fail(w, fmt.Errorf("%w: monthly_usd must be between 0 and 1e9", ErrInvalid))
		return
	}
	if err := h.Admin.SetDepartmentLimit(r.Context(), h.Env.Org(r), d, body.MonthlyUSD, h.Env.Actor(r)); err != nil {
		h.fail(w, err)
		return
	}
	if h.OnLimitChange != nil {
		h.OnLimitChange()
	}
	h.audit(r, "governance.department_limit_set", "department", d, map[string]any{"monthly_usd": body.MonthlyUSD})
	writeJSON(w, 200, map[string]any{"department": d, "monthly_usd": body.MonthlyUSD})
}

// ---- reports ----

func parseWhen(v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			t = t.UTC()
			return &t, nil
		}
	}
	return nil, fmt.Errorf("%w: %q is not an RFC 3339 timestamp or a YYYY-MM-DD date", ErrInvalid, v)
}

// buildUsage parses by/from/to. A plain date in "to" includes that whole day.
func (h *Handler) buildUsage(r *http.Request) (UsageReport, error) {
	q := r.URL.Query()
	from, err := parseWhen(q.Get("from"))
	if err != nil {
		return UsageReport{}, err
	}
	to, err := parseWhen(q.Get("to"))
	if err != nil {
		return UsageReport{}, err
	}
	if to != nil && len(q.Get("to")) == len("2006-01-02") {
		t := to.AddDate(0, 0, 1)
		to = &t
	}
	by := q.Get("by")
	if by == "" {
		by = ByAgent
	}
	return BuildUsageReport(r.Context(), h.Reports, h.Env.Org(r), by, from, to, h.now())
}

func (h *Handler) usageReport(w http.ResponseWriter, r *http.Request) {
	rep, err := h.buildUsage(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	if strings.EqualFold(r.URL.Query().Get("format"), "csv") {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="usage-by-%s-%s.csv"`, rep.By, h.now().Format("20060102")))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if err := rep.WriteCSV(w); err == nil {
			h.audit(r, "governance.usage_exported", "usage", rep.By, map[string]any{"rows": len(rep.Rows)})
		}
		return
	}
	writeJSON(w, 200, rep)
}

// ---- audit export ----

func (h *Handler) auditExport(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	q := domain.AuditQuery{Actor: v.Get("actor"), Action: v.Get("type"), Entity: v.Get("entity"), RequestID: v.Get("request_id")}
	var err error
	if q.From, err = parseWhen(v.Get("from")); err != nil {
		h.fail(w, err)
		return
	}
	if q.To, err = parseWhen(v.Get("to")); err != nil {
		h.fail(w, err)
		return
	}
	if q.To != nil && len(v.Get("to")) == len("2006-01-02") {
		t := q.To.AddDate(0, 0, 1)
		q.To = &t
	}
	if q.From != nil && q.To != nil && !q.From.Before(*q.To) {
		h.fail(w, fmt.Errorf("%w: from must be before to", ErrInvalid))
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Minute))
	started := false
	err = ExportAudit(r.Context(), h.Audit, h.Env.Org(r), q, v.Get("format"), h.now(), w, func(x AuditExport) {
		started = true
		hd := w.Header()
		hd.Set("Content-Type", x.ContentType)
		hd.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, x.Filename))
		hd.Set("Cache-Control", "no-store")
		hd.Set("X-Content-Type-Options", "nosniff")
		names := make([]string, 0, len(x.Headers))
		for k, val := range x.Headers {
			hd.Set(k, val)
			names = append(names, k)
		}
		hd.Set("Access-Control-Expose-Headers", strings.Join(names, ", ")) // lets the console read the verification
	})
	if err != nil {
		if !started {
			h.fail(w, err)
		}
		return
	}
}

// ---- retention ----

func (h *Handler) getRetention(w http.ResponseWriter, r *http.Request) {
	pol, err := h.Retention.GetPolicy(r.Context(), h.Env.Org(r))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"policy": pol, "min_days": map[string]int{"chats": MinChatsDays, "files": MinFilesDays, "audit": MinAuditDays}, "max_days": MaxDays,
		"audit_purge_supported": false})
}

func (h *Handler) putRetention(w http.ResponseWriter, r *http.Request) {
	var body RetentionPolicy
	if err := h.Env.Decode(w, r, &body); err != nil {
		h.fail(w, err)
		return
	}
	if err := body.Validate(); err != nil {
		h.fail(w, err)
		return
	}
	org := h.Env.Org(r)
	if h.Purger != nil && h.Purger.Protected[org] && body.Enabled {
		h.fail(w, fmt.Errorf("%w: the shared demo organization cannot have a retention policy", ErrInvalid))
		return
	}
	body.LastRunAt, body.LastResult = nil, nil
	if err := h.Retention.PutPolicy(r.Context(), org, body, h.Env.Actor(r)); err != nil {
		h.fail(w, err)
		return
	}
	h.audit(r, "retention.policy_set", "org", org, map[string]any{"enabled": body.Enabled, "chats_days": body.ChatsDays, "files_days": body.FilesDays, "audit_days": body.AuditDays})
	pol, _ := h.Retention.GetPolicy(r.Context(), org)
	writeJSON(w, 200, map[string]any{"policy": pol})
}

func (h *Handler) previewRetention(w http.ResponseWriter, r *http.Request) {
	org := h.Env.Org(r)
	pol, err := h.Retention.GetPolicy(r.Context(), org)
	if err != nil {
		h.fail(w, err)
		return
	}
	res, err := h.Purger.Preview(r.Context(), org, pol)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (h *Handler) runRetention(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if err := h.Env.Decode(w, r, &body); err != nil || !body.Confirm {
		h.fail(w, fmt.Errorf("%w: confirm must be true", ErrInvalid))
		return
	}
	org := h.Env.Org(r)
	pol, err := h.Retention.GetPolicy(r.Context(), org)
	if err != nil {
		h.fail(w, err)
		return
	}
	if !pol.Enabled {
		h.fail(w, fmt.Errorf("%w: the policy is switched off", ErrInvalid))
		return
	}
	res, err := h.Purger.RunNow(r.Context(), org, pol)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

// ---- tokens ----

func (h *Handler) listTokens(w http.ResponseWriter, r *http.Request) {
	ts, err := h.Tokens.Store.List(r.Context(), h.Env.Org(r))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": ts, "scopes": AllScopes})
}

func (h *Handler) createToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name          string   `json:"name"`
		Scopes        []string `json:"scopes"`
		ExpiresInDays int      `json:"expires_in_days"`
	}
	if err := h.Env.Decode(w, r, &body); err != nil {
		h.fail(w, err)
		return
	}
	t, plain, err := h.Tokens.Create(r.Context(), h.Env.Org(r), h.Env.Actor(r), body.Name, body.Scopes, body.ExpiresInDays)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.audit(r, "api_token.created", "api_token", t.ID, map[string]any{"name": t.Name, "scopes": t.Scopes, "expires_in_days": body.ExpiresInDays})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"token": plain, "item": t, "note": "shown once"})
}

func (h *Handler) revokeToken(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.Tokens.Store.Revoke(r.Context(), h.Env.Org(r), id, h.now()); err != nil {
		h.fail(w, err)
		return
	}
	h.audit(r, "api_token.revoked", "api_token", id, nil)
	writeJSON(w, 200, map[string]string{"status": "revoked"})
}

// ---- webhooks ----

func (h *Handler) listWebhooks(w http.ResponseWriter, r *http.Request) {
	eps, err := h.Webhooks.Store.ListEndpoints(r.Context(), h.Env.Org(r))
	if err != nil {
		h.fail(w, err)
		return
	}
	if eps == nil {
		eps = []webhooks.Endpoint{}
	}
	writeJSON(w, 200, map[string]any{"items": eps, "events": webhooks.AllEvents})
}

func (h *Handler) createWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL         string   `json:"url"`
		Description string   `json:"description"`
		Events      []string `json:"events"`
	}
	if err := h.Env.Decode(w, r, &body); err != nil {
		h.fail(w, err)
		return
	}
	ep, secret, err := h.Webhooks.CreateEndpoint(r.Context(), h.Env.Org(r), h.Env.Actor(r), body.URL, body.Description, body.Events)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.audit(r, "webhook.created", "webhook", ep.ID, map[string]any{"events": ep.Events, "host": hostOf(ep.URL)})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"item": ep, "secret": secret, "note": "shown once"})
}

func hostOf(raw string) string {
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

func (h *Handler) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.Webhooks.Store.DeleteEndpoint(r.Context(), h.Env.Org(r), id); err != nil {
		h.fail(w, err)
		return
	}
	h.audit(r, "webhook.deleted", "webhook", id, nil)
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (h *Handler) setWebhookActive(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Active bool `json:"active"`
	}
	if err := h.Env.Decode(w, r, &body); err != nil {
		h.fail(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	reason := ""
	if !body.Active {
		reason = "disabled_by_admin"
	}
	if err := h.Webhooks.Store.SetActive(r.Context(), h.Env.Org(r), id, body.Active, reason); err != nil {
		h.fail(w, err)
		return
	}
	h.audit(r, "webhook.toggled", "webhook", id, map[string]any{"active": body.Active})
	writeJSON(w, 200, map[string]any{"id": id, "active": body.Active})
}

func (h *Handler) testWebhook(w http.ResponseWriter, r *http.Request) {
	d, err := h.Webhooks.SendTest(r.Context(), h.Env.Org(r), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func (h *Handler) webhookDeliveries(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	ds, err := h.Webhooks.Store.ListDeliveries(r.Context(), h.Env.Org(r), chi.URLParam(r, "id"), limit)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": ds})
}
