package sso

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"aiworkforce/backend/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PGStore is the Postgres Store (migrations 700-702). Tenant tables go through auth.WithOrgTx so Row-Level Security
// applies; the two pre-tenant tables (verified_domains, sso_pending) are plain queries, see the migration comments.
type PGStore struct{ db auth.DB }

// NewPGStore wraps a pool.
func NewPGStore(db auth.DB) *PGStore { return &PGStore{db: db} }

var _ Store = (*PGStore)(nil)

func (s *PGStore) tx(ctx context.Context, org string, fn func(pgx.Tx) error) error {
	return auth.WithOrgTx(ctx, s.db, org, fn)
}

func zt(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func (s *PGStore) GetConfig(ctx context.Context, org string) (Config, error) {
	c := Config{OrgID: org}
	var role string
	err := s.tx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT enabled, issuer, client_id, sealed_secret, scopes, default_role, jit, allow_unverified_email, updated_by, created_at, updated_at
			FROM org_sso_configs WHERE org_id = $1`, org).
			Scan(&c.Enabled, &c.Issuer, &c.ClientID, &c.SealedSecret, &c.Scopes, &role, &c.JIT, &c.AllowUnverifiedEmail, &c.UpdatedBy, &c.CreatedAt, &c.UpdatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Config{}, ErrNotFound
	}
	c.DefaultRole = auth.Role(role)
	return c, err
}

func (s *PGStore) PutConfig(ctx context.Context, c Config) error {
	return s.tx(ctx, c.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO org_sso_configs (org_id, enabled, issuer, client_id, sealed_secret, scopes, default_role, jit, allow_unverified_email, updated_by, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (org_id) DO UPDATE SET enabled=EXCLUDED.enabled, issuer=EXCLUDED.issuer, client_id=EXCLUDED.client_id,
			  sealed_secret=EXCLUDED.sealed_secret, scopes=EXCLUDED.scopes, default_role=EXCLUDED.default_role, jit=EXCLUDED.jit,
			  allow_unverified_email=EXCLUDED.allow_unverified_email, updated_by=EXCLUDED.updated_by, updated_at=EXCLUDED.updated_at`,
			c.OrgID, c.Enabled, c.Issuer, c.ClientID, c.SealedSecret, c.Scopes, string(c.DefaultRole), c.JIT, c.AllowUnverifiedEmail, c.UpdatedBy, c.UpdatedAt)
		return err
	})
}

func (s *PGStore) DeleteConfig(ctx context.Context, org string) error {
	return s.tx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM org_sso_configs WHERE org_id = $1`, org)
		return err
	})
}

func (s *PGStore) GetPolicy(ctx context.Context, org string) (Policy, error) {
	p := Policy{OrgID: org}
	err := s.tx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT require_mfa, enforce_sso, updated_by, updated_at FROM org_identity_policy WHERE org_id = $1`, org).
			Scan(&p.RequireMFA, &p.EnforceSSO, &p.UpdatedBy, &p.UpdatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Policy{OrgID: org}, nil
	}
	return p, err
}

func (s *PGStore) PutPolicy(ctx context.Context, p Policy) error {
	return s.tx(ctx, p.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO org_identity_policy (org_id, require_mfa, enforce_sso, updated_by, updated_at) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (org_id) DO UPDATE SET require_mfa=EXCLUDED.require_mfa, enforce_sso=EXCLUDED.enforce_sso,
			  updated_by=EXCLUDED.updated_by, updated_at=EXCLUDED.updated_at`, p.OrgID, p.RequireMFA, p.EnforceSSO, p.UpdatedBy, p.UpdatedAt)
		return err
	})
}

func (s *PGStore) ListDomains(ctx context.Context, org string) ([]Domain, error) {
	var out []Domain
	err := s.tx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT domain, token, created_at, verified_at, last_check_at, lapsed_since FROM org_domains WHERE org_id = $1 ORDER BY domain`, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			d := Domain{OrgID: org}
			var v, l, ls *time.Time
			if err := rows.Scan(&d.Domain, &d.Token, &d.CreatedAt, &v, &l, &ls); err != nil {
				return err
			}
			d.VerifiedAt, d.LastCheck, d.LapsedSince = zt(v), zt(l), zt(ls)
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, err
}

func (s *PGStore) GetDomain(ctx context.Context, org, domain string) (Domain, error) {
	d := Domain{OrgID: org, Domain: domain}
	var v, l, ls *time.Time
	err := s.tx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT token, created_at, verified_at, last_check_at, lapsed_since FROM org_domains WHERE org_id = $1 AND domain = $2`, org, domain).
			Scan(&d.Token, &d.CreatedAt, &v, &l, &ls)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Domain{}, ErrNotFound
	}
	d.VerifiedAt, d.LastCheck, d.LapsedSince = zt(v), zt(l), zt(ls)
	return d, err
}

func (s *PGStore) AddDomain(ctx context.Context, d Domain) error {
	return s.tx(ctx, d.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO org_domains (org_id, domain, token, created_at) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
			d.OrgID, d.Domain, d.Token, d.CreatedAt)
		return err
	})
}

func (s *PGStore) DeleteDomain(ctx context.Context, org, domain string) error {
	return s.tx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM org_domains WHERE org_id = $1 AND domain = $2`, org, domain)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `DELETE FROM verified_domains WHERE domain = $1 AND org_id = $2`, domain, org)
		return err
	})
}

func (s *PGStore) MarkVerified(ctx context.Context, org, domain string, at time.Time) error {
	err := s.tx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE org_domains SET verified_at = $3 WHERE org_id = $1 AND domain = $2`, org, domain, at)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		// First organization to verify wins; the primary key refuses the second one.
		_, err = tx.Exec(ctx, `INSERT INTO verified_domains (domain, org_id, verified_at) VALUES ($1,$2,$3)`, domain, org, at)
		return err
	})
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return ErrDomainTaken
	}
	return err
}

func (s *PGStore) TouchDomainCheck(ctx context.Context, org, domain string, at time.Time) error {
	return s.tx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE org_domains SET last_check_at = $3 WHERE org_id = $1 AND domain = $2`, org, domain, at)
		return err
	})
}

func (s *PGStore) OrgForVerifiedDomain(ctx context.Context, domain string) (string, error) {
	var org string
	err := s.db.QueryRow(ctx, `SELECT org_id FROM verified_domains WHERE domain = $1`, domain).Scan(&org)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return org, err
}

func (s *PGStore) SavePending(ctx context.Context, p Pending) error {
	id, err := json.Marshal(p.Identity)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO sso_pending (kind, hash, org_id, nonce, verifier, identity, expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		p.Kind, p.Hash, p.OrgID, p.Nonce, p.Verifier, id, p.ExpiresAt)
	return err
}

// ConsumePending deletes the row and returns it in one statement, so two concurrent redeems cannot both succeed.
func (s *PGStore) ConsumePending(ctx context.Context, kind, hash string, now time.Time) (Pending, error) {
	p := Pending{Kind: kind, Hash: hash}
	var id []byte
	err := s.db.QueryRow(ctx, `DELETE FROM sso_pending WHERE kind = $1 AND hash = $2 RETURNING org_id, nonce, verifier, identity, expires_at`, kind, hash).
		Scan(&p.OrgID, &p.Nonce, &p.Verifier, &id, &p.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Pending{}, ErrNotFound
	}
	if err != nil {
		return Pending{}, err
	}
	if !now.Before(p.ExpiresAt) {
		return Pending{}, ErrNotFound
	}
	_ = json.Unmarshal(id, &p.Identity)
	return p, nil
}

func (s *PGStore) PurgeExpired(ctx context.Context, now time.Time) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sso_pending WHERE expires_at < $1`, now)
	return err
}

func (s *PGStore) ListVerified(ctx context.Context) ([]VerifiedRef, error) {
	rows, err := s.db.Query(ctx, `SELECT org_id, domain FROM verified_domains ORDER BY domain`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VerifiedRef
	for rows.Next() {
		var r VerifiedRef
		if err := rows.Scan(&r.OrgID, &r.Domain); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PGStore) SetDomainLapsed(ctx context.Context, org, domain string, since time.Time) error {
	var v *time.Time
	if !since.IsZero() {
		v = &since
	}
	return s.tx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE org_domains SET lapsed_since = $3 WHERE org_id = $1 AND domain = $2`, org, domain, v)
		return err
	})
}

func (s *PGStore) RevokeVerification(ctx context.Context, org, domain string) error {
	return s.tx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE org_domains SET verified_at = NULL, lapsed_since = NULL WHERE org_id = $1 AND domain = $2`, org, domain)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `DELETE FROM verified_domains WHERE domain = $1 AND org_id = $2`, domain, org)
		return err
	})
}

func (s *PGStore) GetAutoJoin(ctx context.Context, org string) (AutoJoin, error) {
	a := AutoJoin{OrgID: org, Role: auth.RoleViewer}
	var role string
	err := s.tx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT enabled, role, updated_by, updated_at FROM org_auto_join WHERE org_id = $1`, org).
			Scan(&a.Enabled, &role, &a.UpdatedBy, &a.UpdatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AutoJoin{OrgID: org, Role: auth.RoleViewer}, nil
	}
	if err == nil {
		a.Role = auth.Role(role)
	}
	return a, err
}

func (s *PGStore) PutAutoJoin(ctx context.Context, a AutoJoin) error {
	return s.tx(ctx, a.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO org_auto_join (org_id, enabled, role, updated_by, updated_at) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (org_id) DO UPDATE SET enabled=EXCLUDED.enabled, role=EXCLUDED.role, updated_by=EXCLUDED.updated_by, updated_at=EXCLUDED.updated_at`,
			a.OrgID, a.Enabled, string(a.Role), a.UpdatedBy, a.UpdatedAt)
		return err
	})
}
