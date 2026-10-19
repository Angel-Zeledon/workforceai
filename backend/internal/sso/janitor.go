package sso

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"time"
)

// Janitor keeps the identity tables honest (docs/architecture/identity.md section 3):
//
//   - it deletes the abandoned single-use login records (Store.PurgeExpired), which are otherwise only removed when
//     somebody redeems them;
//   - it re-checks the DNS challenge record of every verified domain. A record that is gone starts a grace period
//     (the domain keeps working, the organization is told); a record that stays gone past the grace period revokes the
//     verification, so a domain that lapsed and was bought by somebody else cannot keep routing SSO or auto-join.
//
// A DNS failure (timeout, SERVFAIL) is NOT a lapse: only "the name or the record does not exist", or an answer that no
// longer carries the token, counts. One bad resolver minute must never log an organization out.
type Janitor struct {
	Store    Store
	Resolver Resolver // default net.DefaultResolver
	Now      func() time.Time
	// Grace is how long a record may stay missing before the verification is revoked (default 7 days).
	Grace time.Duration
	// Recheck is the minimum time between two checks of one domain (default 24 hours).
	Recheck time.Duration
	// Audit records lapse, recovery and revocation (optional).
	Audit func(ctx context.Context, orgID, actor, action string, details map[string]any)
	Log   *slog.Logger
}

// Defaults of the janitor.
const (
	DefaultGrace   = 7 * 24 * time.Hour
	DefaultRecheck = 24 * time.Hour
)

// Report says what one sweep did (tests and logs).
type Report struct {
	Checked, Lapsed, Recovered, Revoked, Errors int
}

func (j *Janitor) now() time.Time {
	if j.Now != nil {
		return j.Now().UTC()
	}
	return time.Now().UTC()
}

func (j *Janitor) log() *slog.Logger {
	if j.Log != nil {
		return j.Log
	}
	return slog.Default()
}

func (j *Janitor) audit(ctx context.Context, org, action string, d map[string]any) {
	if j.Audit != nil {
		j.Audit(ctx, org, "system", action, d)
	}
}

// ErrNoRecord can be returned by a Resolver for "that name has no TXT record" (net.DNSError with IsNotFound counts too).
var ErrNoRecord = errors.New("sso: no such record")

func notFound(err error) bool {
	var de *net.DNSError
	return errors.Is(err, ErrNoRecord) || (errors.As(err, &de) && de.IsNotFound)
}

// RunOnce does one sweep: purge, then re-check the domains that are due.
func (j *Janitor) RunOnce(ctx context.Context) Report {
	var rep Report
	now := j.now()
	if err := j.Store.PurgeExpired(ctx, now); err != nil {
		j.log().Warn("sso janitor: purge", "err", err)
		rep.Errors++
	}
	refs, err := j.Store.ListVerified(ctx)
	if err != nil {
		j.log().Warn("sso janitor: list verified domains", "err", err)
		rep.Errors++
		return rep
	}
	res := j.Resolver
	if res == nil {
		res = defaultResolver{}
	}
	every, grace := j.Recheck, j.Grace
	if every <= 0 {
		every = DefaultRecheck
	}
	if grace <= 0 {
		grace = DefaultGrace
	}
	for _, ref := range refs {
		if ctx.Err() != nil {
			return rep
		}
		d, err := j.Store.GetDomain(ctx, ref.OrgID, ref.Domain)
		if err != nil || !d.Verified() {
			continue
		}
		if !d.LastCheck.IsZero() && now.Sub(d.LastCheck) < every {
			continue
		}
		rep.Checked++
		cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		txts, lerr := res.LookupTXT(cctx, challengePrefix+d.Domain)
		cancel()
		if lerr != nil && !notFound(lerr) {
			rep.Errors++ // inconclusive: try again on the next sweep, change nothing
			continue
		}
		_ = j.Store.TouchDomainCheck(ctx, d.OrgID, d.Domain, now)
		if lerr == nil && hasChallenge(txts, d.Token) {
			if !d.LapsedSince.IsZero() {
				_ = j.Store.SetDomainLapsed(ctx, d.OrgID, d.Domain, time.Time{})
				j.audit(ctx, d.OrgID, "domain.recovered", map[string]any{"domain": d.Domain})
				rep.Recovered++
			}
			continue
		}
		if d.LapsedSince.IsZero() {
			_ = j.Store.SetDomainLapsed(ctx, d.OrgID, d.Domain, now)
			j.audit(ctx, d.OrgID, "domain.lapsed", map[string]any{"domain": d.Domain, "grace_hours": int(grace.Hours())})
			rep.Lapsed++
			continue
		}
		if now.Sub(d.LapsedSince) < grace {
			continue
		}
		if err := j.Store.RevokeVerification(ctx, d.OrgID, d.Domain); err != nil {
			rep.Errors++
			continue
		}
		rep.Revoked++
		j.audit(ctx, d.OrgID, "domain.verification_revoked", map[string]any{"domain": d.Domain, "reason": "challenge_record_missing"})
		j.relaxPolicy(ctx, d.OrgID)
	}
	return rep
}

// relaxPolicy keeps a revoked domain from locking people out: with no verified domain left, SSO enforcement and auto-join
// would be unreachable (SSO routing needs a verified domain), so both are switched off. Owners keep the password.
func (j *Janitor) relaxPolicy(ctx context.Context, org string) {
	ds, err := j.Store.ListDomains(ctx, org)
	if err != nil {
		return
	}
	for _, d := range ds {
		if d.Verified() {
			return
		}
	}
	if p, err := j.Store.GetPolicy(ctx, org); err == nil && p.EnforceSSO {
		p.EnforceSSO, p.UpdatedBy, p.UpdatedAt = false, "system", j.now()
		if j.Store.PutPolicy(ctx, p) == nil {
			j.audit(ctx, org, "org.identity_policy_changed", map[string]any{"enforce_sso": false, "reason": "no_verified_domain"})
		}
	}
	if a, err := j.Store.GetAutoJoin(ctx, org); err == nil && a.Enabled {
		a.Enabled, a.UpdatedBy, a.UpdatedAt = false, "system", j.now()
		if j.Store.PutAutoJoin(ctx, a) == nil {
			j.audit(ctx, org, "org.auto_join_changed", map[string]any{"enabled": false, "reason": "no_verified_domain"})
		}
	}
}

func hasChallenge(txts []string, token string) bool {
	want := challengeValue + token
	for _, t := range txts {
		if strings.TrimSpace(strings.Trim(t, `"`)) == want {
			return true
		}
	}
	return false
}

// Start runs RunOnce every interval (default 1 hour) until ctx ends, after a short startup delay.
func (j *Janitor) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(90 * time.Second):
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			r := j.RunOnce(ctx)
			if r.Lapsed+r.Recovered+r.Revoked > 0 {
				j.log().Info("sso janitor", "checked", r.Checked, "lapsed", r.Lapsed, "recovered", r.Recovered, "revoked", r.Revoked)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
