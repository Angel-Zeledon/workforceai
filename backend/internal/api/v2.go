package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// mountV2 registers the agent work profile, the thought audit and the
// real-time intervention endpoints (docs/architecture/08-api.md, V2). Every
// piece is optional: nil dependencies leave its routes unmounted.
func (s *server) mountV2(r chi.Router) {
	if s.Profiles != nil {
		r.With(s.can(auth.PermAgentsRead)).Get("/agents/{id}/profile", s.getProfile)
		r.With(s.can(auth.PermAgentsWrite)).Put("/agents/{id}/profile", s.putProfile)
		r.With(s.can(auth.PermAgentsRead)).Get("/agents/{id}/profile/versions", s.profileVersions)
		r.With(s.can(auth.PermAgentsWrite)).Post("/agents/{id}/profile/restore", s.restoreProfile)
	}
	if s.V2 != nil && s.Tracer != nil {
		// Thought audit: admins and owners only (audit:read).
		r.With(s.can(auth.PermAuditRead)).Get("/agents/{id}/traces", s.listTraces)
		r.With(s.can(auth.PermAuditRead)).Get("/agents/{id}/traces/export", s.exportTraces)
		r.With(s.can(auth.PermAuditRead)).Get("/agents/{id}/traces/{traceId}", s.getTrace)
		r.With(s.can(auth.PermAuditRead)).Get("/settings/traces", s.getTraceSettings)
		r.With(s.can(auth.PermOrgManage)).Put("/settings/traces", s.putTraceSettings)
	}
	r.With(s.can(auth.PermTasksManage)).Get("/tasks/{id}/intervention", s.getIntervention)
	r.With(s.can(auth.PermTasksManage)).Post("/tasks/{id}/steer", s.steerTask)
	r.With(s.can(auth.PermTasksManage)).Post("/tasks/{id}/reassign", s.reassignTask)
}

func (s *server) getProfile(w http.ResponseWriter, r *http.Request) {
	v, err := s.Profiles.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) putProfile(w http.ResponseWriter, r *http.Request) {
	var body application.ProfileInput
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.Profiles.Update(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) profileVersions(w http.ResponseWriter, r *http.Request) {
	v, err := s.Profiles.Versions(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) restoreProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version int `json:"version"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.Profiles.Restore(r.Context(), chi.URLParam(r, "id"), body.Version)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

// ---- thought audit ----

func (s *server) traceRetentionDays() int {
	if s.Tracer.Retention > 0 {
		return int(s.Tracer.Retention / (24 * time.Hour))
	}
	return application.DefaultTraceRetention
}

func (s *server) listTraces(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, 200)
	}
	var before time.Time
	if b := r.URL.Query().Get("before"); b != "" {
		t, err := time.Parse(time.RFC3339Nano, b)
		if err != nil {
			s.fail(w, fmt.Errorf("%w: before must be RFC3339", domain.ErrInvalid))
			return
		}
		before = t
	}
	org := s.org(r)
	trs, err := s.V2.ListTraces(r.Context(), org, chi.URLParam(r, "id"), limit, before)
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range trs {
		trs[i].Prompt = nil // the list is light: the prompt comes with the detail
		application.MergeDecisions(&trs[i])
	}
	writeJSON(w, 200, map[string]any{"traces": list(trs), "store_prompts": s.Tracer.StorePrompts(r.Context(), org),
		"retention_days": s.traceRetentionDays()})
}

func (s *server) getTrace(w http.ResponseWriter, r *http.Request) {
	t, err := s.V2.GetTrace(r.Context(), s.org(r), chi.URLParam(r, "id"), chi.URLParam(r, "traceId"))
	if err != nil {
		s.fail(w, err)
		return
	}
	application.MergeDecisions(&t)
	writeJSON(w, 200, t)
}

// exportTraces downloads the (already redacted) traces of an agent. The export
// itself is audited: who exported what, never the content.
func (s *server) exportTraces(w http.ResponseWriter, r *http.Request) {
	org, agent := s.org(r), chi.URLParam(r, "id")
	trs, err := s.V2.ListTraces(r.Context(), org, agent, 500, time.Time{})
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range trs {
		application.MergeDecisions(&trs[i])
	}
	if s.Rec != nil {
		s.Rec.Audit(r.Context(), domain.AuditLog{Actor: application.ActorFrom(r.Context(), "user"), Action: "trace.exported", Entity: "agent", EntityID: agent,
			Details: map[string]any{"traces": len(trs)}})
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="traces-%s.json"`, agent))
	writeJSON(w, 200, map[string]any{"agent_id": agent, "exported_at": time.Now().UTC(), "retention_days": s.traceRetentionDays(), "traces": list(trs)})
}

func (s *server) getTraceSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"store_prompts": s.Tracer.StorePrompts(r.Context(), s.org(r)), "retention_days": s.traceRetentionDays()})
}

func (s *server) putTraceSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		StorePrompts *bool `json:"store_prompts"`
	}
	if err := s.decode(w, r, &body); err != nil || body.StorePrompts == nil {
		s.fail(w, fmt.Errorf("%w: store_prompts is required", domain.ErrInvalid))
		return
	}
	if err := s.V2.SetTraceStorePrompts(r.Context(), s.org(r), *body.StorePrompts); err != nil {
		s.fail(w, err)
		return
	}
	if s.Rec != nil {
		s.Rec.Audit(r.Context(), domain.AuditLog{Actor: application.ActorFrom(r.Context(), "user"), Action: "trace.settings_changed", Entity: "org", EntityID: s.org(r),
			Details: map[string]any{"store_prompts": *body.StorePrompts}})
	}
	writeJSON(w, 200, map[string]any{"store_prompts": *body.StorePrompts, "retention_days": s.traceRetentionDays()})
}

// ---- intervention ----

func (s *server) getIntervention(w http.ResponseWriter, r *http.Request) {
	v, err := s.Orch.Intervention(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) steerTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Note     string `json:"note"`
		ApplyNow bool   `json:"apply_now"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	res, err := s.Orch.Steer(r.Context(), chi.URLParam(r, "id"), body.Note, body.ApplyNow)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func (s *server) reassignTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AgentID string `json:"agent_id"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	t, err := s.Orch.Reassign(r.Context(), chi.URLParam(r, "id"), body.AgentID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, t)
}
