package api

import (
	"cmp"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/projects"
)

// The approvals of a department (D3, equipos-oficinas.md §4.2): an approval of an agent of an office goes to the
// responsibles of that office (application.ApproverPolicy, installed on the orchestrator's approvals); whoever does
// not see the agent's work does not see its approvals (left out of the listings, 404 by id, like the rest of the
// work filter), except the person who asked for the action: they see it, they do not decide it.

// seesApproval reports whether the person sees an approval: the work of its agent, or they asked for the action.
func (ws *workScope) seesApproval(ap domain.Approval) bool {
	if ws.agent(ap.AgentID) {
		return true
	}
	return ws.err == nil && ws.v.UserID != "" && ap.RequestedBy == ws.v.UserID
}

// visibleApprovals leaves out the approvals the person does not see.
func (s *server) visibleApprovals(r *http.Request, aps []domain.Approval) ([]domain.Approval, error) {
	ws := s.work(r)
	if err := ws.load(); err != nil || ws.open() {
		return aps, err
	}
	out := aps[:0:0]
	for _, ap := range aps {
		if ws.seesApproval(ap) {
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
			if err == nil && !ws.seesApproval(ap) {
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

// decidesApproval guards what only who decides the approval {id} may do besides deciding it ("approve and do not ask
// again", the devil's advocate review): the office rule (ApproverPolicy.CanDecide) and, on top, approvals:decide or
// being one of the responsibles of its agent's office. Without a policy, approvals:decide as before. A missing
// approval is left to the handler (after approvals:decide).
func (s *server) decidesApproval(next http.Handler) http.Handler {
	if !s.AuthEnabled {
		return next
	}
	perm := auth.RequirePermission(auth.PermApprovalsDecide)(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pol := s.Approvals.Policy()
		p, ok := auth.PrincipalFrom(r.Context())
		if pol == nil || !ok {
			perm.ServeHTTP(w, r)
			return
		}
		ctx, org := r.Context(), s.org(r)
		ap, err := s.Store.GetApproval(ctx, org, chi.URLParam(r, "id"))
		if errors.Is(err, domain.ErrNotFound) {
			perm.ServeHTTP(w, r)
			return
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		allowed, err := pol.CanDecide(ctx, org, p.UserID, ap)
		if err != nil {
			s.fail(w, err)
			return
		}
		if !allowed || !p.Can(auth.PermApprovalsDecide) && !s.Approvals.Responsible(ctx, p.UserID, ap) {
			codeErr(w, http.StatusForbidden, "forbidden", "the responsible of the agent's office decides this approval")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// outboxGuard checks the outbox entry {id} before it is edited, approved or rejected, and answers when it refuses:
// an entry of an agent whose work the person does not see answers 404; deciding it needs approvals:decide or being
// a responsible of the agent's office, and the office rule (approving: every rule of Approvals.Decide, so nothing is
// scheduled for a decision that would be refused). A missing entry is left to the handler.
func (s *server) outboxGuard(w http.ResponseWriter, r *http.Request, approve bool) bool {
	if !s.AuthEnabled {
		return true
	}
	ctx, org, id := r.Context(), s.org(r), chi.URLParam(r, "id")
	it, err := s.Gateway.OutboxEntry(org, id)
	if err != nil {
		return true
	}
	ws := s.work(r)
	if err := ws.load(); err != nil {
		s.fail(w, err)
		return false
	}
	ap := domain.Approval{ID: it.ApprovalID, AgentID: it.AgentID, Status: domain.ApprovalPending, RequiredApprovals: 1}
	if it.ApprovalID != "" {
		got, err := s.Store.GetApproval(ctx, org, it.ApprovalID)
		switch {
		case err == nil:
			ap = got
		case !errors.Is(err, domain.ErrNotFound): // fails closed: the rules cannot be told
			s.fail(w, err)
			return false
		}
	}
	if !ws.seesApproval(ap) {
		s.fail(w, notFound("outbox item", id))
		return false
	}
	p, _ := auth.PrincipalFrom(ctx)
	hasDecide := p.Can(auth.PermApprovalsDecide)
	allowed := hasDecide || s.Approvals.Responsible(ctx, p.UserID, ap)
	if pol := s.Approvals.Policy(); allowed && pol != nil {
		ok, err := pol.CanDecide(ctx, org, p.UserID, ap)
		if err != nil {
			s.fail(w, err)
			return false
		}
		allowed = ok
	}
	if allowed && approve && ap.Status == domain.ApprovalPending {
		allowed = application.DecidesApproval(ctx, s.Approvals.Policy(), org, ap, p.UserID, string(p.Role), hasDecide)
	}
	if !allowed {
		codeErr(w, http.StatusForbidden, "forbidden", "the responsible of the agent's office decides this e-mail")
		return false
	}
	return true
}

// decider returns, for the person of the request, whether they may decide an approval now and the names of the
// responsibles who decide it when it belongs to an office (nil without authentication: the client keeps its role).
func (s *server) decider(r *http.Request) func(ap domain.Approval) (bool, []string) {
	if !s.AuthEnabled {
		return nil
	}
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		return nil
	}
	ctx, org, pol := offices.Once(r.Context()), s.org(r), s.Approvals.Policy() // one read of the rule for the whole list
	names := s.approverNames(r)
	return func(ap domain.Approval) (bool, []string) {
		can := application.DecidesApproval(ctx, pol, org, ap, p.UserID, string(p.Role), p.Can(auth.PermApprovalsDecide))
		if pol == nil {
			return can, nil
		}
		return can, names(pol.Approvers(ctx, org, ap))
	}
}

// approverNames turns user ids into the names of the people (the id when unknown).
func (s *server) approverNames(r *http.Request) func(ids []string) []string {
	m, _, err := s.agentModel(r)
	return func(ids []string) []string {
		if len(ids) == 0 {
			return nil
		}
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			name := id
			if err == nil && m != nil {
				if p, ok := m.Person(id); ok && p.Name != "" {
					name = p.Name
				}
			}
			out = append(out, name)
		}
		return out
	}
}

// listedApproval is an approval of GET /approvals with what the person may do with it (D3).
type listedApproval struct {
	domain.Approval
	CanDecide *bool    `json:"can_decide,omitempty"`
	Approvers []string `json:"approvers,omitempty"`
}

// withDeciders adds can_decide and approvers to a listing (unchanged without authentication).
func (s *server) withDeciders(r *http.Request, aps []domain.Approval) any {
	dec := s.decider(r)
	if dec == nil {
		return list(aps)
	}
	out := make([]listedApproval, 0, len(aps))
	for _, ap := range aps {
		it := listedApproval{Approval: ap}
		if ap.Status == domain.ApprovalPending {
			can, names := dec(ap)
			it.CanDecide, it.Approvers = &can, names
		}
		out = append(out, it)
	}
	return out
}

// projectDeciders sets can_decide and approvers on the pending approvals of a project.
func (s *server) projectDeciders(r *http.Request, aps []projects.Approval) {
	dec := s.decider(r)
	if dec == nil {
		return
	}
	for i := range aps {
		if aps[i].Status != string(domain.ApprovalPending) {
			continue
		}
		ap, err := s.Store.GetApproval(r.Context(), s.org(r), aps[i].ID)
		if err != nil {
			continue
		}
		can, names := dec(ap)
		aps[i].CanDecide, aps[i].Approvers = &can, names
	}
}

// inboxOffices adds the office rule to the inbox: what the viewer may decide, and who decides the approvals of an
// office (by name).
func (s *server) inboxOffices(r *http.Request, in *application.InboxInput) {
	pol := s.Approvals.Policy()
	if pol == nil || !s.AuthEnabled || in.Viewer == "" {
		return
	}
	ctx, org := offices.Once(r.Context()), s.org(r) // one read of the rule for the whole inbox
	viewer, role, hasDecide := in.Viewer, in.ViewerRole, in.ViewerCanDecide
	in.Decides = func(ap domain.Approval) bool {
		return application.DecidesApproval(ctx, pol, org, ap, viewer, role, hasDecide)
	}
	names := s.approverNames(r)
	in.Approvers = func(ap domain.Approval) []string { return names(pol.Approvers(ctx, org, ap)) }
}

// awayDecides is the office rule for "decide now" of the away summary (nil: the role rules).
func (s *server) awayDecides(r *http.Request, userID, role string, hasDecide bool) func(domain.Approval) bool {
	pol := s.Approvals.Policy()
	if pol == nil || !s.AuthEnabled {
		return nil
	}
	ctx, org := offices.Once(r.Context()), s.org(r)
	return func(ap domain.Approval) bool {
		return application.DecidesApproval(ctx, pol, org, ap, userID, role, hasDecide)
	}
}
