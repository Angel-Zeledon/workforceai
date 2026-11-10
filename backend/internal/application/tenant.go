package application

import (
	"context"
	"strings"
)

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

type channelKey struct{}

// WithChannel records the channel a human decision came through ("slack"; empty = the app). Approvals add it to
// their audit entries; it never changes what the actor may do.
func WithChannel(ctx context.Context, channel string) context.Context {
	return context.WithValue(ctx, channelKey{}, channel)
}

// ChannelFrom returns the channel of ctx ("" = the app).
func ChannelFrom(ctx context.Context) string {
	c, _ := ctx.Value(channelKey{}).(string)
	return c
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

type actorNameKey struct{}

// WithActorName records the display name of the acting person (E0: the author shown on their chat
// messages and given to the agent that answers). The API sets it from the directory.
func WithActorName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, actorNameKey{}, name)
}

// ActorNameFrom returns the display name of the acting person ("" when unknown).
func ActorNameFrom(ctx context.Context) string {
	n, _ := ctx.Value(actorNameKey{}).(string)
	return n
}

// PersonFrom returns the acting PERSON of ctx: the user id of an authenticated human, "" for the system,
// the scheduler, agents, signed links and deployments without authentication. It is what private chat
// threads, read markers and the cost ledger are keyed by.
func PersonFrom(ctx context.Context) string {
	return PersonID(ActorFrom(ctx, ""))
}

// IsSystemActor reports whether actor is the platform itself ("" included: nobody, the system, the scheduler, the
// orchestrator, the runtime, the demo, an unauthenticated "user"): work it starts is the agents' own. Every other
// actor is either a person (PersonID) or a non-person actor that acts for somebody (token:, link:, slack:, agent:...).
func IsSystemActor(actor string) bool {
	switch strings.TrimSpace(actor) {
	case "", "user", "system", "orchestrator", "scheduler", "demo", "runtime":
		return true
	}
	return false
}

// PersonID filters an actor string down to a person id ("" when the actor is not a person).
func PersonID(actor string) string {
	if IsSystemActor(actor) {
		return ""
	}
	if strings.ContainsAny(actor, ": ") {
		return "" // agent:<id>, link:<to>, slack:<id>, token:<id>...
	}
	return actor
}
