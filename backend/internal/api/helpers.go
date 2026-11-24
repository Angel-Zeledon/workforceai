package api

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// Temporary helpers (docs/superpowers/specs/2026-11-23-ayudantes-temporales-design.md, docs/architecture/08-api.md):
//
//	POST /requests/{id}/helpers          {count, cap_usd}  accept the proposal
//	POST /requests/{id}/helpers/decline  {}                the request follows its normal plan
//	POST /requests/{id}/helpers/stop     {part?}           stop one part (or everything that has not finished)
//	POST /requests/{id}/helpers/retry    {part}            run a failed or stopped part again
//
// They need conversations:write (a viewer cannot) AND the right to ask work of the responsible employee (the same
// rule as the chat and the assignments: the offices Rights.Use). Another organization's request, a request whose
// work the person does not see and a request without a group all read as 404, and so does everything while
// HELPERS_ENABLED is off.

func (s *server) mountHelpers(r chi.Router) {
	w := s.can(auth.PermConversationsPost)
	r.With(w).Post("/requests/{id}/helpers", s.helpersAccept)
	r.With(w).Post("/requests/{id}/helpers/decline", s.helpersDecline)
	r.With(w).Post("/requests/{id}/helpers/stop", s.helpersStop)
	r.With(w).Post("/requests/{id}/helpers/retry", s.helpersRetry)
}

// helperGroupFor returns the group of the request of the URL after every check; ok=false: the answer was written.
func (s *server) helperGroupFor(w http.ResponseWriter, r *http.Request) (g domain.HelperGroup, ok bool) {
	id := chi.URLParam(r, "id")
	if s.Orch == nil || !s.Orch.HelpersEnabled() {
		s.fail(w, notFound("request", id))
		return g, false
	}
	ws := s.work(r)
	if !ws.request(id) { // D3: a request whose work the person does not see does not exist for them
		s.fail(w, cmp.Or(ws.err, notFound("request", id)))
		return g, false
	}
	g, err := s.Orch.HelperProgress(r.Context(), id) // another organization's request is not found here
	if err != nil {
		s.fail(w, err)
		return g, false
	}
	if !ws.use(g.OwnerAgentID) { // the rule of the chat: whoever may not ask the responsible for work does not steer it
		s.fail(w, errAgentNotUsable)
		return g, false
	}
	return g, true
}

type helperGroupOut struct {
	Group domain.HelperGroup `json:"group"`
}

func (s *server) helpersAccept(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Count  int     `json:"count"`
		CapUSD float64 `json:"cap_usd"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if _, ok := s.helperGroupFor(w, r); !ok {
		return
	}
	g, err := s.Orch.AcceptHelpers(r.Context(), chi.URLParam(r, "id"), body.Count, body.CapUSD, application.ActorFrom(r.Context(), "user"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, helperGroupOut{g})
}

func (s *server) helpersDecline(w http.ResponseWriter, r *http.Request) {
	if err := s.decodeOptional(w, r, &struct{}{}); err != nil {
		s.fail(w, err)
		return
	}
	if _, ok := s.helperGroupFor(w, r); !ok {
		return
	}
	g, err := s.Orch.DeclineHelpers(r.Context(), chi.URLParam(r, "id"), application.ActorFrom(r.Context(), "user"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, helperGroupOut{g})
}

func (s *server) helpersStop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Part *int `json:"part"`
	}
	if err := s.decodeOptional(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	part := 0
	if body.Part != nil {
		part = *body.Part
	}
	if part < 0 {
		s.fail(w, errInvalid("part must be 1 or more (omit it to stop everything)"))
		return
	}
	cur, ok := s.helperGroupFor(w, r)
	if !ok {
		return
	}
	if cur.Status == domain.HelperProposed || cur.Status == domain.HelperDeclined {
		s.fail(w, fmt.Errorf("%w: the helpers have not been launched", domain.ErrConflict))
		return
	}
	g, err := s.Orch.StopHelpers(r.Context(), cur.ID, part, application.ActorFrom(r.Context(), "user"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, helperGroupOut{g})
}

func (s *server) helpersRetry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Part int `json:"part"`
	}
	if err := s.decode(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	if body.Part < 1 {
		s.fail(w, errInvalid("part is required (1 or more)"))
		return
	}
	cur, ok := s.helperGroupFor(w, r)
	if !ok {
		return
	}
	if cur.Status == domain.HelperProposed || cur.Status == domain.HelperDeclined {
		s.fail(w, fmt.Errorf("%w: the helpers have not been launched", domain.ErrConflict))
		return
	}
	g, err := s.Orch.RetryHelperPart(r.Context(), cur.ID, body.Part, application.ActorFrom(r.Context(), "user"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, helperGroupOut{g})
}

// decodeOptional is decode for a body that may be empty ({} or nothing at all).
func (s *server) decodeOptional(w http.ResponseWriter, r *http.Request, dst any) error {
	if err := s.decode(w, r, dst); err != nil && !errors.Is(err, io.EOF) {
		var bad *badRequestError
		if errors.As(err, &bad) && errors.Is(bad.cause, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

// helperGroupView is the group of a request for GET /requests/{id} (nil without one or with the flag off). The caller
// already passed the visibility of the request.
func (s *server) helperGroupView(r *http.Request, requestID string) *domain.HelperGroup {
	if s.Orch == nil || !s.Orch.HelpersEnabled() {
		return nil
	}
	g, err := s.Orch.HelperProgress(r.Context(), requestID)
	if err != nil {
		return nil
	}
	return &g
}

// helperGroupsForHello are the groups a client needs when it connects: proposed and in progress, of the requests
// whose work this person sees.
func (s *server) helperGroupsForHello(r *http.Request) []domain.HelperGroup {
	if s.Orch == nil || !s.Orch.HelpersEnabled() {
		return nil
	}
	ws := s.work(r)
	var out []domain.HelperGroup
	for _, g := range s.Orch.HelperGroups(r.Context()) {
		if ws.request(g.RequestID) {
			out = append(out, g)
		}
	}
	return out
}
