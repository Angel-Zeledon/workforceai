package positions

import (
	"context"
	"errors"

	"aiworkforce/backend/internal/domain"
)

// Check validates, before anything is created, a position for an agent about to be hired in officeID: the actor
// manages that department's positions and the position belongs to it. It returns the position (its skills are what
// the hire suggests).
func (s *Service) Check(ctx context.Context, a Actor, officeID, positionID string) (Position, error) {
	org := s.org(ctx)
	if _, _, err := s.manage(ctx, org, a, officeID); err != nil {
		return Position{}, err
	}
	p, err := s.cfg.Store.GetPosition(ctx, org, positionID)
	if errors.Is(err, domain.ErrNotFound) {
		return Position{}, notFound("position not found")
	}
	if err != nil {
		return Position{}, err
	}
	if p.OfficeID != officeID {
		return Position{}, invalid("position_other_office", "an agent can only hold a position of its own department")
	}
	return p, nil
}
