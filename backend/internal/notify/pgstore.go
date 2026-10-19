package notify

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// TxRunner runs fn in a transaction scoped to one organization (Row-Level
// Security, migrations 720 and 721). *postgres.Store implements it.
type TxRunner interface {
	WithOrgTx(ctx context.Context, orgID string, fn func(tx pgx.Tx) error) error
}

// PG implements SettingsStore, ReplayStore and PlanStore on Postgres.
type PG struct{ DB TxRunner }

var (
	_ SettingsStore = (*PG)(nil)
	_ ReplayStore   = (*PG)(nil)
	_ PlanStore     = (*PG)(nil)
)

func (p *PG) GetSettings(ctx context.Context, org string) (Settings, error) {
	s := DefaultSettings(org)
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var to string
		err := tx.QueryRow(ctx, `SELECT email_enabled, email_to, slack_webhook, teams_webhook, min_risk, links_enabled, locale, updated_by, updated_at
			FROM notify_settings WHERE org_id = $1`, org).
			Scan(&s.EmailEnabled, &to, &s.SlackWebhook, &s.TeamsWebhook, &s.MinRisk, &s.LinksEnabled, &s.Locale, &s.UpdatedBy, &s.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		s.EmailTo = splitList(to)
		return err
	})
	return s, err
}

func (p *PG) PutSettings(ctx context.Context, s Settings) error {
	return p.DB.WithOrgTx(ctx, s.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO notify_settings (org_id, email_enabled, email_to, slack_webhook, teams_webhook, min_risk, links_enabled, locale, updated_by, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (org_id) DO UPDATE SET email_enabled=EXCLUDED.email_enabled, email_to=EXCLUDED.email_to,
				slack_webhook=EXCLUDED.slack_webhook, teams_webhook=EXCLUDED.teams_webhook, min_risk=EXCLUDED.min_risk,
				links_enabled=EXCLUDED.links_enabled, locale=EXCLUDED.locale, updated_by=EXCLUDED.updated_by, updated_at=EXCLUDED.updated_at`,
			s.OrgID, s.EmailEnabled, strings.Join(s.EmailTo, ","), s.SlackWebhook, s.TeamsWebhook, s.MinRisk, s.LinksEnabled, s.Locale, s.UpdatedBy, s.UpdatedAt)
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
		err := tx.QueryRow(ctx, `SELECT plan, cap_usd, cap_notified_at FROM org_plans WHERE org_id = $1`, org).Scan(&pl.Plan, &pl.CapUSD, &pl.CapNotifiedAt)
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
