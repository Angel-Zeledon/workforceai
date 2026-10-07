package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/vault"
)

// VaultRepo implements vault.Repo on Postgres. It is the ONLY code outside
// internal/vault that reads or writes the credentials table (enforced by
// vault.TestOnlyVaultTouchesCredentialsTable).
type VaultRepo struct{ S *Store }

var _ vault.Repo = (*VaultRepo)(nil)

func vnf(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return vault.ErrNotFound
	}
	return err
}

func (v *VaultRepo) ActiveDataKey(ctx context.Context, org string) (vault.DataKey, bool, error) {
	k, err := one(ctx, v.S, org, scanDataKey, `SELECT org_id, version, kek_id, wrapped_dek, status FROM org_data_keys
		WHERE org_id=$1 AND status='active' ORDER BY version DESC LIMIT 1`, org)
	if errors.Is(err, pgx.ErrNoRows) {
		return vault.DataKey{}, false, nil
	}
	return k, err == nil, err
}

func scanDataKey(r scanner) (vault.DataKey, error) {
	var k vault.DataKey
	err := r.Scan(&k.Org, &k.Version, &k.KEKID, &k.Wrapped, &k.Status)
	return k, err
}

func (v *VaultRepo) GetDataKey(ctx context.Context, org string, version int) (vault.DataKey, error) {
	k, err := one(ctx, v.S, org, scanDataKey, `SELECT org_id, version, kek_id, wrapped_dek, status FROM org_data_keys WHERE org_id=$1 AND version=$2`, org, version)
	return k, vnf(err)
}

func (v *VaultRepo) ListDataKeys(ctx context.Context, org string) ([]vault.DataKey, error) {
	return many(ctx, v.S, org, scanDataKey, `SELECT org_id, version, kek_id, wrapped_dek, status FROM org_data_keys WHERE org_id=$1 ORDER BY version`, org)
}

func (v *VaultRepo) PutDataKey(ctx context.Context, k vault.DataKey) error {
	return v.S.exec(ctx, k.Org, `INSERT INTO org_data_keys (org_id, version, kek_id, wrapped_dek, status) VALUES ($1,$2,$3,$4,$5)`,
		k.Org, k.Version, k.KEKID, k.Wrapped, k.Status)
}

func (v *VaultRepo) UpdateDataKey(ctx context.Context, k vault.DataKey) error {
	return v.S.exec(ctx, k.Org, `UPDATE org_data_keys SET kek_id=$3, wrapped_dek=$4, status=$5,
		retired_at = CASE WHEN $5='retired' THEN COALESCE(retired_at, now()) ELSE NULL END
		WHERE org_id=$1 AND version=$2`, k.Org, k.Version, k.KEKID, k.Wrapped, k.Status)
}

func (v *VaultRepo) PutCredential(ctx context.Context, r vault.Record) error {
	return v.S.exec(ctx, r.Org, `INSERT INTO credentials (id, org_id, connection_id, version, status, kind, ciphertext, nonce, dek_version, hint, expires_at, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		r.ID, r.Org, r.ConnID, r.Version, r.Status, r.Kind, r.Ciphertext, r.Nonce, r.DEKVersion, r.Hint, r.ExpiresAt, r.CreatedBy, r.CreatedAt)
}

func (v *VaultRepo) ActiveCredential(ctx context.Context, org, connID string) (vault.Record, error) {
	r, err := one(ctx, v.S, org, func(s scanner) (vault.Record, error) {
		var x vault.Record
		var at time.Time
		err := s.Scan(&x.ID, &x.Org, &x.ConnID, &x.Version, &x.Status, &x.Kind, &x.Ciphertext, &x.Nonce, &x.DEKVersion, &x.Hint, &x.ExpiresAt, &x.CreatedBy, &at)
		x.CreatedAt = at
		return x, err
	}, `SELECT id, org_id, connection_id, version, status, kind, ciphertext, nonce, dek_version, hint, expires_at, created_by, created_at
		FROM credentials WHERE org_id=$1 AND connection_id=$2 AND status IN ('active','destroyed') ORDER BY version DESC LIMIT 1`, org, connID)
	if err != nil {
		return vault.Record{}, vnf(err)
	}
	if r.Status == vault.StatusDestroyed {
		return vault.Record{}, vault.ErrDestroyed
	}
	return r, nil
}

func (v *VaultRepo) RetireActive(ctx context.Context, org, connID string) error {
	return v.S.exec(ctx, org, `UPDATE credentials SET status='retired', retired_at=now() WHERE org_id=$1 AND connection_id=$2 AND status='active'`, org, connID)
}

func (v *VaultRepo) DestroyAll(ctx context.Context, org, connID string) error {
	return v.S.exec(ctx, org, `UPDATE credentials SET status='destroyed', ciphertext=NULL, nonce=NULL, destroyed_at=now()
		WHERE org_id=$1 AND connection_id=$2 AND status <> 'destroyed'`, org, connID)
}
