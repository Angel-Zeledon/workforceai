package trust

import (
	"context"
	"fmt"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/roles"
)

// ChooseLevel applies a level a person picked in the agent's "Permissions" section (approval permissions,
// docs/architecture/06-permisos-autonomia.md section 11). It goes through setLevel, so the ladder's guarantees hold:
// never above the role's ceiling, and raising waits while the kill switch, read-only mode or a pause is on. Who may
// choose is checked by the caller (application.PermissionsService); agents and system principals never can. The
// evidence of the ladder starts again at the new level and a pending proposal is superseded.
func (s *Service) ChooseLevel(ctx context.Context, agentID, to, actor string) error {
	org := s.org(ctx)
	if isAgentPrincipal(actor, agentID) {
		return fmt.Errorf("%w: only a person can choose an agent's level", domain.ErrForbidden)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.cfg.Agents.GetAgent(ctx, org, agentID)
	if err != nil {
		return err
	}
	from := a.Autonomy
	if from == to {
		return nil
	}
	if Rank(to) > Rank(from) {
		if Rank(to) > Rank(roles.AutonomyCeiling(a.Role)) {
			return fmt.Errorf("%w: %s is above the %s ceiling of this role", domain.ErrForbidden, to, roles.AutonomyCeiling(a.Role))
		}
		if err := s.blocked(ctx, org, a.ID); err != nil {
			return err
		}
	}
	now := s.now()
	if err := s.setLevel(ctx, org, a, to, true, now); err != nil {
		return err
	}
	if err := s.supersedePending(ctx, org, a.ID, "level chosen by a person in the agent's permissions", now); err != nil {
		s.cfg.Log.Warn("trust: supersede pending proposals", "agent", a.ID, "err", err)
	}
	dir := "down"
	if Rank(to) > Rank(from) {
		dir = "up"
	}
	s.audit(ctx, org, domain.AuditLog{Actor: actor, Action: "agent.autonomy_changed", Entity: "agent", EntityID: a.ID,
		Details: map[string]any{"from": from, "to": to, "direction": dir, "source": "permissions.manual"}})
	s.emit(ctx, org, application.Action{Type: domain.EvAutonomyChanged, AgentID: a.ID, Entity: "agent", EntityID: a.ID,
		Payload: map[string]any{"agent_id": a.ID, "from": from, "to": to, "direction": dir, "source": "permissions.manual"}})
	return nil
}
