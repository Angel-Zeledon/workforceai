package offices

import (
	"context"
	"sync"

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
	// TaskOffice (optional) returns the office the request of a task was asked from ("" = none or unknown). An approval
	// belongs to that office when it has one (a General agent working for a private office asks that office), else to
	// the base office of its agent. An error means unknown: the caller fails closed.
	TaskOffice func(ctx context.Context, org, taskID string) (string, error)
}

// officeOf is the office an approval belongs to: the one its task's request was asked from, else its agent's base.
func (p ApprovalPolicy) officeOf(ctx context.Context, org string, m *Model, a domain.Approval) (string, error) {
	if p.TaskOffice != nil && a.TaskID != "" {
		office, err := p.TaskOffice(ctx, org, a.TaskID)
		if err != nil {
			return "", err
		}
		if _, ok := m.Office(office); ok && office != "" {
			return office, nil
		}
	}
	return m.Agent(a.AgentID).OfficeID, nil
}

// sees reports whether the viewer sees the work of the approval: the work of its agent and of its office.
func (p ApprovalPolicy) sees(ctx context.Context, org string, m *Model, v Viewer, a domain.Approval) bool {
	office, err := p.officeOf(ctx, org, m, a)
	return err == nil && m.CanSeeWork(v, a.AgentID) && m.CanSeeOfficeWork(v, office)
}

// onceKey carries the reads of one listing (Once).
type onceKey struct{}

type onceReads struct {
	mu     sync.Mutex
	models map[string]onceRead[*Model]
	roles  map[string]onceRead[map[string]string]
}

type onceRead[T any] struct {
	v   T
	err error
}

// Once returns a context in which the approval policy reads the model and the roles of an organization once: a
// listing asks it about many approvals of one request (the inbox, the away summary, can_decide of a list) and each
// question would read them again (the roles are a database query). Only for the life of one request.
func Once(ctx context.Context) context.Context {
	if _, ok := ctx.Value(onceKey{}).(*onceReads); ok {
		return ctx
	}
	return context.WithValue(ctx, onceKey{}, &onceReads{models: map[string]onceRead[*Model]{}, roles: map[string]onceRead[map[string]string]{}})
}

// remembered returns what read gives for key, read once per Once context (every time without one).
func remembered[T any](ctx context.Context, cache func(*onceReads) map[string]onceRead[T], key string, read func() (T, error)) (T, error) {
	o, ok := ctx.Value(onceKey{}).(*onceReads)
	if !ok {
		return read()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if r, ok := cache(o)[key]; ok {
		return r.v, r.err
	}
	v, err := read()
	cache(o)[key] = onceRead[T]{v, err}
	return v, err
}

func (p ApprovalPolicy) model(ctx context.Context, org string) (*Model, error) {
	if p.Svc == nil {
		return nil, nil
	}
	m, err := remembered(ctx, func(o *onceReads) map[string]onceRead[*Model] { return o.models }, org, func() (*Model, error) {
		return p.Svc.Model(ctx, org)
	})
	if err != nil || !m.Restricted() {
		return nil, err
	}
	return m, nil
}

func (p ApprovalPolicy) roles(ctx context.Context, org string) (map[string]string, error) {
	return remembered(ctx, func(o *onceReads) map[string]onceRead[map[string]string] { return o.roles }, org, func() (map[string]string, error) {
		return p.Roles(ctx, org)
	})
}

// Sees reports whether the person sees the approval: the work of its agent, or they asked for the action (they see
// it, they do not decide it). Fails closed.
func (p ApprovalPolicy) Sees(ctx context.Context, org, userID string, a domain.Approval) bool {
	m, err := p.model(ctx, org)
	if err != nil {
		return false
	}
	return m == nil || p.sees(ctx, org, m, Viewer{UserID: userID}, a) || userID != "" && a.RequestedBy == userID
}

// Audience says who an approval is for: the office of its agent (ok false for General, or without offices), and the
// responsibles who decide it (none: today's rules). An error means it cannot be told (the caller fails closed).
func (p ApprovalPolicy) Audience(ctx context.Context, org string, a domain.Approval) (office Office, ok bool, leads []string, err error) {
	m, err := p.model(ctx, org)
	if err != nil || m == nil {
		return Office{}, false, nil, err
	}
	officeID, err := p.officeOf(ctx, org, m, a)
	if err != nil {
		return Office{}, false, nil, err
	}
	o, found := m.Office(officeID)
	if !found || o.General {
		return Office{}, false, nil, nil
	}
	leads, err = p.approvers(ctx, org, m, a)
	return o, true, leads, err
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
	office, err := p.officeOf(ctx, org, m, a)
	if err != nil {
		return nil, err
	}
	o, ok := m.Office(office)
	if !ok || o.General {
		return nil, nil
	}
	var roles map[string]string
	if a.RequiredRole != "" && p.Roles != nil {
		r, err := p.roles(ctx, org)
		if err != nil {
			return nil, err
		}
		roles = r
	}
	var out []string
	for _, u := range o.Leads {
		if !p.sees(ctx, org, m, Viewer{UserID: u}, a) {
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
	if !p.sees(ctx, org, m, Viewer{UserID: userID}, a) {
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
