package api

import (
	"context"

	"aiworkforce/backend/internal/offices"
)

// agentOffice is the office an agent works in ("" = General, or an organization without offices to tell). A failing
// model is an error: the caller must not guess, or a private department's work would become company work.
func (s *server) agentOffice(ctx context.Context, org, agentID string) (string, error) {
	if s.Offices == nil || !s.AuthEnabled {
		return "", nil
	}
	m, err := s.Offices.Model(ctx, org)
	if err != nil {
		return "", err
	}
	if off := m.Agent(agentID).OfficeID; off != offices.GeneralID {
		return off, nil
	}
	return "", nil
}
