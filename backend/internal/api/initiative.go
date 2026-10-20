package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
)

// mountInitiative registers the proactive suggestions (B2, docs/architecture/initiative.md):
//
//	GET  /initiatives?status=&limit=     requests:read    suggestions raised by agents, newest first
//	POST /initiatives/{id}/accept        requests:create  submits the suggestion as a normal request (202)
//	POST /initiatives/{id}/dismiss       requests:create  discards it
//	GET  /settings/initiative            requests:read    the initiative budget
//	PUT  /settings/initiative            org:manage       change the budget (owner/admin)
//
// Agents never call these: a suggestion is created by the platform from a signal and only a person accepts it,
// which goes through the same path as POST /requests. No-op when Deps.Initiative is nil.
func (s *server) mountInitiative(r chi.Router) {
	if s.Initiative == nil {
		return
	}
	r.With(s.can(auth.PermRequestsRead)).Get("/initiatives", s.listInitiatives)
	r.With(s.can(auth.PermRequestsCreate)).Post("/initiatives/{id}/accept", s.acceptInitiative)
	r.With(s.can(auth.PermRequestsCreate)).Post("/initiatives/{id}/dismiss", s.dismissInitiative)
	r.With(s.can(auth.PermRequestsRead)).Get("/settings/initiative", s.getInitiativeSettings)
	r.With(s.can(auth.PermOrgManage)).Put("/settings/initiative", s.putInitiativeSettings)
}

func (s *server) listInitiatives(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	v, err := s.Initiative.List(r.Context(), application.InitiativeStatus(r.URL.Query().Get("status")), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(v))
}

func (s *server) acceptInitiative(w http.ResponseWriter, r *http.Request) {
	v, err := s.Initiative.Accept(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}

func (s *server) dismissInitiative(w http.ResponseWriter, r *http.Request) {
	v, err := s.Initiative.Dismiss(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) getInitiativeSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.Initiative.Settings(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) putInitiativeSettings(w http.ResponseWriter, r *http.Request) {
	var in application.InitiativeSettings
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.Initiative.PutSettings(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
