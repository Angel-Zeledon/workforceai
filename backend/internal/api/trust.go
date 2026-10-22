package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/trust"
)

// Trust ladder API (docs/architecture/trust-ladder.md, 08-api.md). Reading follows agents:read; asking for a fresh
// evaluation follows agents:write. Accepting, declining or reverting a change of autonomy needs BOTH agents:write
// and approvals:decide (admin/owner): it is a governance decision, like an approval, and agents never hold it.
// Every response carries the methodology; every decision is audited by the service.

func (s *server) mountTrust(r chi.Router) {
	if s.Trust == nil {
		return
	}
	read, write, decide := s.can(auth.PermAgentsRead), s.can(auth.PermAgentsWrite), s.can(auth.PermApprovalsDecide)
	r.With(read).Get("/trust/agents", s.trustOverview)
	r.With(read).Get("/agents/{id}/trust", s.trustAgent)
	r.With(read).Get("/agents/{id}/trust/proposals", s.trustProposals)
	r.With(write).Post("/agents/{id}/trust/evaluate", s.trustEvaluate)
	r.With(write, decide).Post("/trust/proposals/{id}/accept", s.trustDecide("accept"))
	r.With(write, decide).Post("/trust/proposals/{id}/decline", s.trustDecide("decline"))
	r.With(write, decide).Post("/trust/proposals/{id}/revert", s.trustDecide("revert"))
}

func (s *server) trustFail(w http.ResponseWriter, err error) {
	var be trust.BlockedError
	if errors.As(err, &be) {
		codeErr(w, http.StatusConflict, be.Code, err.Error())
		return
	}
	if errors.Is(err, trust.ErrEvidenceChanged) {
		codeErr(w, http.StatusConflict, "evidence_changed", err.Error())
		return
	}
	s.fail(w, err)
}

func (s *server) trustOverview(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Trust.Overview(r.Context())
	if err != nil {
		s.trustFail(w, err)
		return
	}
	writeJSON(w, 200, rep)
}

func (s *server) trustAgent(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Trust.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.trustFail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"methodology": rep.Methodology, "generated_at": rep.GeneratedAt, "agent": rep.Agents[0]})
}

func (s *server) trustProposals(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	v, err := s.Trust.Proposals(r.Context(), chi.URLParam(r, "id"), limit)
	if err != nil {
		s.trustFail(w, err)
		return
	}
	writeJSON(w, 200, list(v))
}

func (s *server) trustEvaluate(w http.ResponseWriter, r *http.Request) {
	v, err := s.Trust.Evaluate(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.trustFail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"methodology": trust.Describe(), "agent": v})
}

func (s *server) trustDecide(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Note string `json:"note"`
		}
		if r.ContentLength != 0 {
			if err := s.decode(w, r, &body); err != nil {
				s.trustFail(w, err)
				return
			}
		}
		id := chi.URLParam(r, "id")
		var p trust.Proposal
		var err error
		switch action {
		case "accept":
			p, err = s.Trust.Accept(r.Context(), id, body.Note)
		case "decline":
			p, err = s.Trust.Decline(r.Context(), id, body.Note)
		default:
			p, err = s.Trust.Revert(r.Context(), id, body.Note)
		}
		if err != nil {
			s.trustFail(w, err)
			return
		}
		writeJSON(w, 200, p)
	}
}
