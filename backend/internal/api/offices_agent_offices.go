package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/offices"
)

// Agents in several offices (design 2026-11-23 §5; docs/architecture/08-api.md): the base office and the other
// offices an agent works for. Behind MULTI_OFFICE_AGENTS: with it off the grants answer 403.
func (s *server) mountAgentOffices(r chi.Router) {
	read := s.can(auth.PermAgentsRead) // who may do what is the offices model's call, as for /agents/{id}/access
	r.With(read).Get("/agents/{id}/offices", s.getAgentOffices)
	r.With(read).Post("/agents/{id}/offices", s.postAgentOffice)
	r.With(read).Post("/agents/{id}/offices/{officeID}/accept", s.acceptAgentOffice)
	r.With(read).Delete("/agents/{id}/offices/{officeID}", s.deleteAgentOffice)
}

func (s *server) getAgentOffices(w http.ResponseWriter, r *http.Request) {
	v, err := s.Offices.AgentOffices(r.Context(), s.org(r), s.viewer(r), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) postAgentOffice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OfficeID string `json:"office_id"`
	}
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	if strings.TrimSpace(in.OfficeID) == "" {
		s.fail(w, errInvalid("office_id is required"))
		return
	}
	v, err := s.Offices.GrantOffice(r.Context(), s.org(r), s.viewer(r), chi.URLParam(r, "id"), strings.TrimSpace(in.OfficeID))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) acceptAgentOffice(w http.ResponseWriter, r *http.Request) {
	v, err := s.Offices.AcceptOffice(r.Context(), s.org(r), s.viewer(r), chi.URLParam(r, "id"), chi.URLParam(r, "officeID"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) deleteAgentOffice(w http.ResponseWriter, r *http.Request) {
	v, err := s.Offices.RemoveOffice(r.Context(), s.org(r), s.viewer(r), chi.URLParam(r, "id"), chi.URLParam(r, "officeID"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// officeNameOf is the name of the base office of an agent (the single source: the offices model).
func officeNameOf(m *offices.Model, agentID string) (string, string) {
	ea := m.Agent(agentID)
	o, _ := m.Office(ea.OfficeID)
	return ea.OfficeID, o.Name
}
