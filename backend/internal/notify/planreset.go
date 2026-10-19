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
// Only organizations with an explicit org_plans row are reset. An organization on the deployment default
// (FREE_PLAN_CAP_USD, no row) keeps counting its lifetime spend.
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

// ResetReport says what one sweep did.
type ResetReport struct{ Checked, Reset, Errors int }

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
		if !ok || (pl.PeriodStart != nil && !pl.PeriodStart.Before(start)) {
			continue // no explicit plan, or this month already started
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
			if rep := r.RunOnce(ctx); rep.Reset+rep.Errors > 0 {
				r.log().Info("plan reset", "checked", rep.Checked, "reset", rep.Reset, "errors", rep.Errors)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
