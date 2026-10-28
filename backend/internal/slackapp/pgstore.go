package slackapp

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// TxRunner runs fn in a transaction scoped to one organization (Row-Level Security, migration 801).
// *postgres.Store implements it.
type TxRunner interface {
	WithOrgTx(ctx context.Context, orgID string, fn func(tx pgx.Tx) error) error
}

// PG implements Store on Postgres (tables of migration 801). Secrets arrive already sealed.
type PG struct{ DB TxRunner }

var _ Store = (*PG)(nil)

func (p *PG) GetRecord(ctx context.Context, org string) (Record, bool, error) {
	r := DefaultRecord(org)
	found := false
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT enabled, signing_secret_enc, bot_token_enc, token_last4, team_id, team_name, bot_user_id, channel_id,
				min_risk, locale, email_matching, high_risk_in_app, verified_at, updated_by, updated_at
			FROM slack_app_settings WHERE org_id = $1`, org).
			Scan(&r.Enabled, &r.SigningSecretEnc, &r.BotTokenEnc, &r.TokenLast4, &r.TeamID, &r.TeamName, &r.BotUserID, &r.ChannelID,
				&r.MinRisk, &r.Locale, &r.EmailMatching, &r.HighRiskInApp, &r.VerifiedAt, &r.UpdatedBy, &r.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		found = err == nil
		return err
	})
	return r, found, err
}

func (p *PG) PutRecord(ctx context.Context, r Record) error {
	return p.DB.WithOrgTx(ctx, r.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO slack_app_settings (org_id, enabled, signing_secret_enc, bot_token_enc, token_last4, team_id, team_name,
				bot_user_id, channel_id, min_risk, locale, email_matching, high_risk_in_app, verified_at, updated_by, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
			ON CONFLICT (org_id) DO UPDATE SET enabled=EXCLUDED.enabled, signing_secret_enc=EXCLUDED.signing_secret_enc,
				bot_token_enc=EXCLUDED.bot_token_enc, token_last4=EXCLUDED.token_last4, team_id=EXCLUDED.team_id, team_name=EXCLUDED.team_name,
				bot_user_id=EXCLUDED.bot_user_id, channel_id=EXCLUDED.channel_id, min_risk=EXCLUDED.min_risk, locale=EXCLUDED.locale,
				email_matching=EXCLUDED.email_matching, high_risk_in_app=EXCLUDED.high_risk_in_app, verified_at=EXCLUDED.verified_at,
				updated_by=EXCLUDED.updated_by, updated_at=EXCLUDED.updated_at`,
			r.OrgID, r.Enabled, r.SigningSecretEnc, r.BotTokenEnc, r.TokenLast4, r.TeamID, r.TeamName, r.BotUserID, r.ChannelID,
			r.MinRisk, r.Locale, r.EmailMatching, r.HighRiskInApp, r.VerifiedAt, r.UpdatedBy, r.UpdatedAt.UTC())
		return err
	})
}

func (p *PG) DeleteAll(ctx context.Context, org string) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		for _, q := range []string{
			`DELETE FROM slack_user_links WHERE org_id = $1`,
			`DELETE FROM slack_link_codes WHERE org_id = $1`,
			`DELETE FROM slack_interactions WHERE org_id = $1`,
			`DELETE FROM slack_approval_messages WHERE org_id = $1`,
			`DELETE FROM slack_app_settings WHERE org_id = $1`,
		} {
			if _, err := tx.Exec(ctx, q, org); err != nil {
				return err
			}
		}
		return nil
	})
}

func (p *PG) GetLink(ctx context.Context, org, team, slackUser string) (Link, error) {
	l := Link{OrgID: org}
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT team_id, slack_user_id, user_id, method, created_at FROM slack_user_links
			WHERE org_id = $1 AND team_id = $2 AND slack_user_id = $3`, org, team, slackUser).
			Scan(&l.TeamID, &l.SlackUserID, &l.UserID, &l.Method, &l.CreatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	return l, err
}

func (p *PG) LinksOf(ctx context.Context, org, userID string) ([]Link, error) {
	out := []Link{}
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT team_id, slack_user_id, user_id, method, created_at FROM slack_user_links
			WHERE org_id = $1 AND user_id = $2 ORDER BY created_at`, org, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			l := Link{OrgID: org}
			if err := rows.Scan(&l.TeamID, &l.SlackUserID, &l.UserID, &l.Method, &l.CreatedAt); err != nil {
				return err
			}
			out = append(out, l)
		}
		return rows.Err()
	})
	return out, err
}

func (p *PG) PutLink(ctx context.Context, l Link) error {
	return p.DB.WithOrgTx(ctx, l.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO slack_user_links (org_id, team_id, slack_user_id, user_id, method, created_at) VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (org_id, team_id, slack_user_id) DO UPDATE SET user_id=EXCLUDED.user_id, method=EXCLUDED.method, created_at=EXCLUDED.created_at`,
			l.OrgID, l.TeamID, l.SlackUserID, l.UserID, l.Method, l.CreatedAt.UTC())
		return err
	})
}

func (p *PG) DeleteLinksOf(ctx context.Context, org, userID string) (int, error) {
	n := 0
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM slack_user_links WHERE org_id = $1 AND user_id = $2`, org, userID)
		n = int(tag.RowsAffected())
		return err
	})
	return n, err
}

func (p *PG) CreateCode(ctx context.Context, org, codeHash, userID string, expires time.Time) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		// One pending code per person, and old codes are pruned on the way.
		if _, err := tx.Exec(ctx, `DELETE FROM slack_link_codes WHERE org_id = $1 AND (user_id = $2 OR expires_at < now() - interval '1 day')`, org, userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO slack_link_codes (org_id, code_hash, user_id, expires_at) VALUES ($1,$2,$3,$4)`, org, codeHash, userID, expires.UTC())
		return err
	})
}

// ConsumeCode: the UPDATE ... WHERE used_at IS NULL is the claim, so a code links exactly one Slack account.
func (p *PG) ConsumeCode(ctx context.Context, org, codeHash string, now time.Time) (string, error) {
	var user string
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `UPDATE slack_link_codes SET used_at = $3 WHERE org_id = $1 AND code_hash = $2 AND used_at IS NULL AND expires_at > $3
			RETURNING user_id`, org, codeHash, now.UTC()).Scan(&user)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrCodeInvalid
	}
	return user, err
}

// Claim: the primary key makes exactly one delivery of an interaction win. Rows older than a day are pruned.
func (p *PG) Claim(ctx context.Context, org, key string, now time.Time) (bool, error) {
	won := false
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, _ = tx.Exec(ctx, `DELETE FROM slack_interactions WHERE org_id = $1 AND created_at < $2`, org, now.Add(-24*time.Hour).UTC())
		tag, err := tx.Exec(ctx, `INSERT INTO slack_interactions (org_id, dedupe_key, outcome, created_at) VALUES ($1,$2,$3,$4)
			ON CONFLICT (org_id, dedupe_key) DO NOTHING`, org, key, OutcomeProcessing, now.UTC())
		won = err == nil && tag.RowsAffected() == 1
		return err
	})
	return won, err
}

func (p *PG) SetOutcome(ctx context.Context, org, key, outcome string) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE slack_interactions SET outcome = $3 WHERE org_id = $1 AND dedupe_key = $2`, org, key, outcome)
		return err
	})
}

func (p *PG) PutMessage(ctx context.Context, org, approvalID, channel, ts string) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO slack_approval_messages (org_id, approval_id, channel_id, message_ts) VALUES ($1,$2,$3,$4)
			ON CONFLICT (org_id, approval_id) DO UPDATE SET channel_id=EXCLUDED.channel_id, message_ts=EXCLUDED.message_ts`, org, approvalID, channel, ts)
		return err
	})
}

func (p *PG) GetMessage(ctx context.Context, org, approvalID string) (string, string, error) {
	var ch, ts string
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT channel_id, message_ts FROM slack_approval_messages WHERE org_id = $1 AND approval_id = $2`, org, approvalID).Scan(&ch, &ts)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return ch, ts, err
}
