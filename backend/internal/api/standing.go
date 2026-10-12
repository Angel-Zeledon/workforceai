package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
)

// mountStanding registers the standing per-project approvals (A1):
//
//	GET    /projects/{id}/standing-approvals          approvals:read   history included, newest first
//	DELETE /projects/{id}/standing-approvals/{sid}    approvals:decide revoke (idempotent)
//
// They are created by approving with {"always_allow": true} (see decide), never
// directly, so a standing permission always traces back to a human decision.
// No-op when Deps.Standing is nil.
func (s *server) mountStanding(r chi.Router) {
	if s.Standing == nil {
		return
	}
	r.With(s.can(auth.PermApprovalsRead)).Get("/projects/{id}/standing-approvals", s.listStanding)
	r.With(s.can(auth.PermApprovalsDecide)).Delete("/projects/{id}/standing-approvals/{sid}", s.revokeStanding)
}

func (s *server) listStanding(w http.ResponseWriter, r *http.Request) {
	v, err := s.Standing.List(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(v))
}

func (s *server) revokeStanding(w http.ResponseWriter, r *http.Request) {
	v, err := s.Standing.Revoke(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "sid"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
