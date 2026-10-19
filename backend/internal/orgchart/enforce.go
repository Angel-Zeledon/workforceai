package orgchart

import (
	"context"
	"time"

	"aiworkforce/backend/internal/application"
)

// Delegation enforcement port: the orchestrator (internal/application, delegation_hook.go) asks
// Service.Checker() on every assignment of work. Lookups are cached per organization in a snapshot that
// every change of the model drops (Service.invalidate), so the hot path does no store reads.

const snapshotTTL = 15 * time.Second // safety net for changes made behind the service's back

type snapshot struct {
	at    time.Time
	rules []Rule
	dir   Directory
	dept  map[string]string // "kind:id" -> department id
}

func (s *Service) snapshotOf(ctx context.Context, org string) (snapshot, error) {
	s.mu.Lock()
	sn, ok := s.snaps[org]
	s.mu.Unlock()
	if ok && s.now().Sub(sn.at) < snapshotTTL {
		return sn, nil
	}
	rules, err := s.Store.ListRules(ctx, org)
	if err != nil {
		return snapshot{}, err
	}
	dir, dept, err := s.directory(application.WithOrg(ctx, org))
	if err != nil {
		return snapshot{}, err
	}
	sn = snapshot{at: s.now(), rules: rules, dir: dir, dept: dept}
	s.mu.Lock()
	if s.snaps == nil {
		s.snaps = map[string]snapshot{}
	}
	s.snaps[org] = sn
	s.mu.Unlock()
	return sn, nil
}

type delegationChecker struct{ s *Service }

// Checker returns the application.DelegationChecker backed by this service.
func (s *Service) Checker() application.DelegationChecker { return delegationChecker{s} }

// CheckDelegation implements application.DelegationChecker. A lookup error never blocks work: the
// answer is "allowed" (the organization behaves as if it had no rules).
func (c delegationChecker) CheckDelegation(ctx context.Context, org string, from application.DelegationActor, toAgentID string) application.DelegationVerdict {
	sn, err := c.s.snapshotOf(ctx, org)
	if err != nil {
		return application.DelegationVerdict{Allowed: true, Reason: "no_rule"}
	}
	f := Ref{Kind: Kind(from.Kind), ID: from.ID}
	t := Ref{Kind: KindAgent, ID: toAgentID}
	d := Check(sn.rules, Actor{Kind: f.Kind, ID: f.ID, DepartmentID: sn.dept[f.Key()]},
		Actor{Kind: t.Kind, ID: t.ID, DepartmentID: sn.dept[t.Key()]}, sn.dir)
	return application.DelegationVerdict{Allowed: d.Allowed, NeedsApproval: d.NeedsApproval,
		ApproverKind: string(d.Approver.Kind), ApproverID: d.Approver.ID, RuleID: d.RuleID, Reason: d.Reason}
}
