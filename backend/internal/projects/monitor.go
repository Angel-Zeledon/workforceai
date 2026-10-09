package projects

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
)

// Monitor cost control (W5). The watcher of a project used to rebuild and diff
// the whole detail every 250 ms. Now:
//   - the snapshot is loaded with filtered queries (loadSnapshot);
//   - an unchanged snapshot (same fingerprint) skips build + publish;
//   - the period backs off while nothing changes (Poll -> PollIdleMax) and
//     snaps back on any change or on a human action (liveProject.wake);
//   - a full rebuild still runs at least every PollIdleMax, because the view
//     also depends on state outside the snapshot (execution guard, wait nodes).

const (
	defaultPollIdleMax = 2 * time.Second
	// busyTicks is how many unchanged ticks keep the fast period before backing off.
	busyTicks = 8
	// defaultBudgetWarnPct is the early-warning threshold of a project (80% of its budget).
	defaultBudgetWarnPct = 0.8
)

func (s *Service) pollIdleMax() time.Duration {
	if s.cfg.PollIdleMax <= 0 {
		return defaultPollIdleMax
	}
	return max(s.cfg.PollIdleMax, s.cfg.Poll)
}

// nextDelay returns how long to sleep before the next tick.
func (s *Service) nextDelay(lp *liveProject, changed bool) time.Duration {
	if changed {
		lp.idleTicks = 0
	} else {
		lp.idleTicks++
	}
	if lp.idleTicks <= busyTicks {
		return s.cfg.Poll
	}
	shift := min(lp.idleTicks-busyTicks, 8)
	d := s.cfg.Poll << shift
	return min(d, s.pollIdleMax())
}

// snapshotChanged reports whether the project must be rebuilt: the fingerprint
// of the snapshot changed, or the periodic full rebuild is due.
func (s *Service) snapshotChanged(lp *liveProject, snap snapshot) bool {
	fp := fingerprint(snap)
	now := time.Now()
	if fp == lp.lastFP && !lp.lastFull.IsZero() && now.Sub(lp.lastFull) < s.pollIdleMax() {
		return false
	}
	lp.lastFP, lp.lastFull = fp, now
	return true
}

// fingerprint hashes everything the derived view reads from the stores.
func fingerprint(snap snapshot) string {
	h := sha1.New()
	rec := snap.rec
	fmt.Fprintf(h, "r|%s|%s|%g|%d|%d|%v\n", rec.Status, rec.Control, rec.BudgetUSD, rec.StructureVersion, len(rec.Decisions), rec.BudgetWarned)
	if snap.req != nil {
		fmt.Fprintf(h, "q|%s|%g\n", snap.req.Status, snap.req.CostUSD)
	}
	ids := make([]string, 0, len(snap.tasks))
	for id := range snap.tasks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		t := snap.tasks[id]
		fmt.Fprintf(h, "t|%s|%s|%s|%g|%d|%d\n", id, t.ID, t.Status, t.CostUSD, unixOrZero(t.StartedAt), unixOrZero(t.FinishedAt))
	}
	for _, a := range snap.approvals {
		fmt.Fprintf(h, "a|%s|%s|%d\n", a.ID, a.Status, unixOrZero(a.ResolvedAt))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func unixOrZero(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.UnixNano()
}

// ---- budget early warning ----

// warnThresholds returns the early-warning thresholds (fractions of the budget)
// of a project. A project that still has the default policy (0.5/0.8/0.95)
// alerts once at Config.BudgetWarnPct (default 80%); a customized WarnAt is
// honored as is.
func (s *Service) warnThresholds(rec Record) []float64 {
	def := defaultBudgetPolicy().WarnAt
	w := rec.Budget.WarnAt
	if len(w) == 0 || slices.Equal(w, def) {
		p := s.cfg.BudgetWarnPct
		if p <= 0 || p > 1 {
			p = defaultBudgetWarnPct
		}
		return []float64{p}
	}
	out := make([]float64, 0, len(w))
	for _, v := range w {
		if v > 0 && v <= 1 {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}

// warnedPct is the highest alerted threshold in percent (0 when none).
func warnedPct(warned []float64) float64 {
	m := 0.0
	for _, v := range warned {
		m = math.Max(m, v)
	}
	return math.Round(m * 1000 / 10)
}

// checkBudgetAlert emits project.budget_warning once per threshold crossing.
// The crossing is persisted before the event, so a restart never repeats it; if
// the budget is raised and the spend falls back under a threshold, it re-arms.
// It returns true when an alert was emitted.
func (s *Service) checkBudgetAlert(ctx context.Context, rec *Record, d Detail) bool {
	if rec.BudgetUSD <= 0 || terminalStatus(rec.Status) {
		return false
	}
	frac := d.Project.SpentUSD / rec.BudgetUSD
	var crossed, rearmed []float64
	for _, t := range s.warnThresholds(*rec) {
		switch has := containsFrac(rec.BudgetWarned, t); {
		case frac >= t && !has:
			crossed = append(crossed, t)
		case frac < t && has:
			rearmed = append(rearmed, t)
		}
	}
	if len(crossed) == 0 && len(rearmed) == 0 {
		return false
	}
	var fired bool
	upd, err := s.update(ctx, rec.ID, func(r *Record) error {
		for _, t := range rearmed {
			r.BudgetWarned = slices.DeleteFunc(r.BudgetWarned, func(v float64) bool { return math.Abs(v-t) < 1e-9 })
		}
		for _, t := range crossed {
			if !containsFrac(r.BudgetWarned, t) {
				r.BudgetWarned = append(r.BudgetWarned, t)
				fired = true
			}
		}
		return nil
	})
	if err != nil {
		s.cfg.Log.Warn("persist budget warning", "project", rec.ID, "err", err)
		return false
	}
	*rec = upd
	if !fired {
		return false
	}
	top := crossed[len(crossed)-1]
	d.Project.BudgetWarnPct = warnedPct(upd.BudgetWarned)
	pct := math.Round(frac*1000) / 10
	s.audit(application.WithActor(ctx, "system"), "project.budget_warning", rec.ID,
		map[string]any{"threshold_pct": top * 100, "pct": pct, "spent_usd": d.Project.SpentUSD, "budget_usd": rec.BudgetUSD})
	s.emit(ctx, "project.budget_warning", rec.ID, map[string]any{"project": d.Project, "threshold_pct": top * 100, "pct": pct,
		"spent_usd": d.Project.SpentUSD, "budget_usd": rec.BudgetUSD, "remaining_usd": math.Max(0, rec.BudgetUSD-d.Project.SpentUSD),
		"text": strings.TrimSpace(tr(rec.Locale, "pv.budgetWarn.text", map[string]string{"pct": fmt.Sprintf("%.0f", pct), "name": rec.Name}))})
	return true
}

func containsFrac(list []float64, t float64) bool {
	return slices.ContainsFunc(list, func(v float64) bool { return math.Abs(v-t) < 1e-9 })
}
