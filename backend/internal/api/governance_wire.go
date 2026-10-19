package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/governance"
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

func (b publicBackend) org(ctx context.Context) string { return application.OrgFrom(ctx, b.s.Cfg.OrgID) }

func (b publicBackend) Agents(ctx context.Context) (any, error) {
	v, err := b.s.Queries.Agents(ctx)
	return list(v), err
}
func (b publicBackend) Requests(ctx context.Context) (any, error) {
	v, err := b.s.Store.ListRequests(ctx, b.org(ctx))
	return list(v), err
}
func (b publicBackend) Request(ctx context.Context, id string) (any, error) {
	d, err := b.s.Queries.RequestDetail(ctx, id)
	if err != nil {
		return nil, err
	}
	d.Tasks = list(d.Tasks)
	return d, nil
}
func (b publicBackend) Submit(ctx context.Context, text string, capUSD float64) (string, error) {
	return b.s.Orch.Submit(application.WithBudgetCap(ctx, capUSD), text)
}
func (b publicBackend) Reports(ctx context.Context) (any, error) {
	v, err := b.s.Store.ListReports(ctx, b.org(ctx))
	return list(v), err
}
func (b publicBackend) Report(ctx context.Context, id string) (any, error) {
	return b.s.Store.GetReport(ctx, b.org(ctx), id)
}
