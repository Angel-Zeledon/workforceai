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

// hook marks a context whose changes are recorded by the caller when it finishes (the HTTP middleware of the API).
// While it is open, the Observer hands the agents its events name to the caller instead of recording them itself, so
// a request that changes several things leaves one version per agent, with the cause of the event (a hire, or the
// system demoting an agent on its own inside the request). Once the caller closed it, work that outlives the request
// (the trust ladder evaluates in the background) is recorded by the Observer. Opening, handing over and closing share
// one lock: no event falls between the caller's last look and the close.
type hook struct {
	mu      sync.Mutex
	open    bool
	pending map[string]Cause // agent id -> the cause of its last event
}

type hookKey struct{}

// WithHook marks ctx as recorded by the caller. The returned func closes it and returns the agents that events
// named meanwhile (with their cause); the caller records them.
func WithHook(ctx context.Context) (context.Context, func() map[string]Cause) {
	h := &hook{open: true, pending: map[string]Cause{}}
	return context.WithValue(ctx, hookKey{}, h), func() map[string]Cause {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.open = false
		out := h.pending
		h.pending = map[string]Cause{}
		return out
	}
}

// Hooked reports whether a caller will record the changes made under ctx.
func Hooked(ctx context.Context) bool {
	h, ok := ctx.Value(hookKey{}).(*hook)
	if !ok {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.open
}

// handOver gives the agent to the caller of an open hook (false when there is none or it is closed). A change the
// system made on its own is never overwritten by a later event of the same agent.
func handOver(ctx context.Context, agentID string, c Cause) bool {
	h, ok := ctx.Value(hookKey{}).(*hook)
	if !ok {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.open {
		return false
	}
	if prev, had := h.pending[agentID]; !had || prev.Actor != ActorSystem {
		h.pending[agentID] = c
	}
	return true
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
	if handOver(ctx, agent, cause) {
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
