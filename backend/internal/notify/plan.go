package notify

import (
	"context"
	"math"
	"sync"
	"time"

	"aiworkforce/backend/internal/policy"
)

// Plan is the plan of one organization.
type Plan struct {
	OrgID         string     `json:"-"`
	Plan          string     `json:"plan"`
	CapUSD        float64    `json:"cap_usd"`
	CapNotifiedAt *time.Time `json:"cap_notified_at,omitempty"`
	// PeriodStart is the 1st of the month (UTC) of the current period; nil until PlanResetter has seen the plan.
	PeriodStart *time.Time `json:"period_start,omitempty"`
	// SpendBaseUSD is the lifetime spend of the organization when the period started: the plan only counts
	// lifetime - SpendBaseUSD (migration 785).
	SpendBaseUSD float64 `json:"-"`
}

// Hard reports whether the plan has a hard spend cap.
func (p Plan) Hard() bool { return p.Plan == policy.PlanFree }

// PlanStore persists explicit plans. No row = the deployment default.
type PlanStore interface {
	GetPlan(ctx context.Context, org string) (Plan, bool, error)
	PutPlan(ctx context.Context, p Plan, by string) error
	// MarkCapNotified records that the "cap reached" notice was sent at t.
	MarkCapNotified(ctx context.Context, org string, t time.Time) error
	// ResetPeriod starts the period at periodStart if the plan's period began earlier, recording lifetimeUSD as the
	// new base and clearing the "cap reached" mark. It is atomic: it returns true for exactly one caller per period.
	// A plan whose period was never set only gets its period_start (base and mark are kept, so nobody is unlocked
	// by the first run). A missing plan returns false.
	ResetPeriod(ctx context.Context, org string, periodStart time.Time, lifetimeUSD float64) (bool, error)
}

// Plans resolves the plan of an organization and answers the two questions the
// rest of the system asks: "what is your hard cap?" (the budget hook) and "what
// should the UI show?".
type Plans struct {
	Store PlanStore
	// DefaultFreeCapUSD > 0 makes every organization without an explicit plan a
	// free one with this hard cap (FREE_PLAN_CAP_USD). 0 = no default plan.
	DefaultFreeCapUSD float64
	// CacheTTL bounds how stale a cached plan may be (default 20 s). The budget
	// hook runs on every reservation, so it must not hit the database each time.
	CacheTTL time.Duration
	Now      func() time.Time

	mu    sync.Mutex
	cache map[string]cachedPlan
}

type cachedPlan struct {
	p  Plan
	at time.Time
}

func (s *Plans) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Resolve returns the plan in force for org. A store error fails CLOSED for a
// deployment with a default free plan (the cap applies), and open otherwise.
func (s *Plans) Resolve(ctx context.Context, org string) Plan {
	ttl := s.CacheTTL
	if ttl <= 0 {
		ttl = 20 * time.Second
	}
	s.mu.Lock()
	if c, ok := s.cache[org]; ok && s.now().Sub(c.at) < ttl {
		s.mu.Unlock()
		return c.p
	}
	s.mu.Unlock()
	p := Plan{OrgID: org, Plan: policy.PlanPaid}
	if s.DefaultFreeCapUSD > 0 {
		p = Plan{OrgID: org, Plan: policy.PlanFree, CapUSD: s.DefaultFreeCapUSD}
	}
	if s.Store != nil {
		if sp, ok, err := s.Store.GetPlan(ctx, org); err == nil && ok {
			p = sp
		}
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]cachedPlan{}
	}
	s.cache[org] = cachedPlan{p: p, at: s.now()}
	s.mu.Unlock()
	return p
}

// Invalidate drops the cached plan (after a change).
func (s *Plans) Invalidate(org string) {
	s.mu.Lock()
	delete(s.cache, org)
	s.mu.Unlock()
}

// OrgCap is the hook for application.Budget.OrgCapFor: the hard cap of a free
// plan, 0 for a plan without one.
func (s *Plans) OrgCap(ctx context.Context, org string) float64 {
	if s == nil {
		return 0
	}
	if p := s.Resolve(ctx, org); p.Hard() {
		if p.CapUSD <= 0 {
			return 1e-9 // a free plan with a broken cap blocks everything instead of allowing everything
		}
		// The budget compares this with the lifetime spend, so the allowance of the month sits on top of the base.
		return p.SpendBaseUSD + p.CapUSD
	}
	return 0
}

// Usage is what GET /plan/usage returns.
type Usage struct {
	policy.CapStatus
	// OperatorCapUSD is the organization-wide cap of the deployment (0 = none), shown for transparency.
	OperatorCapUSD float64 `json:"operator_cap_usd,omitempty"`
}

// Status builds the usage for the UI from the ledger numbers.
func (s *Plans) Status(ctx context.Context, org string, operatorCap, spent, reserved float64) Usage {
	p := s.Resolve(ctx, org)
	capUSD := policy.EffectiveCap(operatorCap, 0)
	base := 0.0
	if p.Hard() {
		// Both caps are lifetime numbers (the operator cap is not monthly); the plan's own is base + monthly cap.
		// The card shows the month: spent and cap are both moved down by the base.
		planCap := p.CapUSD
		if planCap > 0 {
			planCap += p.SpendBaseUSD
		}
		capUSD = policy.EffectiveCap(operatorCap, planCap)
		if capUSD > 0 {
			base = math.Min(p.SpendBaseUSD, spent)
		}
	}
	if base > 0 {
		capUSD = math.Max(capUSD-base, 0)
		if capUSD == 0 {
			capUSD = 1e-9 // the operator cap is already used up: still exhausted, never unlimited
		}
	}
	return Usage{CapStatus: policy.EvaluateCap(p.Plan, p.Hard(), capUSD, spent-base, reserved), OperatorCapUSD: operatorCap}
}

// MemPlans is the in-memory PlanStore.
type MemPlans struct {
	mu sync.Mutex
	m  map[string]Plan
}

// NewMemPlans returns an empty store.
func NewMemPlans() *MemPlans { return &MemPlans{m: map[string]Plan{}} }

func (m *MemPlans) GetPlan(_ context.Context, org string) (Plan, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.m[org]
	return p, ok, nil
}

func (m *MemPlans) PutPlan(_ context.Context, p Plan, _ string) error {
	m.mu.Lock()
	m.m[p.OrgID] = p
	m.mu.Unlock()
	return nil
}

func (m *MemPlans) ResetPeriod(_ context.Context, org string, start time.Time, lifetime float64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.m[org]
	if !ok || (p.PeriodStart != nil && !p.PeriodStart.Before(start)) {
		return false, nil
	}
	if p.PeriodStart != nil {
		p.SpendBaseUSD, p.CapNotifiedAt = lifetime, nil
	}
	p.PeriodStart = &start
	m.m[org] = p
	return true, nil
}

// StampDefaultPlan implements PlanStamper.
func (m *MemPlans) StampDefaultPlan(_ context.Context, org string, capUSD float64, start time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.m[org]; ok {
		return false, nil
	}
	m.m[org] = Plan{OrgID: org, Plan: policy.PlanFree, CapUSD: capUSD, PeriodStart: &start}
	return true, nil
}

func (m *MemPlans) MarkCapNotified(_ context.Context, org string, t time.Time) error {
	m.mu.Lock()
	if p, ok := m.m[org]; ok { // only an explicit plan keeps the mark; the default plan is deduplicated in memory
		p.CapNotifiedAt = &t
		m.m[org] = p
	}
	m.mu.Unlock()
	return nil
}
