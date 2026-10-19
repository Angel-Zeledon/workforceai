package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Retention floors and ceiling (days). 0 means "keep for ever".
const (
	MinChatsDays = 30
	MinFilesDays = 30
	MinAuditDays = 365
	MaxDays      = 3650
	// DefaultBatch bounds the rows one run touches per organization and table: a mistaken policy cannot wipe
	// an organization in one go, and a long backlog drains over several runs.
	DefaultBatch = 5000
)

// RetentionPolicy is the per-organization policy.
type RetentionPolicy struct {
	Enabled    bool            `json:"enabled"`
	ChatsDays  int             `json:"chats_days"`
	FilesDays  int             `json:"files_days"`
	AuditDays  int             `json:"audit_days"`
	LastRunAt  *time.Time      `json:"last_run_at,omitempty"`
	LastResult json.RawMessage `json:"last_result,omitempty"`
}

// Validate enforces the floors. The same function guards the API and the purge job (a row edited directly in
// the database is re-validated before anything is deleted).
func (p RetentionPolicy) Validate() error {
	for _, c := range []struct {
		name     string
		v, floor int
	}{{"chats_days", p.ChatsDays, MinChatsDays}, {"files_days", p.FilesDays, MinFilesDays}, {"audit_days", p.AuditDays, MinAuditDays}} {
		if c.v != 0 && (c.v < c.floor || c.v > MaxDays) {
			return fmt.Errorf("%w: %s must be 0 (keep) or between %d and %d", ErrInvalid, c.name, c.floor, MaxDays)
		}
	}
	return nil
}

// PurgeResult says what a run did (or, for a preview, would do).
type PurgeResult struct {
	DryRun        bool      `json:"dry_run"`
	At            time.Time `json:"at"`
	Messages      int       `json:"messages"`
	Conversations int       `json:"conversations"`
	FilesTrashed  int       `json:"files_trashed"`
	// AuditSkipped is always true when audit_days is set: the hash-chained trail is never deleted by this job.
	AuditSkipped bool `json:"audit_skipped"`
	// Capped is true when a batch limit stopped the run: the rest follows in the next run.
	Capped bool `json:"capped"`
}

// RetentionStore is the persistence of the policy and of the purge. Everything is scoped to one organization.
type RetentionStore interface {
	GetPolicy(ctx context.Context, org string) (RetentionPolicy, error)
	PutPolicy(ctx context.Context, org string, p RetentionPolicy, actor string) error
	// ClaimDue atomically claims a scheduled run: it returns the policy when it is enabled and due, and moves
	// next_run_at forward, so concurrent replicas never purge the same organization twice.
	ClaimDue(ctx context.Context, org string, now time.Time, every time.Duration) (RetentionPolicy, bool, error)
	// CountChats / CountFiles report what a cutoff would remove (preview).
	CountChats(ctx context.Context, org string, cutoff time.Time) (messages, conversations int, err error)
	CountFiles(ctx context.Context, org string, cutoff time.Time) (int, error)
	// PurgeChats deletes at most limit messages older than cutoff, then the conversations that are older than
	// cutoff and have no messages left.
	PurgeChats(ctx context.Context, org string, cutoff time.Time, limit int) (messages, conversations int, err error)
	// TrashFiles moves at most limit live files not updated since cutoff to the trash (recoverable).
	TrashFiles(ctx context.Context, org string, cutoff time.Time, limit int) (int, error)
	SaveResult(ctx context.Context, org string, at time.Time, r PurgeResult) error
}

// OrgLister lists organizations.
type OrgLister interface {
	ListOrgIDs(ctx context.Context) ([]string, error)
}

// Purger applies retention policies.
type Purger struct {
	Store RetentionStore
	Orgs  OrgLister
	Now   func() time.Time
	Batch int
	// Every is the pause between runs of one organization (default 24h).
	Every time.Duration
	// Protected organizations are never purged (the shared demo organization).
	Protected map[string]bool
	// Audit records what was removed (counts only).
	Audit func(ctx context.Context, org, action string, details map[string]any)
	Log   *slog.Logger
}

func (p *Purger) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

func (p *Purger) batch() int {
	if p.Batch > 0 {
		return p.Batch
	}
	return DefaultBatch
}

func (p *Purger) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

func cutoff(now time.Time, days int) time.Time { return now.AddDate(0, 0, -days) }

// Preview counts what the current policy would remove right now, without touching anything. It works even
// when the policy is disabled (so an administrator can see the effect before switching it on).
func (p *Purger) Preview(ctx context.Context, org string, pol RetentionPolicy) (PurgeResult, error) {
	return p.run(ctx, org, pol, true, true)
}

// RunNow applies the policy immediately (an owner clicked "run now"). Disabled policies do nothing.
func (p *Purger) RunNow(ctx context.Context, org string, pol RetentionPolicy) (PurgeResult, error) {
	r, err := p.run(ctx, org, pol, false, false)
	if err == nil {
		_ = p.Store.SaveResult(ctx, org, r.At, r)
	}
	return r, err
}

// run is the single place that decides what may be deleted.
func (p *Purger) run(ctx context.Context, org string, pol RetentionPolicy, dry, ignoreEnabled bool) (PurgeResult, error) {
	now := p.now()
	res := PurgeResult{DryRun: dry, At: now, AuditSkipped: pol.AuditDays > 0}
	if org == "" || p.Protected[org] {
		return res, fmt.Errorf("%w: organization is protected", ErrInvalid)
	}
	if err := pol.Validate(); err != nil {
		return res, err
	}
	if !pol.Enabled && !ignoreEnabled {
		return res, nil
	}
	limit := p.batch()
	if pol.ChatsDays > 0 {
		c := cutoff(now, pol.ChatsDays)
		if dry {
			m, cv, err := p.Store.CountChats(ctx, org, c)
			if err != nil {
				return res, err
			}
			res.Messages, res.Conversations = m, cv
		} else {
			m, cv, err := p.Store.PurgeChats(ctx, org, c, limit)
			if err != nil {
				return res, err
			}
			res.Messages, res.Conversations = m, cv
			res.Capped = res.Capped || m >= limit
		}
	}
	if pol.FilesDays > 0 {
		c := cutoff(now, pol.FilesDays)
		if dry {
			n, err := p.Store.CountFiles(ctx, org, c)
			if err != nil {
				return res, err
			}
			res.FilesTrashed = n
		} else {
			n, err := p.Store.TrashFiles(ctx, org, c, limit)
			if err != nil {
				return res, err
			}
			res.FilesTrashed = n
			res.Capped = res.Capped || n >= limit
		}
	}
	if !dry && p.Audit != nil && (res.Messages+res.Conversations+res.FilesTrashed) > 0 {
		p.Audit(ctx, org, "retention.purged", map[string]any{"messages": res.Messages, "conversations": res.Conversations,
			"files_trashed": res.FilesTrashed, "chats_days": pol.ChatsDays, "files_days": pol.FilesDays, "capped": res.Capped})
	}
	return res, nil
}

// Tick runs the scheduled purge once over every organization.
func (p *Purger) Tick(ctx context.Context) {
	if p.Orgs == nil {
		return
	}
	ids, err := p.Orgs.ListOrgIDs(ctx)
	if err != nil {
		p.log().Warn("retention: list organizations", "err", err)
		return
	}
	every := p.Every
	if every <= 0 {
		every = 24 * time.Hour
	}
	for _, org := range ids {
		if ctx.Err() != nil {
			return
		}
		if p.Protected[org] {
			continue
		}
		pol, due, err := p.Store.ClaimDue(ctx, org, p.now(), every)
		if err != nil || !due {
			if err != nil {
				p.log().Warn("retention: claim", "org", org, "err", err)
			}
			continue
		}
		res, err := p.run(ctx, org, pol, false, false)
		if err != nil {
			p.log().Warn("retention: purge", "org", org, "err", err)
			continue
		}
		_ = p.Store.SaveResult(ctx, org, res.At, res)
		if res.Messages+res.Conversations+res.FilesTrashed > 0 {
			p.log().Info("retention: purged", "org", org, "messages", res.Messages, "conversations", res.Conversations, "files", res.FilesTrashed)
		}
	}
}

// Start runs Tick every interval (default 1h) until ctx ends, after a short startup delay.
func (p *Purger) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Minute):
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			p.Tick(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
