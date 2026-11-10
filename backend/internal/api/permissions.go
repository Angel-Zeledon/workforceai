package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/trust"
)

// Approval permissions per agent (docs/architecture/06-permisos-autonomia.md section 11, 08-api.md). Reading follows
// agents:read. Changing a level or an exception is allowed to admins, owners and the person responsible for the
// agent's team, so the route only needs agents:read and the service decides (it audits every refusal). Lifting the
// safety floor: only the owner, with understood=true. The organization default: org:manage.

func (s *server) mountPermissions(r chi.Router) {
	if s.Permissions == nil {
		return
	}
	read := s.can(auth.PermAgentsRead)
	r.With(read).Get("/permissions", s.permOverview)
	r.With(s.can(auth.PermOrgManage)).Put("/permissions/default", s.permDefault)
	r.With(read).Get("/agents/{id}/permissions", s.permView)
	r.With(read).Put("/agents/{id}/permissions/level", s.permLevel)
	r.With(read).Put("/agents/{id}/permissions/exceptions/{category}", s.permSetException)
	r.With(read).Delete("/agents/{id}/permissions/exceptions/{category}", s.permRemoveException)
	r.With(read).Post("/agents/{id}/permissions/floor/{category}", s.permLiftFloor)
	r.With(read).Delete("/agents/{id}/permissions/floor/{category}", s.permRestoreFloor)
	// "Approve and do not ask again" (Pendientes) and the project / batch scopes it can create.
	r.With(s.can(auth.PermApprovalsDecide), s.workApproval).Post("/approvals/{id}/remember", s.permRemember)
	r.With(read).Get("/permissions/scopes", s.permScopes)
	r.With(read).Delete("/permissions/scopes/{id}", s.permRevokeScope)
}

func (s *server) permRemember(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope string `json:"scope"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.Permissions.Remember(r.Context(), chi.URLParam(r, "id"), body.Scope)
	if err != nil {
		for _, code := range []string{"not_rememberable", "no_project", "no_batch"} {
			if errors.Is(err, domain.ErrConflict) && strings.Contains(err.Error(), code) {
				codeErr(w, http.StatusConflict, code, err.Error())
				return
			}
		}
		s.permFail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) permScopes(w http.ResponseWriter, r *http.Request) {
	v, err := s.Permissions.Scopes(r.Context(), r.URL.Query().Get("project_id"), r.URL.Query().Get("batch_id"))
	if err != nil {
		s.permFail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) permRevokeScope(w http.ResponseWriter, r *http.Request) {
	if err := s.Permissions.RevokeScope(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.permFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) permFail(w http.ResponseWriter, err error) {
	var be trust.BlockedError
	if errors.As(err, &be) {
		codeErr(w, http.StatusConflict, be.Code, err.Error())
		return
	}
	if errors.Is(err, domain.ErrConflict) && strings.Contains(err.Error(), "choose_level_first") {
		codeErr(w, http.StatusConflict, "choose_level_first", err.Error())
		return
	}
	s.fail(w, err)
}

func (s *server) permOverview(w http.ResponseWriter, r *http.Request) {
	v, err := s.Permissions.Overview(r.Context())
	if err != nil {
		s.permFail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) permDefault(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Level string `json:"level"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.Permissions.SetDefault(r.Context(), body.Level)
	if err != nil {
		s.permFail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) permView(w http.ResponseWriter, r *http.Request) {
	v, err := s.Permissions.View(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.permFail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) permLevel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Level string `json:"level"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.Permissions.ChooseLevel(r.Context(), chi.URLParam(r, "id"), body.Level)
	if err != nil {
		s.permFail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) permSetException(w http.ResponseWriter, r *http.Request) {
	var body application.ExceptionInput
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.Permissions.SetException(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "category"), body)
	if err != nil {
		s.permFail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) permRemoveException(w http.ResponseWriter, r *http.Request) {
	v, err := s.Permissions.RemoveException(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "category"))
	if err != nil {
		s.permFail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) permLiftFloor(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Understood bool `json:"understood"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.Permissions.LiftFloor(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "category"), body.Understood)
	if err != nil {
		s.permFail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *server) permRestoreFloor(w http.ResponseWriter, r *http.Request) {
	v, err := s.Permissions.RestoreFloor(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "category"))
	if err != nil {
		s.permFail(w, err)
		return
	}
	writeJSON(w, 200, v)
}
