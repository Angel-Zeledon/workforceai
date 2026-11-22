package agenthistory

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// hook marks a context whose changes are recorded by the caller when it finishes (the HTTP middleware of the API,
// a restore). While it is active the Observer leaves the events of that context alone, so a request that changes
// several things leaves one version. Work that outlives the request (the trust ladder evaluates in the background)
// finds it inactive and is recorded by the Observer.
type hook struct {
	active atomic.Bool
	mu     sync.Mutex
	system *Cause // a change the system made on its own inside the request (an automatic demotion)
}

type hookKey struct{}

// WithHook marks ctx as recorded by the caller; call the returned func when the caller has recorded.
func WithHook(ctx context.Context) (context.Context, func()) {
	h := &hook{}
	h.active.Store(true)
	return context.WithValue(ctx, hookKey{}, h), func() { h.active.Store(false) }
}

// Hooked reports whether a caller will record the changes made under ctx.
func Hooked(ctx context.Context) bool {
	h, ok := ctx.Value(hookKey{}).(*hook)
	return ok && h.active.Load()
}

// SystemCause is the change the system made on its own inside a hooked context (ok=false when none): the caller
// records it as the system's, not as the person's whose request set it off.
func SystemCause(ctx context.Context) (Cause, bool) {
	h, ok := ctx.Value(hookKey{}).(*hook)
	if !ok {
		return Cause{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.system == nil {
		return Cause{}, false
	}
	return *h.system, true
}

func noteSystem(ctx context.Context, c Cause) {
	if h, ok := ctx.Value(hookKey{}).(*hook); ok {
		h.mu.Lock()
		h.system = &c
		h.mu.Unlock()
	}
}

// Triggers are the events that may change an agent's configuration without a request that names it: hiring, the
// trust ladder (an automatic demotion after an incident, an accepted promotion), the controls and the work profile.
var Triggers = map[string]bool{
	domain.EvAgentCreated: true, domain.EvAutonomyChanged: true, "agent.control_changed": true, "agent.profile_changed": true,
}

// Observer wraps the event publisher and records the agent named by a trigger event in the background.
type Observer struct {
	Inner application.Publisher
	svc   atomic.Pointer[Service]
}

// Bind installs the service (late binding, like trust.Observer).
func (o *Observer) Bind(s *Service) { o.svc.Store(s) }

// CauseOf tells who and what produced the change an event announces.
func CauseOf(ctx context.Context, e domain.Event) Cause {
	src := e.Type
	if m, ok := e.Payload.(map[string]any); ok {
		if v, ok := m["source"].(string); ok && v != "" {
			if !strings.Contains(v, ".") {
				v = "trust." + v
			}
			src = v
		}
	}
	actor := application.ActorFrom(ctx, ActorSystem)
	if strings.HasPrefix(src, "trust.auto") { // the ladder demoted it on its own, whoever's work set it off
		actor = ActorSystem
	}
	c := Cause{Actor: actor, Source: src}
	if actorKind(actor) == ActorPerson {
		c.ActorName = application.ActorNameFrom(ctx)
	}
	return c
}

// Publish implements application.Publisher.
func (o *Observer) Publish(ctx context.Context, e domain.Event) error {
	err := o.Inner.Publish(ctx, e)
	svc := o.svc.Load()
	if svc == nil || !Triggers[e.Type] || e.AgentID == "" || e.OrgID == "" {
		return err
	}
	org, agent, cause := e.OrgID, e.AgentID, CauseOf(ctx, e)
	if Hooked(ctx) {
		if cause.Actor == ActorSystem {
			noteSystem(ctx, cause)
		}
		return err
	}
	c := application.WithOrg(context.WithoutCancel(ctx), org)
	svc.Go(func() {
		if _, _, rerr := svc.Record(c, org, agent, cause); rerr != nil && !errors.Is(rerr, domain.ErrNotFound) {
			svc.log().Warn("agent history: record", "agent", agent, "event", e.Type, "err", rerr)
		}
	})
	return err
}
