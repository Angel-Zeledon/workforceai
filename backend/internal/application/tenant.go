package application

import "context"

type (
	orgKey     struct{}
	actorKey   struct{}
	roleKey    struct{}
	requestKey struct{}
)

// WithOrg scopes ctx to a tenant. Every use case reads the organization from
// the context (set by the API from the authenticated principal, never from the
// client) and falls back to the configured default org (the fixed `demo` org
// when authentication is disabled). Background work started by the
// orchestrator inherits the org of the request that spawned it.
func WithOrg(ctx context.Context, orgID string) context.Context {
	return context.WithValue(ctx, orgKey{}, orgID)
}

// OrgFrom returns the tenant of ctx, or def when none was set.
func OrgFrom(ctx context.Context, def string) string {
	if id, ok := ctx.Value(orgKey{}).(string); ok && id != "" {
		return id
	}
	return def
}

// WithActor records who performs an action (user id) for audit logs.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFrom returns the actor of ctx or def.
func ActorFrom(ctx context.Context, def string) string {
	if a, ok := ctx.Value(actorKey{}).(string); ok && a != "" {
		return a
	}
	return def
}

func (o *Orchestrator) org(ctx context.Context) string { return OrgFrom(ctx, o.cfg.OrgID) }
func (a *Approvals) org(ctx context.Context) string    { return OrgFrom(ctx, a.cfg.OrgID) }
func (q *Queries) org(ctx context.Context) string      { return OrgFrom(ctx, q.Cfg.OrgID) }
func (r *Recorder) org(ctx context.Context) string     { return OrgFrom(ctx, r.OrgID) }

// WithActorRole records the organization role of the acting user (set by the
// API from the verified token). Approvals use it to enforce the minimum role
// of the approver.
func WithActorRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, roleKey{}, role)
}

// ActorRoleFrom returns the role of the acting user or def.
func ActorRoleFrom(ctx context.Context, def string) string {
	if r, ok := ctx.Value(roleKey{}).(string); ok && r != "" {
		return r
	}
	return def
}

// WithRequestID tags everything done under ctx with the request it belongs to,
// so that audit entries can be filtered by request.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestKey{}, id)
}

// RequestIDFrom returns the request id of ctx ("" when none).
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestKey{}).(string)
	return id
}
