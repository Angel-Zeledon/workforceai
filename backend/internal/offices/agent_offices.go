package offices

import (
	"context"
	"fmt"
	"slices"

	"aiworkforce/backend/internal/domain"
)

// Agents in several offices (docs/superpowers/specs/2026-11-23-oficinas-agentes-habilidades-design.md §5).
//
// An agent has ONE base office (Model.Agent(id).OfficeID: desk, budget, boss, position) and may also work for
// others. The responsible of the base office offers it, the responsible of the other office accepts (an admin may do
// both at once). Once accepted, the members of the other office may ask it for work (Rights.Use). Personal agents do
// not change: they are their person's.
//
// The isolation of memory and files per request is NOT part of this step: until it exists, everything here is behind
// Config.MultiOffice (MULTI_OFFICE_AGENTS, off by default). Off: grants are refused and the stored rows are ignored
// by the model, so nobody gains access through them.

// Status of an extra office.
const (
	StatusPending  = "pending"
	StatusAccepted = "accepted"
)

// OfficeRef names an office.
type OfficeRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ExtraOffice is one office an agent also works for, as a viewer sees it.
type ExtraOffice struct {
	OfficeID   string `json:"office_id"`
	Name       string `json:"name"`
	Status     string `json:"status"` // pending | accepted
	GrantedBy  string `json:"granted_by"`
	AcceptedBy string `json:"accepted_by,omitempty"`
	CanAccept  bool   `json:"can_accept"` // pending and the viewer leads that office
	CanRemove  bool   `json:"can_remove"`
}

// AgentOfficesView is the single source every screen reads to say where an agent works: its base office (the same
// one the directory, the positions and the org chart use) and the other offices with their state.
type AgentOfficesView struct {
	AgentID    string        `json:"agent_id"`
	BaseOffice OfficeRef     `json:"base_office"`
	Offices    []ExtraOffice `json:"offices"`
	Enabled    bool          `json:"enabled"` // MULTI_OFFICE_AGENTS: false = the agent can only work for its base office
}

// setExtras loads the extra offices into the model. Rows of agents or offices that no longer exist, of personal
// agents and of the base office itself are ignored; everything is ignored while the feature is off.
func (m *Model) setExtras(rows []AgentOffice, enabled bool) {
	m.multi = enabled
	m.extras = map[string][]AgentOffice{}
	if !enabled {
		return
	}
	known := map[string]bool{}
	for _, a := range m.agents {
		known[a.ID] = true
	}
	for _, r := range rows {
		ea, ok := m.access[r.AgentID]
		if !ok || !known[r.AgentID] || ea.OwnerKind == OwnerPerson || r.OfficeID == ea.OfficeID {
			continue
		}
		if _, ok := m.byID[r.OfficeID]; !ok {
			continue
		}
		m.extras[r.AgentID] = append(m.extras[r.AgentID], r)
	}
	if len(m.extras) > 0 {
		m.restricted = true
	}
}

// MultiOffice reports whether agents may work for more than their base office.
func (m *Model) MultiOffice() bool { return m.multi }

// AgentOffices returns the other offices of an agent (pending and accepted), in order.
func (m *Model) AgentOffices(agentID string) []AgentOffice { return m.extras[agentID] }

// usesViaExtra reports whether the viewer is a member of an office that accepted the agent.
func (m *Model) usesViaExtra(v Viewer, agentID string) bool {
	for _, x := range m.extras[agentID] {
		if x.Accepted() && m.IsMember(v.UserID, x.OfficeID) {
			return true
		}
	}
	return false
}

func (m *Model) extra(agentID, office string) (AgentOffice, bool) {
	for _, x := range m.extras[agentID] {
		if x.OfficeID == office {
			return x, true
		}
	}
	return AgentOffice{}, false
}

// OfficesView is the base office and the other offices of an agent as the viewer may see them: an office the viewer
// cannot enter is not named (unless they lead the agent's base office).
func (m *Model) OfficesView(v Viewer, agentID string) AgentOfficesView {
	ea := m.Agent(agentID)
	view := AgentOfficesView{AgentID: agentID, Offices: []ExtraOffice{}, Enabled: m.multi}
	base, _ := m.Office(ea.OfficeID)
	if m.CanSeeWork(v, agentID) { // the base office of an agent of a private office is not named to who is not in it
		view.BaseOffice = OfficeRef{ID: base.ID, Name: base.Name}
	}
	baseLead := m.CanManage(v, ea.OfficeID)
	for _, x := range m.extras[agentID] {
		if !m.CanEnter(v, x.OfficeID) && !baseLead {
			continue
		}
		o, _ := m.Office(x.OfficeID)
		e := ExtraOffice{OfficeID: x.OfficeID, Name: o.Name, Status: StatusPending, GrantedBy: x.GrantedBy, AcceptedBy: x.AcceptedBy}
		if x.Accepted() {
			e.Status = StatusAccepted
		}
		e.CanAccept = !x.Accepted() && m.CanManage(v, x.OfficeID)
		e.CanRemove = baseLead || m.CanManage(v, x.OfficeID)
		view.Offices = append(view.Offices, e)
	}
	return view
}

// agentOfficeTarget loads the model and checks the agent exists and that the viewer may know about it: an agent of a
// private office answers "not found" to whoever cannot see its work.
func (s *Service) agentOfficeTarget(ctx context.Context, org string, v Viewer, agentID string) (*Model, error) {
	m, err := s.fresh(ctx, org)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(m.Agents(), func(a AgentRef) bool { return a.ID == agentID }) || !m.Rights(v, agentID).See {
		return nil, fmt.Errorf("%w: agent %s", domain.ErrNotFound, agentID)
	}
	return m, nil
}

// AgentOffices is the base office and the other offices of an agent for the viewer.
func (s *Service) AgentOffices(ctx context.Context, org string, v Viewer, agentID string) (AgentOfficesView, error) {
	m, err := s.agentOfficeTarget(ctx, org, v, agentID)
	if err != nil {
		return AgentOfficesView{}, err
	}
	return m.OfficesView(v, agentID), nil
}

// GrantOffice offers an agent to another office: who leads its base office (or an admin) does it. It stays pending
// until the responsible of that office accepts, except when the same person leads both (an admin): then it is
// accepted at once. Granting twice changes nothing.
func (s *Service) GrantOffice(ctx context.Context, org string, v Viewer, agentID, officeID string) (AgentOfficesView, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	m, err := s.agentOfficeTarget(ctx, org, v, agentID)
	if err != nil {
		return AgentOfficesView{}, err
	}
	if !m.multi {
		return AgentOfficesView{}, forbidden("agents working for more than one office are not enabled yet")
	}
	ea := m.Agent(agentID)
	if ea.OwnerKind == OwnerPerson {
		return AgentOfficesView{}, invalid("a personal agent works for its person only")
	}
	if _, ok := m.Office(officeID); !ok {
		return AgentOfficesView{}, fmt.Errorf("%w: office %s", domain.ErrNotFound, officeID)
	}
	if officeID == ea.OfficeID {
		return AgentOfficesView{}, invalid("that is already the base office of the agent")
	}
	if !m.CanManage(v, ea.OfficeID) {
		return AgentOfficesView{}, forbidden("only who leads the agent's base office or an admin offers it to another office")
	}
	now := s.now()
	cur, exists := m.extra(agentID, officeID)
	if !exists {
		cur = AgentOffice{AgentID: agentID, OfficeID: officeID, GrantedBy: v.UserID, CreatedAt: now}
	}
	changed := !exists
	if !cur.Accepted() && m.CanManage(v, officeID) {
		cur.AcceptedBy, cur.AcceptedAt, changed = v.UserID, &now, true
	}
	if changed {
		if err := s.cfg.Store.PutAgentOffice(ctx, org, cur); err != nil {
			return AgentOfficesView{}, err
		}
		if !exists {
			s.audit(ctx, org, v, "agent.office_granted", agentID, map[string]any{"office_id": officeID, "base_office": ea.OfficeID})
		}
		if cur.Accepted() {
			s.audit(ctx, org, v, "agent.office_accepted", agentID, map[string]any{"office_id": officeID})
		}
	}
	return s.afterOffices(ctx, org, v, agentID)
}

// AcceptOffice accepts an agent offered to an office: who leads that office (or an admin). Accepting twice changes
// nothing.
func (s *Service) AcceptOffice(ctx context.Context, org string, v Viewer, agentID, officeID string) (AgentOfficesView, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	m, err := s.agentOfficeTarget(ctx, org, v, agentID)
	if err != nil {
		return AgentOfficesView{}, err
	}
	if !m.multi {
		return AgentOfficesView{}, forbidden("agents working for more than one office are not enabled yet")
	}
	cur, ok := m.extra(agentID, officeID)
	if !ok {
		return AgentOfficesView{}, fmt.Errorf("%w: agent %s was not offered to office %s", domain.ErrNotFound, agentID, officeID)
	}
	if !m.CanManage(v, officeID) {
		return AgentOfficesView{}, forbidden("only who leads that office or an admin accepts an agent")
	}
	if !cur.Accepted() {
		now := s.now()
		cur.AcceptedBy, cur.AcceptedAt = v.UserID, &now
		if err := s.cfg.Store.PutAgentOffice(ctx, org, cur); err != nil {
			return AgentOfficesView{}, err
		}
		s.audit(ctx, org, v, "agent.office_accepted", agentID, map[string]any{"office_id": officeID})
	}
	return s.afterOffices(ctx, org, v, agentID)
}

// RemoveOffice takes an agent out of another office (or declines the offer): who leads the base office, who leads
// that office, or an admin. The access of that office's members ends at once. Removing what is not there changes
// nothing. It works with the feature off too, so a stored grant can always be cleaned up.
func (s *Service) RemoveOffice(ctx context.Context, org string, v Viewer, agentID, officeID string) (AgentOfficesView, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	m, err := s.agentOfficeTarget(ctx, org, v, agentID)
	if err != nil {
		return AgentOfficesView{}, err
	}
	ea := m.Agent(agentID)
	if _, ok := m.Office(officeID); !ok {
		return AgentOfficesView{}, fmt.Errorf("%w: office %s", domain.ErrNotFound, officeID)
	}
	if !m.CanManage(v, ea.OfficeID) && !m.CanManage(v, officeID) {
		return AgentOfficesView{}, forbidden("only who leads the agent's base office, who leads that office or an admin removes it")
	}
	rows, err := s.cfg.Store.ListAgentOffices(ctx, org)
	if err != nil {
		return AgentOfficesView{}, err
	}
	if slices.ContainsFunc(rows, func(r AgentOffice) bool { return r.AgentID == agentID && r.OfficeID == officeID }) {
		if err := s.cfg.Store.DeleteAgentOffice(ctx, org, agentID, officeID); err != nil {
			return AgentOfficesView{}, err
		}
		s.audit(ctx, org, v, "agent.office_removed", agentID, map[string]any{"office_id": officeID})
	}
	return s.afterOffices(ctx, org, v, agentID)
}

func (s *Service) afterOffices(ctx context.Context, org string, v Viewer, agentID string) (AgentOfficesView, error) {
	m, err := s.fresh(ctx, org)
	if err != nil {
		return AgentOfficesView{}, err
	}
	return m.OfficesView(v, agentID), nil
}
