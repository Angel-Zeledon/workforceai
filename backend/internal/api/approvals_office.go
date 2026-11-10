package api

import (
	"cmp"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// The approvals of a department (D3, equipos-oficinas.md §4.2): an approval of an agent of an office goes to the
// responsibles of that office (application.ApproverPolicy, installed on the orchestrator's approvals); whoever does
// not see the agent's work does not see its approvals (left out of the listings, 404 by id, like the rest of the
// work filter).

// visibleApprovals leaves out the approvals of the agents whose work the person does not see.
func (s *server) visibleApprovals(r *http.Request, aps []domain.Approval) ([]domain.Approval, error) {
	ws := s.work(r)
	if err := ws.load(); err != nil || ws.open() {
		return aps, err
	}
	out := aps[:0:0]
	for _, ap := range aps {
		if ws.agent(ap.AgentID) {
			out = append(out, ap)
		}
	}
	return out, nil
}

// workApproval guards the routes about the approval {id}: a hidden one answers like a missing one (a missing one is
// left to the handler).
func (s *server) workApproval(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, id := s.work(r), chi.URLParam(r, "id")
		if err := ws.load(); err != nil {
			s.fail(w, err)
			return
		}
		if !ws.open() {
			ap, err := s.Store.GetApproval(r.Context(), ws.org, id)
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				s.fail(w, err)
				return
			}
			if err == nil && !ws.agent(ap.AgentID) {
				s.fail(w, cmp.Or(ws.err, notFound("approval", id)))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// canDecide guards deciding the approval {id}: approvals:decide, or being one of the responsibles of its agent's
// office (they decide their office's approvals whatever their role). Approvals.Decide applies every other rule.
func (s *server) canDecide(next http.Handler) http.Handler {
	if !s.AuthEnabled {
		return next
	}
	perm := auth.RequirePermission(auth.PermApprovalsDecide)(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth.PrincipalFrom(r.Context())
		if ok && !p.Can(auth.PermApprovalsDecide) && s.Approvals.Policy() != nil {
			if ap, err := s.Store.GetApproval(r.Context(), s.org(r), chi.URLParam(r, "id")); err == nil && s.Approvals.Responsible(r.Context(), p.UserID, ap) {
				next.ServeHTTP(w, r)
				return
			}
		}
		perm.ServeHTTP(w, r)
	})
}

// inboxOffices adds the office rule to the inbox: what the viewer may decide, and who decides the approvals of an
// office (by name).
func (s *server) inboxOffices(r *http.Request, in *application.InboxInput) {
	pol := s.Approvals.Policy()
	if pol == nil || !s.AuthEnabled || in.Viewer == "" {
		return
	}
	ctx, org := r.Context(), s.org(r)
	viewer, role, hasDecide := in.Viewer, in.ViewerRole, in.ViewerCanDecide
	in.Decides = func(ap domain.Approval) bool {
		return application.DecidesApproval(ctx, pol, org, ap, viewer, role, hasDecide)
	}
	m, _, err := s.agentModel(r)
	in.Approvers = func(ap domain.Approval) []string {
		ids := pol.Approvers(ctx, org, ap)
		names := make([]string, 0, len(ids))
		for _, id := range ids {
			name := id
			if err == nil && m != nil {
				if p, ok := m.Person(id); ok && p.Name != "" {
					name = p.Name
				}
			}
			names = append(names, name)
		}
		if len(names) == 0 {
			return nil
		}
		return names
	}
}
