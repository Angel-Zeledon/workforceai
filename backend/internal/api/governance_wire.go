package api

import (
	"cmp"
	"context"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/governance"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/webhooks"
)

// GovernanceDeps are the collaborators of the governance endpoints (internal/governance). Optional: when
// Deps.Governance is nil nothing is mounted. See docs/architecture/governance.md.
type GovernanceDeps struct {
	Tokens    *governance.TokenService
	Admin     governance.AdminStore
	Reports   governance.ReportStore
	Retention governance.RetentionStore
	Purger    *governance.Purger
	Webhooks  *webhooks.Service
	Audit     governance.AuditSource // nil when the audit trail is not configured
	// OnLimitChange (optional) runs after a department limit changed.
	OnLimitChange func()
}

// governanceHandler adapts the router to the governance package (tenancy, permissions, audit).
func (s *server) governanceHandler() *governance.Handler {
	g := s.Governance
	h := &governance.Handler{
		Tokens: g.Tokens, Admin: g.Admin, Reports: g.Reports, Retention: g.Retention, Purger: g.Purger, Webhooks: g.Webhooks, Audit: g.Audit, OnLimitChange: g.OnLimitChange,
		Env: governance.Env{
			Org:    s.org,
			Actor:  func(r *http.Request) string { return application.ActorFrom(r.Context(), "user") },
			Decode: s.decode,
			Fail:   s.fail,
			Audit: func(ctx context.Context, org, actor, action, entity, id string, details map[string]any) {
				if s.Rec != nil {
					s.Rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: entity, EntityID: id, Details: details})
				}
			},
			Admin:     s.requireRole(auth.RoleAdmin),
			Owner:     s.requireRole(auth.RoleOwner),
			Metrics:   s.can(auth.PermMetricsRead),
			AuditRead: s.can(auth.PermAuditRead),
			WithOrg: func(ctx context.Context, org, actor string) context.Context {
				return application.WithActor(application.WithOrg(ctx, org), actor)
			},
			SeesAllPeople: func(r *http.Request) bool {
				if !s.AuthEnabled {
					return true
				}
				p, ok := auth.PrincipalFrom(r.Context())
				return ok && p.Role.Rank() >= auth.RoleAdmin.Rank()
			},
			Person: func(r *http.Request) string { return application.PersonFrom(r.Context()) },
		},
		Policies: func(ctx context.Context, org string) (map[string]any, error) {
			out := map[string]any{}
			if s.OrgConfig != nil {
				if v, err := s.OrgConfig.Rules(ctx); err == nil {
					out["approval_rules"] = v
				}
			}
			if s.ModelPolicy != nil {
				if v, err := s.ModelPolicy.Get(ctx); err == nil {
					out["model_policy"] = v
				}
			}
			return out, nil
		},
	}
	return h
}

// mountGovernance registers /governance/* (admin console, reports, retention, tokens, webhooks).
func (s *server) mountGovernance(r chi.Router) {
	if s.Governance == nil {
		return
	}
	s.governanceHandler().Mount(r)
}

// mountPublicAPI registers the token-authenticated API at /api/public/v1. It sits outside the user-session
// group on purpose: bearer tokens are not JWTs and carry their own scopes and organization.
func (s *server) mountPublicAPI(r chi.Router) {
	if s.Governance == nil || s.Governance.Tokens == nil {
		return
	}
	r.Mount("/api/public/v1", s.governanceHandler().PublicRouter(publicBackend{s}))
}

// publicBackend serves the public API with the same services as the web app, under the organization bound to
// the context by the token middleware.
type publicBackend struct{ s *server }

func (b publicBackend) org(ctx context.Context) string {
	return application.OrgFrom(ctx, b.s.Cfg.OrgID)
}

// scope (D3) is the work rule of the public API. A token is the organization's: it records who created it, not a
// person it acts for, so no department's membership comes with it. It reads like an administrator outside every
// private department: the work of their agents is left out (their activity blanked, their requests and reports gone)
// and the rest, requests still being planned included, stays. An unreadable offices model fails closed.
func (b publicBackend) scope(ctx context.Context) *workScope {
	ws := &workScope{s: b.s, ctx: ctx, org: b.org(ctx), loaded: true, v: offices.Viewer{Admin: true},
		agents: map[string][]string{}, seen: map[string]bool{}}
	if b.s.Offices == nil || !b.s.AuthEnabled {
		return ws
	}
	m, err := b.s.Offices.Model(ctx, ws.org)
	switch {
	case err != nil:
		ws.err = err
	case m.Restricted():
		ws.m = m
	}
	return ws
}

func (b publicBackend) Agents(ctx context.Context) (any, error) {
	v, err := b.s.Queries.Agents(ctx)
	if err != nil {
		return nil, err
	}
	ws := b.scope(ctx)
	for i := range v {
		if !ws.agent(v[i].ID) {
			v[i] = hideActivity(v[i])
		}
	}
	return list(v), ws.err
}
func (b publicBackend) Requests(ctx context.Context) (any, error) {
	v, err := b.s.Store.ListRequests(ctx, b.org(ctx))
	if err != nil {
		return nil, err
	}
	ws := b.scope(ctx)
	if err := ws.indexAll(); err != nil {
		return nil, err
	}
	v = slices.DeleteFunc(v, func(x domain.Request) bool { return !ws.request(x.ID) })
	return list(v), ws.err
}
func (b publicBackend) Request(ctx context.Context, id string) (any, error) {
	d, err := b.s.Queries.RequestDetail(ctx, id)
	if err != nil {
		return nil, err
	}
	ws := b.scope(ctx)
	if ws.index(d.Tasks); !ws.request(d.ID) {
		return nil, cmp.Or(ws.err, notFound("request", d.ID))
	}
	d.Tasks = list(d.Tasks)
	return d, nil
}
func (b publicBackend) Submit(ctx context.Context, text string, capUSD float64) (string, error) {
	return b.s.Orch.Submit(application.WithBudgetCap(ctx, capUSD), text)
}
func (b publicBackend) Reports(ctx context.Context) (any, error) {
	v, err := b.s.Store.ListReports(ctx, b.org(ctx))
	if err != nil {
		return nil, err
	}
	ws := b.scope(ctx)
	if err := ws.indexAll(); err != nil {
		return nil, err
	}
	v = slices.DeleteFunc(v, func(x domain.Report) bool { return !ws.request(x.RequestID) })
	return list(v), ws.err
}
func (b publicBackend) Report(ctx context.Context, id string) (any, error) {
	v, err := b.s.Store.GetReport(ctx, b.org(ctx), id)
	if err != nil {
		return nil, err
	}
	if ws := b.scope(ctx); !ws.request(v.RequestID) {
		return nil, cmp.Or(ws.err, notFound("report", v.ID))
	}
	return v, nil
}
