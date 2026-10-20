package notify

import (
	"context"
	"log/slog"
	"time"
)

// OrgLister lists organizations (application.OrgLister has this shape).
type OrgLister interface {
	ListOrgIDs(ctx context.Context) ([]string, error)
}

// PlanResetter makes the cap of a free plan monthly (docs/architecture/approvals-notify.md section 3, migration 785).
//
// The budget ledger only knows the lifetime spend of an organization. On the 1st of each month (UTC) the resetter
// records that lifetime spend as the plan's base (spend_base_usd) and moves period_start to the 1st; from then on the
// plan counts lifetime - base, so the cap starts again from zero. The UPDATE that moves period_start is the claim
// (PlanStore.ResetPeriod), so replicas, restarts and repeated sweeps never reset a month twice. A missed month is not
// a problem: the next sweep catches up (the base is the lifetime spend at that moment).
//
// An organization on the deployment default (FREE_PLAN_CAP_USD, no org_plans row) has nowhere to keep its base, so the
// first sweep creates its row (PlanStamper: same free plan and cap as the default, period_start = this month, base 0).
// Like the first stamp of an explicit plan, that grants nothing: the org keeps counting its lifetime spend until the
// next month, when the normal reset applies. The insert is ON CONFLICT DO NOTHING, so replicas and an admin PutPlan
// racing with it are safe. Note the row pins the cap in force when it was created.
type PlanResetter struct {
	Plans *Plans
	Orgs  OrgLister
	// Spend returns the lifetime spend of the organization (the ledger total).
	Spend func(ctx context.Context, org string) (float64, error)
	Now   func() time.Time
	// Audit (optional) records each reset.
	Audit func(ctx context.Context, org, action string, details map[string]any)
	Log   *slog.Logger
}

func (r *PlanResetter) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *PlanResetter) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// PeriodStart is the 1st of the month of t, at 00:00 UTC.
func PeriodStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// PlanStamper is the optional PlanStore capability to create the row of an organization that is on the default plan.
type PlanStamper interface {
	// StampDefaultPlan inserts a free plan with capUSD and period_start = periodStart (base 0) if org has no row.
	// It returns true for exactly one caller; an existing row is left untouched.
	StampDefaultPlan(ctx context.Context, org string, capUSD float64, periodStart time.Time) (bool, error)
}

// ResetReport says what one sweep did.
type ResetReport struct{ Checked, Reset, Stamped, Errors int }

// RunOnce sweeps every organization once.
func (r *PlanResetter) RunOnce(ctx context.Context) ResetReport {
	var rep ResetReport
	if r.Plans == nil || r.Plans.Store == nil || r.Orgs == nil || r.Spend == nil {
		return rep
	}
	ids, err := r.Orgs.ListOrgIDs(ctx)
	if err != nil {
		r.log().Warn("plan reset: list organizations", "err", err)
		rep.Errors++
		return rep
	}
	start := PeriodStart(r.now())
	for _, org := range ids {
		if ctx.Err() != nil {
			return rep
		}
		pl, ok, err := r.Plans.Store.GetPlan(ctx, org)
		if err != nil {
			rep.Errors++
			continue
		}
		if !ok {
			// default plan: create the row so the next months can be reset
			st, canStamp := r.Plans.Store.(PlanStamper)
			if !canStamp || r.Plans.DefaultFreeCapUSD <= 0 {
				continue
			}
			done, err := st.StampDefaultPlan(ctx, org, r.Plans.DefaultFreeCapUSD, start)
			if err != nil {
				r.log().Warn("plan reset: stamp default plan", "org", org, "err", err)
				rep.Errors++
				continue
			}
			if done {
				r.Plans.Invalidate(org)
				rep.Stamped++
			}
			continue
		}
		if pl.PeriodStart != nil && !pl.PeriodStart.Before(start) {
			continue // this month already started
		}
		rep.Checked++
		spent, err := r.Spend(ctx, org)
		if err != nil {
			r.log().Warn("plan reset: spend", "org", org, "err", err)
			rep.Errors++
			continue
		}
		done, err := r.Plans.Store.ResetPeriod(ctx, org, start, spent)
		if err != nil {
			r.log().Warn("plan reset", "org", org, "err", err)
			rep.Errors++
			continue
		}
		if !done {
			continue // another replica won the claim
		}
		r.Plans.Invalidate(org)
		if pl.PeriodStart != nil { // the first run only stamps the period: nothing was reset yet
			rep.Reset++
			if r.Audit != nil {
				r.Audit(ctx, org, "plan.period_reset", map[string]any{"period_start": start.Format("2006-01-02"), "base_usd": spent})
			}
		}
	}
	return rep
}

// Start sweeps shortly after start-up and then every interval (default 1h) until ctx ends.
func (r *PlanResetter) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			if rep := r.RunOnce(ctx); rep.Reset+rep.Stamped+rep.Errors > 0 {
				r.log().Info("plan reset", "checked", rep.Checked, "reset", rep.Reset, "stamped", rep.Stamped, "errors", rep.Errors)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
