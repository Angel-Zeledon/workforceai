package webhooks

import (
	"context"

	"aiworkforce/backend/internal/domain"
)

// Inner is the event publisher being decorated (application.Publisher).
type Inner interface {
	Publish(ctx context.Context, e domain.Event) error
}

// Publisher forwards every event to Inner and, for the three public ones (request.done, approval.requested,
// report.created), also hands it to the webhook outbox. It never fails or slows the inner publisher.
type Publisher struct {
	Inner Inner
	Svc   *Service
}

// Publish implements application.Publisher.
func (p Publisher) Publish(ctx context.Context, e domain.Event) error {
	var err error
	if p.Inner != nil {
		err = p.Inner.Publish(ctx, e)
	}
	if p.Svc != nil && e.OrgID != "" {
		if name, ok := MapEvent(e.Type); ok {
			p.Svc.EnqueueAsync(e.OrgID, name, e.Payload)
		}
	}
	return err
}
