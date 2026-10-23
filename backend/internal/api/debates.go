package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
)

// mountDebates registers the team debates (B5, docs/architecture/team-debates.md):
//
//	GET  /debates?request_id=&limit=   requests:read     newest first (failed attempts included, with fail_code)
//	GET  /debates/{id}                 requests:read     one debate with its transcript
//	POST /debates                      requests:create   hold a debate now; body {"topic", "details"?, "options"?, "agent_ids"?, "locale"?}
//
// A debate is advisory and its transcript is untrusted model output: nothing here approves, rejects or executes
// anything. When it cannot happen the answer is 503/423/402 with a stable "code" (unavailable, blocked,
// budget_exceeded) and the failed attempt's "debate_id". No-op when Deps.Debates is nil.
func (s *server) mountDebates(r chi.Router) {
	if s.Debates == nil {
		return
	}
	r.With(s.can(auth.PermRequestsRead)).Get("/debates", s.listDebates)
	r.With(s.can(auth.PermRequestsRead)).Get("/debates/{id}", s.getDebate)
	r.With(s.can(auth.PermRequestsCreate)).Post("/debates", s.createDebate)
}

func (s *server) listDebates(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	v, err := s.Debates.List(r.Context(), r.URL.Query().Get("request_id"), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list(v))
}

func (s *server) getDebate(w http.ResponseWriter, r *http.Request) {
	d, err := s.Debates.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) createDebate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Topic    string   `json:"topic"`
		Details  string   `json:"details"`
		Options  []string `json:"options"`
		AgentIDs []string `json:"agent_ids"`
		Locale   string   `json:"locale"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	d, err := s.Debates.Run(r.Context(), application.DebateInput{Topic: body.Topic, Details: body.Details, Options: body.Options,
		AgentIDs: body.AgentIDs, Locale: body.Locale})
	var de *application.DebateError
	if errors.As(err, &de) {
		status := http.StatusServiceUnavailable
		switch de.Code {
		case application.DebateBlocked:
			status = http.StatusLocked
		case application.DebateOverBudget:
			status = http.StatusPaymentRequired
		}
		// The cause stays in the audit trail and the logs; the client gets the stable code and the failed row.
		s.Log.Info("debate unavailable", "code", de.Code, "err", de.Err)
		writeJSON(w, status, map[string]any{"code": de.Code, "error": "the team debate is not available right now", "debate_id": de.Debate.ID})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}
