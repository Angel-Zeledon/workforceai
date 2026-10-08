package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
)

// mountModelPolicy registers the organization model policy endpoints (A3).
// Owners and admins only (org:manage), also for reading: the policy says where
// the organization's data may be sent. No-op when Deps.ModelPolicy is nil.
func (s *server) mountModelPolicy(r chi.Router) {
	if s.ModelPolicy == nil {
		return
	}
	manage := s.can(auth.PermOrgManage)
	r.With(manage).Get("/settings/model-policy", s.getModelPolicy)
	r.With(manage).Put("/settings/model-policy", s.putModelPolicy)
}

func (s *server) getModelPolicy(w http.ResponseWriter, r *http.Request) {
	v, err := s.ModelPolicy.Get(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) putModelPolicy(w http.ResponseWriter, r *http.Request) {
	var in application.ModelPolicy
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.ModelPolicy.Put(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
