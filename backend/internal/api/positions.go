package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/positions"
)

// Positions inside a department (docs/architecture/positions.md; 08-api.md section 55): a department's positions
// ("Gerente", "Asistente de gerente", "Especialista") with their skills and their library, and the position of each
// agent. Everybody with agents:read reads; the positions service decides every change (an administrator or the
// department's responsible, never an agent) and answers 404 for a private department the person is not in. Every
// change is audited (position.*). The agent routes are versioned by the history hook (F4: the position is part of the
// agent's configuration snapshot); the /offices routes are org-wide for it. Optional: no-op without Deps.Positions.
func (s *server) mountPositions(r chi.Router) {
	if s.Positions == nil {
		return
	}
	read := s.can(auth.PermAgentsRead)
	r.With(read).Get("/offices/{id}/positions", s.listPositions)
	r.With(read).Get("/offices/{id}/positions/templates", s.positionTemplates)
	r.With(read).Post("/offices/{id}/positions", s.createPosition)
	r.With(read).Put("/offices/{id}/positions/{pid}", s.updatePosition)
	r.With(read).Delete("/offices/{id}/positions/{pid}", s.deletePosition)
	r.With(read, s.workAgent).Get("/agents/{id}/position", s.agentPosition)
	r.With(read, s.workAgent).Put("/agents/{id}/position", s.putAgentPosition)
	r.With(read, s.workAgent).Delete("/agents/{id}/position", s.deleteAgentPosition)
}

// positionActor is the person behind the request (the open demo: everybody manages).
func (s *server) positionActor(r *http.Request) positions.Actor {
	v := s.viewer(r)
	return positions.Actor{ID: s.actor(r), Admin: v.Admin, Open: !s.AuthEnabled}
}

// positionFail maps positions errors; the stable code (and, for exceeds_superior, what exceeds) travels in the body.
func (s *server) positionFail(w http.ResponseWriter, err error) {
	var pe *positions.Error
	if !errors.As(err, &pe) {
		if errors.Is(err, domain.ErrConflict) {
			codeErr(w, http.StatusConflict, "name_taken", "the department already has a position with that name")
			return
		}
		s.fail(w, err)
		return
	}
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, domain.ErrForbidden):
		status = http.StatusForbidden
	}
	body := map[string]any{"code": pe.Code, "error": pe.Msg}
	if pe.Details != nil {
		body["details"] = pe.Details
	}
	writeJSON(w, status, body)
}

func (s *server) listPositions(w http.ResponseWriter, r *http.Request) {
	v, err := s.Positions.List(r.Context(), s.positionActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.positionFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"office":     map[string]any{"id": v.Office.ID, "name": v.Office.Name, "folder_path": v.Bounds.Office},
		"can_manage": v.CanManage, "positions": list(v.Positions), "agents": list(v.Agents), "skills": list(v.Skills), "bounds": v.Bounds,
	})
}

func (s *server) positionTemplates(w http.ResponseWriter, r *http.Request) {
	ts, err := s.Positions.Templates(r.Context(), s.positionActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.positionFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": list(ts)})
}

func (s *server) createPosition(w http.ResponseWriter, r *http.Request) {
	var in positions.Input
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	p, err := s.Positions.Create(r.Context(), s.positionActor(r), chi.URLParam(r, "id"), in)
	if err != nil {
		s.positionFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *server) updatePosition(w http.ResponseWriter, r *http.Request) {
	var in struct {
		positions.Input
		DryRun bool `json:"dry_run"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	p, imp, err := s.Positions.Update(r.Context(), s.positionActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "pid"), in.Input, in.DryRun)
	if err != nil {
		s.positionFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"position": p, "impact": imp, "dry_run": in.DryRun})
}

func (s *server) deletePosition(w http.ResponseWriter, r *http.Request) {
	if err := s.Positions.Delete(r.Context(), s.positionActor(r), chi.URLParam(r, "id"), chi.URLParam(r, "pid")); err != nil {
		s.positionFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) writeAgentPosition(w http.ResponseWriter, r *http.Request) {
	v, err := s.Positions.Agent(r.Context(), s.positionActor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.positionFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) agentPosition(w http.ResponseWriter, r *http.Request) { s.writeAgentPosition(w, r) }

func (s *server) putAgentPosition(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PositionID string `json:"position_id"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	a, id := s.positionActor(r), chi.URLParam(r, "id")
	var err error
	if pid := strings.TrimSpace(in.PositionID); pid == "" {
		err = s.Positions.Unassign(r.Context(), a, id)
	} else {
		err = s.Positions.Assign(r.Context(), a, id, pid)
	}
	if err != nil {
		s.positionFail(w, err)
		return
	}
	s.writeAgentPosition(w, r)
}

func (s *server) deleteAgentPosition(w http.ResponseWriter, r *http.Request) {
	if err := s.Positions.Unassign(r.Context(), s.positionActor(r), chi.URLParam(r, "id")); err != nil {
		s.positionFail(w, err)
		return
	}
	s.writeAgentPosition(w, r)
}
