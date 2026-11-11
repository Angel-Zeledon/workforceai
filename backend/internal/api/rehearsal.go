package api

import (
	"net/http"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
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
	r.With(s.can(auth.PermRequestsRead), s.workRequest).Get("/requests/{id}/rehearsal", s.requestRehearsal)
	r.With(s.can(auth.PermApprovalsDecide), s.workRehearsal).Post("/rehearsal/actions/{id}/verdict", s.rehearsalVerdict)
}

func (s *server) rehearsalAgents(w http.ResponseWriter, r *http.Request) {
	v, err := s.Rehearsal.Agents(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	if v, err = s.visibleRehearsals(r, v); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": v})
}

// visibleRehearsals (D3) recounts the evidence of the agents whose work the person does not see with only the
// rehearsed actions they see (those of their own requests, work_sealed.go); the switch stays (an admin manages the
// agent). An agent left with no switch and no action is left out, like one that never rehearsed.
func (s *server) visibleRehearsals(r *http.Request, v []application.AgentSummary) ([]application.AgentSummary, error) {
	ws := s.work(r)
	if err := ws.load(); err != nil || ws.open() || !ws.hidesAny() {
		return v, err
	}
	as, err := s.Rehearsal.List(r.Context(), domain.RehearsalQuery{})
	if err != nil {
		return nil, err
	}
	seen := map[string][]domain.RehearsalAction{}
	for _, a := range as {
		if !ws.agent(a.AgentID) && ws.seesRehearsal(a) {
			seen[a.AgentID] = append(seen[a.AgentID], a)
		}
	}
	out := v[:0]
	for _, x := range v {
		if !ws.agent(x.AgentID) {
			c := application.Count(seen[x.AgentID])
			x.Actions, x.WouldApprove, x.WouldReject, x.Undecided = c.Total, c.WouldApprove, c.WouldReject, c.Undecided
			if x.UpdatedAt == nil && x.Actions == 0 {
				continue
			}
		}
		out = append(out, x)
	}
	return out, nil
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
		codeErr(w, http.StatusForbidden, "forbidden", "only the owner can turn off “try without sending” for an agent")
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
	ws := s.work(r) // D3: a private department's rehearsals are its members' (work_sealed.go)
	if err := ws.load(); err != nil {
		s.fail(w, err)
		return
	}
	want := q.Limit
	if !ws.open() {
		q.Limit = 0 // the widest window (the service's maximum), cut at the limit after leaving out
	}
	v, err := s.Rehearsal.List(r.Context(), q)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ws.open() {
		v = firstN(slices.DeleteFunc(v, func(a domain.RehearsalAction) bool { return !ws.seesRehearsal(a) }), want)
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
