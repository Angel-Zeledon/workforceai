package trust

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Observer wraps the event publisher: after an approval is resolved, a task ends or an agent is hired, it
// re-evaluates that agent in the background. Policy denials only reach the audit trail (no event), so they are
// picked up at the agent's next evaluation (the task that hit them ends right after). The ladder's own events
// (agent.autonomy_changed, trust.proposal) do not trigger anything, so there is no loop.
type Observer struct {
	Inner application.Publisher
	svc   atomic.Pointer[Service]
}

// Bind installs the service (late binding: the Recorder the service emits through publishes through this
// observer).
func (o *Observer) Bind(s *Service) { o.svc.Store(s) }

// Triggers are the event types that re-evaluate an agent.
var Triggers = map[string]bool{
	domain.EvApprovalResolved: true, domain.EvTaskFailed: true, domain.EvTaskCompleted: true, domain.EvAgentCreated: true,
}

// Publish implements application.Publisher.
func (o *Observer) Publish(ctx context.Context, e domain.Event) error {
	err := o.Inner.Publish(ctx, e)
	svc := o.svc.Load()
	if svc == nil || !Triggers[e.Type] || e.AgentID == "" || e.OrgID == "" {
		return err
	}
	org, agent := e.OrgID, e.AgentID
	go func() {
		c, cancel := context.WithTimeout(application.WithOrg(context.WithoutCancel(ctx), org), 30*time.Second)
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				svc.cfg.Log.Error("trust: panic while evaluating", "panic", r)
			}
		}()
		if _, err := svc.Evaluate(c, agent); err != nil && !errors.Is(err, domain.ErrNotFound) {
			svc.cfg.Log.Warn("trust: evaluate", "agent", agent, "err", err)
		}
	}()
	return err
}
