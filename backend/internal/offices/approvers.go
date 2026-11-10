package offices

import (
	"context"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
)

// ApprovalPolicy is the D3 rule of who decides an approval (equipos-oficinas.md §4.2), application.ApproverPolicy:
// the approvals of an agent of an office (not General) go to the responsibles of that office (the admins when it has
// none) who see its work; an agent of General keeps today's rules. Whoever does not see the work of the agent
// (CanSeeWork) does not see its approvals. In an organization without offices or rules nothing changes.
type ApprovalPolicy struct {
	Svc *Service
	// Roles (optional) returns the organization role of each person. When an approval needs a minimum role (an amount
	// tier, a role per action), a responsible below it does not count: without one who reaches it the approval keeps
	// today's rules (the admins with that role who see the work). Without Roles every responsible counts.
	Roles func(ctx context.Context, org string) (map[string]string, error)
}

func (p ApprovalPolicy) model(ctx context.Context, org string) (*Model, error) {
	if p.Svc == nil {
		return nil, nil
	}
	m, err := p.Svc.Model(ctx, org)
	if err != nil || !m.Restricted() {
		return nil, err
	}
	return m, nil
}

// Sees reports whether the person sees the approval: the work of its agent (fails closed).
func (p ApprovalPolicy) Sees(ctx context.Context, org, userID string, a domain.Approval) bool {
	m, err := p.model(ctx, org)
	if err != nil {
		return false
	}
	return m == nil || m.CanSeeWork(Viewer{UserID: userID}, a.AgentID)
}

// Approvers returns the responsibles who decide the approval: the leads of its agent's office who see the work and
// reach its minimum role. None: today's rules.
func (p ApprovalPolicy) Approvers(ctx context.Context, org string, a domain.Approval) []string {
	m, err := p.model(ctx, org)
	if err != nil || m == nil {
		return nil
	}
	out, _ := p.approvers(ctx, org, m, a)
	return out
}

func (p ApprovalPolicy) approvers(ctx context.Context, org string, m *Model, a domain.Approval) ([]string, error) {
	o, ok := m.Office(m.Agent(a.AgentID).OfficeID)
	if !ok || o.General {
		return nil, nil
	}
	var roles map[string]string
	if a.RequiredRole != "" && p.Roles != nil {
		r, err := p.Roles(ctx, org)
		if err != nil {
			return nil, err
		}
		roles = r
	}
	var out []string
	for _, u := range o.Leads {
		if !m.CanSeeWork(Viewer{UserID: u}, a.AgentID) {
			continue
		}
		if roles != nil && policy.RoleRank(roles[u]) < policy.RoleRank(a.RequiredRole) {
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// CanDecide reports whether the office rule lets the person decide the approval: they see it, and when its office
// has responsibles who may decide it they are one of them.
func (p ApprovalPolicy) CanDecide(ctx context.Context, org, userID string, a domain.Approval) (bool, error) {
	m, err := p.model(ctx, org)
	if err != nil || m == nil {
		return err == nil, err
	}
	if !m.CanSeeWork(Viewer{UserID: userID}, a.AgentID) {
		return false, nil
	}
	leads, err := p.approvers(ctx, org, m, a)
	if err != nil {
		return false, err
	}
	if len(leads) == 0 {
		return true, nil
	}
	for _, u := range leads {
		if u == userID {
			return true, nil
		}
	}
	return false, nil
}
