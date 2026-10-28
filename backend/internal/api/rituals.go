package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/rituals"
)

// mountRituals registers the team rituals (B4, docs/architecture/rituals.md):
//
//	GET  /rituals?kind=&limit=                        requests:read    stand-ups and retros, newest first
//	GET  /rituals/{id}                                requests:read    one ritual (a retro carries its proposals)
//	POST /rituals/{kind}/run                          requests:create  hold a stand-up or a retro now (201)
//	GET  /rituals/proposals?status=&agent_id=&ritual_id=&limit=   requests:read
//	POST /rituals/proposals/{id}/accept               agents:write     a person accepts; an instruction note goes to the work profile
//	POST /rituals/proposals/{id}/dismiss              agents:write
//	GET  /settings/rituals                            requests:read
//	PUT  /settings/rituals                            org:manage
//
// Agents never run rituals nor resolve proposals (the service refuses agent principals). No-op when Deps.Rituals is nil.
func (s *server) mountRituals(r chi.Router) {
	if s.Rituals == nil {
		return
	}
	read := s.can(auth.PermRequestsRead)
	r.With(read).Get("/rituals", s.listRituals)
	r.With(read).Get("/rituals/proposals", s.listRitualProposals)
	r.With(read).Get("/rituals/{id}", s.getRitual)
	r.With(s.can(auth.PermRequestsCreate)).Post("/rituals/{kind}/run", s.runRitual)
	r.With(s.can(auth.PermAgentsWrite)).Post("/rituals/proposals/{id}/accept", s.acceptRitualProposal)
	r.With(s.can(auth.PermAgentsWrite)).Post("/rituals/proposals/{id}/dismiss", s.dismissRitualProposal)
	r.With(read).Get("/settings/rituals", s.getRitualSettings)
	r.With(s.can(auth.PermOrgManage)).Put("/settings/rituals", s.putRitualSettings)
}

// ritualFail maps a ritual that cannot be held now to a stable code; everything else goes through s.fail.
func (s *server) ritualFail(w http.ResponseWriter, err error) {
	var re *rituals.Error
	if errors.As(err, &re) {
		status := http.StatusConflict
		if re.Code == rituals.CodeLimit {
			status = http.StatusTooManyRequests
		}
		writeJSON(w, status, map[string]string{"error": re.Error(), "code": re.Code})
		return
	}
	s.fail(w, err)
}

func (s *server) listRituals(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	v, err := s.Rituals.List(r.Context(), rituals.Kind(r.URL.Query().Get("kind")), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(v))
}

func (s *server) getRitual(w http.ResponseWriter, r *http.Request) {
	v, err := s.Rituals.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) runRitual(w http.ResponseWriter, r *http.Request) {
	v, err := s.Rituals.RunNow(r.Context(), rituals.Kind(chi.URLParam(r, "kind")))
	if err != nil {
		s.ritualFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (s *server) listRitualProposals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	v, err := s.Rituals.Proposals(r.Context(), rituals.ProposalQuery{Status: q.Get("status"), AgentID: q.Get("agent_id"), RitualID: q.Get("ritual_id"), Limit: limit})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(v))
}

func (s *server) acceptRitualProposal(w http.ResponseWriter, r *http.Request) {
	v, err := s.Rituals.AcceptProposal(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) dismissRitualProposal(w http.ResponseWriter, r *http.Request) {
	v, err := s.Rituals.DismissProposal(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) getRitualSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.Rituals.Settings(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) putRitualSettings(w http.ResponseWriter, r *http.Request) {
	var in rituals.Settings
	if err := s.decode(w, r, &in); err != nil {
		s.fail(w, err)
		return
	}
	v, err := s.Rituals.PutSettings(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
