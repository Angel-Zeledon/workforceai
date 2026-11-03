package offices

import (
	"context"
	"fmt"
	"slices"

	"aiworkforce/backend/internal/domain"
)

// MaxShares caps the people an agent is shared with one by one (beyond that, the office or the directory).
const MaxShares = 50

// AccessInput changes the owner, the visibility or the shares of an agent. Empty fields keep the current value;
// Shares nil keeps them, an empty list removes them.
type AccessInput struct {
	OwnerKind  string  `json:"owner_kind"`
	OwnerID    string  `json:"owner_id"`
	Visibility string  `json:"visibility"`
	Shares     []Share `json:"shares"`
}

func validVis(v string) bool {
	return v == VisPrivate || v == VisOffice || v == VisPeople || v == VisDirectory
}

// SetAgentAccess changes who owns an agent and who may see or use it. The viewer must be able to edit the agent;
// a person who is not an admin may only give it to themself, to a member of an office they lead, or to one of
// their offices.
func (s *Service) SetAgentAccess(ctx context.Context, org string, v Viewer, agentID string, in AccessInput) (EffectiveAccess, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	m, err := s.fresh(ctx, org)
	if err != nil {
		return EffectiveAccess{}, err
	}
	if !slices.ContainsFunc(m.Agents(), func(a AgentRef) bool { return a.ID == agentID }) {
		return EffectiveAccess{}, fmt.Errorf("%w: agent %s", domain.ErrNotFound, agentID)
	}
	cur := m.Agent(agentID)
	if !m.Rights(v, agentID).Edit {
		return EffectiveAccess{}, forbidden("only the agent's owner, its office's responsible or an admin changes who uses it")
	}
	next := AgentAccess{AgentID: agentID, OwnerKind: cur.OwnerKind, OwnerID: cur.OwnerID, Visibility: cur.Visibility,
		Shares: cur.Shares, UpdatedBy: v.UserID, UpdatedAt: s.now()}
	if row, ok := m.rows[agentID]; ok && row.OwnerKind == OwnerPerson && row.OwnerID == next.OwnerID {
		next.HomeOffice = row.HomeOffice
	}
	if in.Visibility != "" {
		if !validVis(in.Visibility) {
			return EffectiveAccess{}, invalid("visibility must be private, office, people or directory")
		}
		next.Visibility = in.Visibility
	}
	if in.OwnerKind != "" || in.OwnerID != "" {
		if err := s.checkOwner(m, v, cur, in.OwnerKind, in.OwnerID); err != nil {
			return EffectiveAccess{}, err
		}
		if in.OwnerKind != next.OwnerKind || in.OwnerID != next.OwnerID {
			next.HomeOffice = ""
		}
		next.OwnerKind, next.OwnerID = in.OwnerKind, in.OwnerID
	}
	if next.OwnerKind == OwnerPerson && next.HomeOffice == "" {
		next.HomeOffice = homeOffice(m, next.OwnerID, cur.OfficeID)
	}
	if in.Shares != nil {
		shares, err := cleanShares(m, in.Shares)
		if err != nil {
			return EffectiveAccess{}, err
		}
		next.Shares = shares
	}
	if err := s.cfg.Store.PutAgentAccess(ctx, org, next); err != nil {
		return EffectiveAccess{}, err
	}
	s.audit(ctx, org, v, "agent.access_changed", agentID, map[string]any{"owner_kind": next.OwnerKind, "owner_id": next.OwnerID,
		"visibility": next.Visibility, "shares": len(next.Shares), "from_owner": cur.OwnerID, "from_visibility": cur.Visibility})
	after, err := s.fresh(ctx, org)
	if err != nil {
		return EffectiveAccess{}, err
	}
	return after.Agent(agentID), nil
}

func (s *Service) checkOwner(m *Model, v Viewer, cur EffectiveAccess, kind, id string) error {
	switch kind {
	case OwnerPerson:
		if _, ok := m.Person(id); !ok {
			return invalid("%s is not a member of the organization", id)
		}
		if v.Admin || id == v.UserID {
			return nil
		}
		// the responsible of the agent's office may give it to one of its members
		if cur.OwnerKind == OwnerOffice && m.IsLead(v.UserID, cur.OfficeID) && m.IsMember(id, cur.OfficeID) {
			return nil
		}
		return forbidden("an agent can only be given to yourself or to a member of an office you lead")
	case OwnerOffice:
		if _, ok := m.Office(id); !ok {
			return invalid("office %s does not exist", id)
		}
		if v.Admin || m.IsMember(v.UserID, id) {
			return nil
		}
		return forbidden("an agent can only be given to an office you belong to")
	}
	return invalid("owner_kind must be person or office")
}

// homeOffice is the office a personal agent returns to if its owner leaves: the agent's current office when the
// owner belongs to it, else the owner's first office other than General, else General.
func homeOffice(m *Model, owner, current string) string {
	if current != "" && current != GeneralID && m.IsMember(owner, current) {
		return current
	}
	for _, o := range m.OfficesOf(owner) {
		if o != GeneralID {
			return o
		}
	}
	return GeneralID
}

func cleanShares(m *Model, in []Share) ([]Share, error) {
	if len(in) > MaxShares {
		return nil, invalid("an agent is shared with at most %d people one by one", MaxShares)
	}
	out := make([]Share, 0, len(in))
	seen := map[string]bool{}
	for _, sh := range in {
		if sh.Level == "" {
			sh.Level = ShareChat
		}
		if sh.Level != ShareChat && sh.Level != ShareEdit {
			return nil, invalid("share level must be chat or edit")
		}
		if _, ok := m.Person(sh.UserID); !ok {
			return nil, invalid("%s is not a member of the organization", sh.UserID)
		}
		if seen[sh.UserID] {
			continue
		}
		seen[sh.UserID] = true
		out = append(out, sh)
	}
	return out, nil
}
