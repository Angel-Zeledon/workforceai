package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// mountDevilsAdvocate registers the advisory review of high-risk approvals (B8):
//
//	GET  /approvals/{id}/counterargument   approvals:read    the cached review (404 not_generated before the first one)
//	POST /approvals/{id}/counterargument   approvals:decide  generate it (once) or return the cached one; body {"locale"?}
//
// The review is a second opinion for the person who decides: nothing here approves, rejects or blocks. When it
// cannot be produced the answer is 503/423/402 with a stable "code" (unavailable, blocked, budget_exceeded) and
// the approval keeps working. No-op when Deps.DevilsAdvocate is nil.
func (s *server) mountDevilsAdvocate(r chi.Router) {
	if s.DevilsAdvocate == nil {
		return
	}
	r.With(s.can(auth.PermApprovalsRead)).Get("/approvals/{id}/counterargument", s.getCounterargument)
	r.With(s.can(auth.PermApprovalsDecide)).Post("/approvals/{id}/counterargument", s.makeCounterargument)
}

func (s *server) getCounterargument(w http.ResponseWriter, r *http.Request) {
	c, err := s.DevilsAdvocate.Get(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, domain.ErrNotFound) {
		codeErr(w, http.StatusNotFound, "not_generated", "no review has been generated for this approval")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *server) makeCounterargument(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Locale string `json:"locale"`
	}
	if err := s.decode(w, r, &body); err != nil && !errors.Is(err, io.EOF) {
		s.fail(w, err)
		return
	}
	c, err := s.DevilsAdvocate.Generate(r.Context(), chi.URLParam(r, "id"), body.Locale)
	var ce *application.CounterargumentError
	if errors.As(err, &ce) {
		status := http.StatusServiceUnavailable
		switch ce.Code {
		case application.CounterargumentBlocked:
			status = http.StatusLocked
		case application.CounterargumentOverBudget:
			status = http.StatusPaymentRequired
		}
		// The cause stays in the audit trail and the logs; the client only gets the stable code.
		s.Log.Info("counterargument unavailable", "approval", chi.URLParam(r, "id"), "code", ce.Code, "err", ce.Err)
		codeErr(w, status, ce.Code, "the devil's advocate review is not available right now; you can still decide this approval")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}
