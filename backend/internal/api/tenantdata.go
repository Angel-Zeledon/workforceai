package api

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/tenantdata"
)

// mountTenantData registers the tenant data lifecycle (internal/tenantdata):
//
//	GET    /org/export    owner or admin; ZIP of all the organization's data (1 per hour per org)
//	GET    /org/deletion  any member; deletion status (the app shows a banner while pending)
//	POST   /org/deletion  owner only; {confirm_name, password}: schedules the purge after 7 days
//	DELETE /org/deletion  owner only; cancels a pending deletion
//
// No-op when Deps.TenantData is nil.
func (s *server) mountTenantData(r chi.Router) {
	if s.TenantData == nil {
		return
	}
	r.With(s.requireRole(auth.RoleAdmin)).Get("/org/export", s.exportOrg)
	r.Get("/org/deletion", s.orgDeletionStatus)
	r.With(s.can(auth.PermOrgDelete)).Post("/org/deletion", s.scheduleOrgDeletion)
	r.With(s.can(auth.PermOrgDelete)).Delete("/org/deletion", s.cancelOrgDeletion)
}

func (s *server) tenantDataFail(w http.ResponseWriter, err error) {
	var rl *tenantdata.RateLimitedError
	switch {
	case errors.As(err, &rl):
		secs := int(math.Ceil(rl.RetryAfter.Seconds()))
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		code := "rate_limited"
		if errors.Is(err, tenantdata.ErrExportTooOften) {
			code = "export_rate_limited"
		}
		codeErr(w, http.StatusTooManyRequests, code, err.Error())
	case errors.Is(err, tenantdata.ErrProtected):
		codeErr(w, http.StatusConflict, "protected", "the demo organization cannot be deleted")
	case errors.Is(err, tenantdata.ErrNameMismatch):
		codeErr(w, http.StatusBadRequest, "name_mismatch", "the confirmation does not match the organization name")
	case errors.Is(err, tenantdata.ErrBadPassword):
		codeErr(w, http.StatusForbidden, "invalid_password", "wrong password")
	case errors.Is(err, tenantdata.ErrNoAccounts):
		codeErr(w, http.StatusForbidden, "auth_required", "deleting an organization requires authentication")
	default:
		s.fail(w, err)
	}
}

func (s *server) exportOrg(w http.ResponseWriter, r *http.Request) {
	ctx, org := r.Context(), s.org(r)
	if err := s.TenantData.AllowExport(ctx, org); err != nil {
		s.tenantDataFail(w, err)
		return
	}
	actor := application.ActorFrom(ctx, "user")
	exp, err := s.TenantData.BeginExport(ctx, org, actor)
	if err != nil {
		s.tenantDataFail(w, err)
		return
	}
	defer exp.Close()
	// Large organizations: the export may take longer than the REST write timeout.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(60 * time.Minute))
	name := exp.Org().Slug
	if name == "" {
		name = "org"
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="export-%s-%s.zip"`, safeFileName(name), time.Now().UTC().Format("20060102-1504")))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	m, err := exp.WriteZip(w)
	if err != nil {
		// Headers are gone: the client gets a truncated (invalid) ZIP.
		s.Log.Error("org export failed mid-stream", "org", org, "err", err)
		return
	}
	var rows int64
	for _, t := range m.Tables {
		rows += t.Rows
	}
	if s.Rec != nil {
		s.Rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: "org.exported", Entity: "org", EntityID: org,
			Details: map[string]any{"tables": len(m.Tables), "rows": rows, "files": m.Files, "file_bytes": m.FileBytes}})
	}
}

// safeFileName keeps [a-z0-9-] for the Content-Disposition file name.
func safeFileName(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s) && len(b) < 60; i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			b = append(b, c)
		case c >= 'A' && c <= 'Z':
			b = append(b, c+'a'-'A')
		default:
			b = append(b, '-')
		}
	}
	if len(b) == 0 {
		return "org"
	}
	return string(b)
}

func (s *server) orgDeletionStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.TenantData.Status(r.Context(), s.org(r))
	if err != nil {
		s.tenantDataFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *server) scheduleOrgDeletion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConfirmName string `json:"confirm_name"`
		Password    string `json:"password"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	ctx, org := r.Context(), s.org(r)
	user, _ := auth.UserIDFrom(ctx)
	if !s.AuthEnabled {
		user = "" // the open demo has no accounts (and its organization is protected anyway)
	}
	if org == domain.DemoOrgID {
		s.tenantDataFail(w, tenantdata.ErrProtected)
		return
	}
	before, err := s.TenantData.Status(ctx, org)
	if err != nil {
		s.tenantDataFail(w, err)
		return
	}
	st, err := s.TenantData.ScheduleDeletion(ctx, org, user, body.ConfirmName, body.Password)
	if err != nil {
		if errors.Is(err, tenantdata.ErrBadPassword) && s.Rec != nil {
			s.Rec.Audit(ctx, domain.AuditLog{Actor: application.ActorFrom(ctx, "user"), Action: "org.deletion_denied", Entity: "org", EntityID: org,
				Details: map[string]any{"reason": "invalid_password"}})
		}
		s.tenantDataFail(w, err)
		return
	}
	if !before.Pending && s.Rec != nil {
		s.Rec.Audit(ctx, domain.AuditLog{Actor: application.ActorFrom(ctx, "user"), Action: "org.deletion_scheduled", Entity: "org", EntityID: org,
			Details: map[string]any{"deletion_at": st.DeletionAt}})
	}
	writeJSON(w, http.StatusAccepted, st)
}

func (s *server) cancelOrgDeletion(w http.ResponseWriter, r *http.Request) {
	ctx, org := r.Context(), s.org(r)
	before, err := s.TenantData.Status(ctx, org)
	if err != nil {
		s.tenantDataFail(w, err)
		return
	}
	st, err := s.TenantData.CancelDeletion(ctx, org)
	if err != nil {
		s.tenantDataFail(w, err)
		return
	}
	if before.Pending && s.Rec != nil {
		s.Rec.Audit(ctx, domain.AuditLog{Actor: application.ActorFrom(ctx, "user"), Action: "org.deletion_cancelled", Entity: "org", EntityID: org})
	}
	writeJSON(w, http.StatusOK, st)
}
