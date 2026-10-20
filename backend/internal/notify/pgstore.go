package notify

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// TxRunner runs fn in a transaction scoped to one organization (Row-Level
// Security, migrations 720 and 721). *postgres.Store implements it.
type TxRunner interface {
	WithOrgTx(ctx context.Context, orgID string, fn func(tx pgx.Tx) error) error
}

// PG implements SettingsStore, ReplayStore, PlanStore and OutboxStore on Postgres.
type PG struct {
	DB TxRunner
	// Sealer (optional) seals the Slack/Teams URLs and the outbox payloads (migrations 783 and 784). Without a vault
	// key they stay as before: URLs in the plaintext columns, payloads unsealed.
	Sealer Sealer
	Log    *slog.Logger
}

var (
	_ SettingsStore = (*PG)(nil)
	_ ReplayStore   = (*PG)(nil)
	_ PlanStore     = (*PG)(nil)
	_ OutboxStore   = (*PG)(nil)
)

func (p *PG) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

func (p *PG) GetSettings(ctx context.Context, org string) (Settings, error) {
	s := DefaultSettings(org)
	var slackEnc, teamsEnc []byte
	found := false
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var to string
		err := tx.QueryRow(ctx, `SELECT email_enabled, email_to, slack_webhook, teams_webhook, slack_webhook_enc, teams_webhook_enc, min_risk, links_enabled, locale, updated_by, updated_at
			FROM notify_settings WHERE org_id = $1`, org).
			Scan(&s.EmailEnabled, &to, &s.SlackWebhook, &s.TeamsWebhook, &slackEnc, &teamsEnc, &s.MinRisk, &s.LinksEnabled, &s.Locale, &s.UpdatedBy, &s.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		found = err == nil
		s.EmailTo = splitList(to)
		return err
	})
	if err != nil || !found {
		return s, err
	}
	var resealSlack, resealTeams bool
	s.SlackWebhook, resealSlack = openHook(ctx, p.Sealer, p.log(), org, KindSlack, slackEnc, s.SlackWebhook)
	s.TeamsWebhook, resealTeams = openHook(ctx, p.Sealer, p.log(), org, KindTeams, teamsEnc, s.TeamsWebhook)
	if resealSlack || resealTeams {
		p.reseal(ctx, s) // rows written before migration 783: seal them once, best effort
	}
	return s, nil
}

// reseal moves the plaintext URLs of s into the sealed columns. It never changes anything else and never fails the read.
func (p *PG) reseal(ctx context.Context, s Settings) {
	slackEnc, _, err1 := sealHook(ctx, p.Sealer, s.OrgID, KindSlack, s.SlackWebhook)
	teamsEnc, _, err2 := sealHook(ctx, p.Sealer, s.OrgID, KindTeams, s.TeamsWebhook)
	if err1 != nil || err2 != nil {
		p.log().Warn("notify: cannot seal webhook URLs", "org", s.OrgID)
		return
	}
	// Only rows that still hold the plaintext are touched, so a concurrent PutSettings is never overwritten.
	err := p.DB.WithOrgTx(ctx, s.OrgID, func(tx pgx.Tx) error {
		if slackEnc != nil {
			if _, err := tx.Exec(ctx, `UPDATE notify_settings SET slack_webhook_enc=$2, slack_webhook='' WHERE org_id=$1 AND slack_webhook_enc IS NULL AND slack_webhook<>''`, s.OrgID, slackEnc); err != nil {
				return err
			}
		}
		if teamsEnc != nil {
			if _, err := tx.Exec(ctx, `UPDATE notify_settings SET teams_webhook_enc=$2, teams_webhook='' WHERE org_id=$1 AND teams_webhook_enc IS NULL AND teams_webhook<>''`, s.OrgID, teamsEnc); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		p.log().Warn("notify: cannot store sealed webhook URLs", "org", s.OrgID)
	}
}

func (p *PG) PutSettings(ctx context.Context, s Settings) error {
	slackEnc, slackPlain, err := sealHook(ctx, p.Sealer, s.OrgID, KindSlack, s.SlackWebhook)
	if err != nil {
		return err
	}
	teamsEnc, teamsPlain, err := sealHook(ctx, p.Sealer, s.OrgID, KindTeams, s.TeamsWebhook)
	if err != nil {
		return err
	}
	return p.DB.WithOrgTx(ctx, s.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO notify_settings (org_id, email_enabled, email_to, slack_webhook, teams_webhook, slack_webhook_enc, teams_webhook_enc, min_risk, links_enabled, locale, updated_by, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			ON CONFLICT (org_id) DO UPDATE SET email_enabled=EXCLUDED.email_enabled, email_to=EXCLUDED.email_to,
				slack_webhook=EXCLUDED.slack_webhook, teams_webhook=EXCLUDED.teams_webhook,
				slack_webhook_enc=EXCLUDED.slack_webhook_enc, teams_webhook_enc=EXCLUDED.teams_webhook_enc, min_risk=EXCLUDED.min_risk,
				links_enabled=EXCLUDED.links_enabled, locale=EXCLUDED.locale, updated_by=EXCLUDED.updated_by, updated_at=EXCLUDED.updated_at`,
			s.OrgID, s.EmailEnabled, strings.Join(s.EmailTo, ","), slackPlain, teamsPlain, slackEnc, teamsEnc, s.MinRisk, s.LinksEnabled, s.Locale, s.UpdatedBy, s.UpdatedAt)
		return err
	})
}

func splitList(s string) []string {
	out := []string{}
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Consume inserts the token id; the primary key makes exactly one caller win.
// Rows past their expiry are pruned on the way (they can never verify again).
func (p *PG) Consume(ctx context.Context, org, jti string, expires time.Time) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, _ = tx.Exec(ctx, `DELETE FROM notify_token_use WHERE org_id = $1 AND expires_at < now() - interval '1 hour'`, org)
		tag, err := tx.Exec(ctx, `INSERT INTO notify_token_use (org_id, jti, expires_at) VALUES ($1,$2,$3) ON CONFLICT (org_id, jti) DO NOTHING`,
			org, jti, expires.UTC())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrUsed
		}
		return nil
	})
}

func (p *PG) GetPlan(ctx context.Context, org string) (Plan, bool, error) {
	pl := Plan{OrgID: org}
	found := true
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT plan, cap_usd, cap_notified_at, period_start, spend_base_usd FROM org_plans WHERE org_id = $1`, org).
				Scan(&pl.Plan, &pl.CapUSD, &pl.CapNotifiedAt, &pl.PeriodStart, &pl.SpendBaseUSD)
		if errors.Is(err, pgx.ErrNoRows) {
			found = false
			return nil
		}
		return err
	})
	return pl, found, err
}

func (p *PG) PutPlan(ctx context.Context, pl Plan, by string) error {
	return p.DB.WithOrgTx(ctx, pl.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO org_plans (org_id, plan, cap_usd, updated_by) VALUES ($1,$2,$3,$4)
			ON CONFLICT (org_id) DO UPDATE SET plan=EXCLUDED.plan, cap_usd=EXCLUDED.cap_usd, updated_by=EXCLUDED.updated_by, updated_at=now()`,
			pl.OrgID, pl.Plan, pl.CapUSD, by)
		return err
	})
}

func (p *PG) MarkCapNotified(ctx context.Context, org string, t time.Time) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE org_plans SET cap_notified_at = $2 WHERE org_id = $1`, org, t.UTC())
		return err
	})
}

// ResetPeriod implements PlanStore. The UPDATE is the claim: it matches only a row whose period started before
// periodStart, so of several replicas exactly one resets a given month.
func (p *PG) ResetPeriod(ctx context.Context, org string, periodStart time.Time, lifetimeUSD float64) (bool, error) {
	done := false
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE org_plans SET
				spend_base_usd  = CASE WHEN period_start IS NULL THEN spend_base_usd ELSE $3 END,
				period_start    = $2,
				cap_notified_at = CASE WHEN period_start IS NULL THEN cap_notified_at ELSE NULL END
			WHERE org_id = $1 AND (period_start IS NULL OR period_start < $2)`, org, periodStart.UTC(), lifetimeUSD)
		done = err == nil && tag.RowsAffected() == 1
		return err
	})
	return done, err
}

// StampDefaultPlan implements PlanStamper. DO NOTHING on conflict makes it idempotent and safe between replicas.
func (p *PG) StampDefaultPlan(ctx context.Context, org string, capUSD float64, periodStart time.Time) (bool, error) {
	done := false
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO org_plans (org_id, plan, cap_usd, period_start, updated_by) VALUES ($1,'free',$2,$3,'plan-resetter')
			ON CONFLICT (org_id) DO NOTHING`, org, capUSD, periodStart.UTC())
		done = err == nil && tag.RowsAffected() == 1
		return err
	})
	return done, err
}
