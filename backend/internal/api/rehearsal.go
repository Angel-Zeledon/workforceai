package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// mountRehearsal registers the rehearsal API (B9, docs/architecture/rehearsal.md):
//
//	GET  /rehearsal/agents                    connections:read   switches + evidence per agent (would approve / reject)
//	PUT  /agents/{id}/rehearsal               controls:pause     {"enabled": bool}; turning it OFF needs controls:release
//	GET  /rehearsal/actions?request_id&agent_id&limit  approvals:read  "would have done" entries, newest first
//	GET  /requests/{id}/rehearsal             requests:read      the rehearsal report of a request
//	POST /rehearsal/actions/{id}/verdict      approvals:decide   {"verdict": "would_approve"|"would_reject"|""}
//
// A rehearsal request is POST /requests {"text", "rehearsal": true}. Nothing here executes a tool: a verdict is the
// person's judgement on something that did NOT happen. No-op when Deps.Rehearsal is nil.
func (s *server) mountRehearsal(r chi.Router) {
	if s.Rehearsal == nil {
		return
	}
	r.With(s.can(auth.PermConnectionsRead)).Get("/rehearsal/agents", s.rehearsalAgents)
	r.With(s.can(auth.PermControlsPause)).Put("/agents/{id}/rehearsal", s.putAgentRehearsal)
	r.With(s.can(auth.PermApprovalsRead)).Get("/rehearsal/actions", s.rehearsalActions)
	r.With(s.can(auth.PermRequestsRead)).Get("/requests/{id}/rehearsal", s.requestRehearsal)
	r.With(s.can(auth.PermApprovalsDecide)).Post("/rehearsal/actions/{id}/verdict", s.rehearsalVerdict)
}

func (s *server) rehearsalAgents(w http.ResponseWriter, r *http.Request) {
	v, err := s.Rehearsal.Agents(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": v})
}

func (s *server) putAgentRehearsal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if body.Enabled == nil {
		codeErr(w, http.StatusBadRequest, "invalid", "enabled is required")
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := s.Store.GetAgent(r.Context(), s.org(r), id); err != nil {
		s.fail(w, err)
		return
	}
	// Turning rehearsal on only takes power away (like a pause); turning it off gives the agent its real effects back.
	if !*body.Enabled && !s.has(r, auth.PermControlsRelease) {
		codeErr(w, http.StatusForbidden, "forbidden", "only the owner can take an agent out of rehearsal")
		return
	}
	v, err := s.Rehearsal.SetAgent(r.Context(), id, *body.Enabled, s.actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) rehearsalActions(w http.ResponseWriter, r *http.Request) {
	q := domain.RehearsalQuery{RequestID: r.URL.Query().Get("request_id"), AgentID: r.URL.Query().Get("agent_id")}
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 {
			codeErr(w, http.StatusBadRequest, "invalid", "limit must be a positive integer")
			return
		}
		q.Limit = n
	}
	v, err := s.Rehearsal.List(r.Context(), q)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": v})
}

func (s *server) requestRehearsal(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.Store.GetRequest(r.Context(), s.org(r), id); err != nil {
		s.fail(w, err)
		return
	}
	rep, err := s.Rehearsal.Report(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *server) rehearsalVerdict(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Verdict string `json:"verdict"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.Rehearsal.SetVerdict(r.Context(), chi.URLParam(r, "id"), body.Verdict, s.actor(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
