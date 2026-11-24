package offices

import (
	"context"
	"fmt"

	"aiworkforce/backend/internal/domain"
)

// Hiring for an office (design 2026-11-23 §3): the checks that run before the agent exists, so a hire that cannot be
// completed leaves nothing behind.

// CheckHire validates the office an agent is hired for and the access it starts with. "" means General. Whoever may
// not manage that office (its responsible, or an admin) is refused with ErrForbidden; an unknown office is not found;
// the visibility and the shares are checked like SetAgentAccess does.
func (s *Service) CheckHire(ctx context.Context, org string, v Viewer, officeID string, in AccessInput) (string, error) {
	m, err := s.fresh(ctx, org)
	if err != nil {
		return "", err
	}
	if officeID == "" {
		officeID = GeneralID
	}
	o, ok := m.Office(officeID)
	if !ok || o.Visibility == Private && !m.IsMember(v.UserID, officeID) {
		// a private office answers as if it did not exist to whoever is not in it (administrators included, D3)
		return "", fmt.Errorf("%w: office %s", domain.ErrNotFound, officeID)
	}
	if !m.CanManage(v, officeID) {
		return "", forbidden("only who leads the office or an admin hires agents for it")
	}
	if in.Visibility != "" && !validVis(in.Visibility) {
		return "", invalid("visibility must be private, office, people or directory")
	}
	if in.Shares != nil {
		if _, err := cleanShares(m, in.Shares); err != nil {
			return "", err
		}
	}
	return officeID, nil
}

// PlaceNewAgent gives a just hired agent to the office with the visibility and shares asked for. The office owns it.
func (s *Service) PlaceNewAgent(ctx context.Context, org string, v Viewer, agentID, officeID string, in AccessInput) (EffectiveAccess, error) {
	in.OwnerKind, in.OwnerID = OwnerOffice, officeID
	return s.SetAgentAccess(ctx, org, v, agentID, in)
}

// ForgetAgent drops the access row and the extra offices of an agent that was never completed (a hire that failed
// half way). It audits nothing: the agent never existed for anybody.
func (s *Service) ForgetAgent(ctx context.Context, org, agentID string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	defer s.invalidate(org)
	if err := s.cfg.Store.DeleteAgentAccess(ctx, org, agentID); err != nil {
		return err
	}
	rows, err := s.cfg.Store.ListAgentOffices(ctx, org)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.AgentID == agentID {
			if err := s.cfg.Store.DeleteAgentOffice(ctx, org, agentID, r.OfficeID); err != nil {
				return err
			}
		}
	}
	return nil
}
