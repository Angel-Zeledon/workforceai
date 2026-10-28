package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// mountInitiative registers the proactive suggestions (B2, docs/architecture/initiative.md):
//
//	GET  /initiatives?status=&limit=     requests:read    suggestions raised by agents, newest first
//	POST /initiatives/{id}/accept        requests:create  submits the suggestion as a normal request (202)
//	POST /initiatives/{id}/dismiss       requests:create  discards it
//	GET  /settings/initiative            requests:read    the initiative budget
//	PUT  /settings/initiative            org:manage       change the budget and the metric signals (owner/admin)
//	POST /initiatives/scan               org:manage       evaluate the metric signals now (optional route)
//
// Agents never call these: a suggestion is created by the platform from a signal and only a person accepts it,
// which goes through the same path as POST /requests. No-op when Deps.Initiative is nil.
func (s *server) mountInitiative(r chi.Router) {
	if s.Initiative == nil {
		return
	}
	r.With(s.can(auth.PermRequestsRead)).Get("/initiatives", s.listInitiatives)
	if s.InitiativeScanner != nil {
		r.With(s.can(auth.PermOrgManage)).Post("/initiatives/scan", s.scanInitiatives)
	}
	r.With(s.can(auth.PermRequestsCreate)).Post("/initiatives/{id}/accept", s.acceptInitiative)
	r.With(s.can(auth.PermRequestsCreate)).Post("/initiatives/{id}/dismiss", s.dismissInitiative)
	r.With(s.can(auth.PermRequestsRead)).Get("/settings/initiative", s.getInitiativeSettings)
	r.With(s.can(auth.PermOrgManage)).Put("/settings/initiative", s.putInitiativeSettings)
}

// scanInitiatives runs the metric signals of the caller's organization once and says what each candidate became.
// A source that fails does not hide the others: the results come back with 200 and the failure is logged.
func (s *server) scanInitiatives(w http.ResponseWriter, r *http.Request) {
	res, err := s.InitiativeScanner.Scan(r.Context())
	if res == nil {
		s.fail(w, err)
		return
	}
	if err != nil && s.Log != nil {
		s.Log.Warn("initiative scan: a signal source failed", "err", err)
	}
	raised := 0
	for _, x := range res {
		if x.Raised {
			raised++
		}
	}
	if s.Rec != nil {
		s.Rec.Audit(r.Context(), domain.AuditLog{Actor: s.actor(r), Action: "initiative.scanned",
			Entity: "initiative", EntityID: "scan", Details: map[string]any{"candidates": len(res), "raised": raised}})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res, "raised": raised})
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
	// "signals" is merged over the stored values, so a client may send only what it changes (or omit it: an
	// older client keeps the stored signals). The budget fields keep their full-replace meaning.
	cur, err := s.Initiative.Settings(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	in := application.InitiativeSettings{Signals: cur.Signals}
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
