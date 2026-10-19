package application

import (
	"context"

	"aiworkforce/backend/internal/domain"
)

// ReservePlanner holds budget for one project-planner call, sized from the REAL request payload plus the output
// cap of the call kind (plan, plan_phases, plan_phase, replan), like run_task (call_guard.go). A fixed 6k+6k
// reservation both under-reserved big plans (long goal, many agents) and over-reserved small ones. Planner calls
// belong to no request, so (like chat) the agent cap and the organization limit apply and the ledger is the
// source of truth. A non-nil Exceeded means the call must not run. The returned Reservation is nil-safe to Release.
func (o *Orchestrator) ReservePlanner(ctx context.Context, kind domain.UsageKind, payload any) (*Reservation, *Exceeded) {
	est := callEstimateUSD("", kind, payload)
	org := o.org(ctx)
	if o.cfg.BudgetUSD > 0 {
		if used, err := o.store.OrgCost(ctx, org); err == nil {
			spent := o.chatSpend(ctx)
			if used+spent+est > o.cfg.BudgetUSD+1e-9 {
				return nil, &Exceeded{Scope: domain.ScopeOrg, ScopeID: org, AgentID: assistantID, CapUSD: o.cfg.BudgetUSD, SpentUSD: used + spent, NeededUSD: used + spent + est}
			}
		}
	}
	res, ex, err := o.budget.Reserve(ctx, "", assistantID, est)
	if err != nil {
		o.log.Warn("planner budget check failed", "err", err)
		return nil, nil
	}
	return res, ex
}

// RecordPlannerUsage writes a planner call to the ledger (kind "plan", no
// request) and releases its reservation. Calls without usage record nothing.
func (o *Orchestrator) RecordPlannerUsage(ctx context.Context, u Usage, res *Reservation) {
	defer res.Release()
	if u.CostUSD <= 0 && u.InputTokens == 0 && u.OutputTokens == 0 {
		return
	}
	entry := domain.UsageEntry{AgentID: assistantID, Kind: domain.UsagePlan, Model: u.Model, InputTokens: u.InputTokens, CachedTokens: u.CachedTokens, OutputTokens: u.OutputTokens}
	cost, err := o.budget.Record(ctx, "", entry, u.CostUSD)
	if err != nil {
		o.log.Warn("record planner usage", "err", err)
	}
	u.CostUSD = cost
	o.rec.Audit(ctx, domain.AuditLog{Actor: assistantID, Action: "runtime.usage", Entity: "project_planner", EntityID: "plan", Details: u})
	o.emitMetrics(ctx)
}
