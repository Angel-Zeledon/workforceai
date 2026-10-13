package tenantdata

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aiworkforce/backend/internal/domain"
)

// janitorLockKey is the advisory lock that makes the janitor run on one replica at a time.
const janitorLockKey int64 = 0x7E4A_0650

// DefaultJanitorInterval and DefaultTrialGrace are the janitor defaults.
const (
	DefaultJanitorInterval = 15 * time.Minute
	DefaultTrialGrace      = 7 * 24 * time.Hour
)

// Janitor purges, every Interval, the organizations whose deletion grace
// period is over and the trial accounts expired for more than TrialGrace
// (with their organizations). Several replicas may run it: a session-level
// advisory lock lets only one of them work at a time; the others skip the tick.
type Janitor struct {
	Pool       *pgxpool.Pool // owner connection (same as Purger.Owner)
	Purger     *Purger
	Interval   time.Duration // default 15 minutes
	TrialGrace time.Duration // default 7 days after trial_expires_at
	Log        *slog.Logger
}

// RunResult reports one janitor pass.
type RunResult struct {
	Locked       bool     // false when another replica held the lock (nothing was done)
	PurgedOrgs   []string // org hashes
	PurgedTrials int      // trial accounts deleted
	Errors       int
}

func (j *Janitor) log() *slog.Logger {
	if j.Log != nil {
		return j.Log
	}
	return slog.Default()
}

// Run loops until ctx is cancelled. The first pass runs right away.
func (j *Janitor) Run(ctx context.Context) {
	iv := j.Interval
	if iv <= 0 {
		iv = DefaultJanitorInterval
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		if _, err := j.RunOnce(ctx); err != nil && ctx.Err() == nil {
			j.log().Warn("tenantdata: janitor pass", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOnce performs one pass under the advisory lock.
func (j *Janitor) RunOnce(ctx context.Context) (RunResult, error) {
	var res RunResult
	conn, err := j.Pool.Acquire(ctx)
	if err != nil {
		return res, err
	}
	defer conn.Release()
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, janitorLockKey).Scan(&res.Locked); err != nil {
		return res, err
	}
	if !res.Locked {
		return res, nil
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, janitorLockKey) }()

	// 1. Organizations whose grace period is over.
	due, err := j.dueOrgs(ctx)
	if err != nil {
		return res, err
	}
	for _, org := range due {
		r, err := j.Purger.PurgeOrg(ctx, org, PurgeOptions{Reason: ReasonRequested, RequireDue: true})
		switch {
		case err == nil:
			res.PurgedOrgs = append(res.PurgedOrgs, r.OrgHash)
		case errors.Is(err, ErrNotDue):
			// cancelled meanwhile
		default:
			res.Errors++
			j.log().Error("tenantdata: purge of a due organization failed (rolled back)", "org_hash", OrgHash(org), "err", err)
		}
	}

	// 2. Trial accounts expired for more than TrialGrace, and their organizations.
	grace := j.TrialGrace
	if grace <= 0 {
		grace = DefaultTrialGrace
	}
	users, err := strings1(ctx, j.Pool, `SELECT id FROM users WHERE trial_expires_at IS NOT NULL AND trial_expires_at < now() - make_interval(secs => $1) ORDER BY trial_expires_at LIMIT 500`, grace.Seconds())
	if err != nil {
		return res, err
	}
	for _, u := range users {
		// Only organizations where every member is such an expired trial account.
		orgs, err := strings1(ctx, j.Pool, `SELECT m.org_id FROM memberships m WHERE m.user_id = $1 AND m.org_id <> $3
			AND NOT EXISTS (SELECT 1 FROM memberships m2 JOIN users u2 ON u2.id = m2.user_id
			                WHERE m2.org_id = m.org_id AND (u2.trial_expires_at IS NULL OR u2.trial_expires_at >= now() - make_interval(secs => $2)))`,
			u, grace.Seconds(), domain.DemoOrgID)
		if err != nil {
			res.Errors++
			continue
		}
		failed := false
		for _, org := range orgs {
			r, err := j.Purger.PurgeOrg(ctx, org, PurgeOptions{Reason: ReasonTrialExpired})
			if err != nil {
				failed = true
				res.Errors++
				j.log().Error("tenantdata: purge of an expired trial organization failed (rolled back)", "org_hash", OrgHash(org), "err", err)
				continue
			}
			res.PurgedOrgs = append(res.PurgedOrgs, r.OrgHash)
			res.PurgedTrials += int(r.Accounts)
		}
		if failed {
			continue
		}
		// The account itself (already gone when its last organization was purged).
		n, err := j.Purger.PurgeAccounts(ctx, []string{u})
		if err != nil {
			res.Errors++
			j.log().Error("tenantdata: purge of an expired trial account failed", "err", err)
			continue
		}
		res.PurgedTrials += int(n)
	}
	if len(res.PurgedOrgs) > 0 || res.PurgedTrials > 0 || res.Errors > 0 {
		j.log().Info("tenantdata: janitor pass", "purged_orgs", len(res.PurgedOrgs), "purged_trial_accounts", res.PurgedTrials, "errors", res.Errors)
	}
	return res, nil
}

// dueOrgs lists the organizations whose deletion is due. The owner role normally
// bypasses RLS (superuser or BYPASSRLS); when it does not, organizations are
// checked one by one with app.org_id bound (organizations known from memberships).
func (j *Janitor) dueOrgs(ctx context.Context) ([]string, error) {
	const q = `SELECT id FROM organizations WHERE deletion_at IS NOT NULL AND deletion_at <= now() AND id <> $1 ORDER BY deletion_at LIMIT 100`
	var bypass bool
	if err := j.Pool.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass); err != nil {
		return nil, err
	}
	if bypass {
		return strings1(ctx, j.Pool, q, domain.DemoOrgID)
	}
	all, err := strings1(ctx, j.Pool, `SELECT DISTINCT org_id FROM memberships`)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, org := range all {
		tx, err := j.Pool.Begin(ctx)
		if err != nil {
			return nil, err
		}
		ids, err := func() ([]string, error) {
			defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
			if _, err := tx.Exec(ctx, `SELECT set_config('app.org_id', $1, true)`, org); err != nil {
				return nil, err
			}
			return strings1(ctx, tx, q, domain.DemoOrgID)
		}()
		if err != nil {
			return nil, err
		}
		out = append(out, ids...)
	}
	return out, nil
}
