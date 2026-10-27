package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/portal"
)

// PortalStore implements portal.Store on Postgres (migration 798). Every call but LinkByHash runs through
// WithOrgTx, so Row-Level Security applies; LinkByHash announces only the token hash (portal_lookup policy).
type PortalStore struct{ S *Store }

var _ portal.Store = (*PortalStore)(nil)

func (p *PortalStore) GetBranding(ctx context.Context, org string) (portal.Branding, error) {
	b, err := one(ctx, p.S, org, func(r scanner) (portal.Branding, error) {
		var b portal.Branding
		err := r.Scan(&b.DisplayName, &b.AccentColor, &b.Logo, &b.LogoMime, &b.UpdatedBy, &b.UpdatedAt)
		return b, err
	}, `SELECT display_name, accent_color, logo, logo_mime, updated_by, updated_at FROM portal_branding WHERE org_id=$1`, org)
	return b, mapErr(err)
}

func (p *PortalStore) PutBranding(ctx context.Context, org string, b portal.Branding) error {
	var logo []byte
	if len(b.Logo) > 0 {
		logo = b.Logo
	}
	return p.S.exec(ctx, org, `INSERT INTO portal_branding (org_id, display_name, accent_color, logo, logo_mime, updated_by, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (org_id) DO UPDATE SET display_name=EXCLUDED.display_name, accent_color=EXCLUDED.accent_color, logo=EXCLUDED.logo,
			logo_mime=EXCLUDED.logo_mime, updated_by=EXCLUDED.updated_by, updated_at=EXCLUDED.updated_at`,
		org, b.DisplayName, b.AccentColor, logo, b.LogoMime, b.UpdatedBy, b.UpdatedAt)
}

const linkCols = `id, project_id, label, title, token_hash, deliverable_ids, created_by, created_at, updated_at, expires_at, revoked_at, revoked_by, first_viewed_at, last_viewed_at`

func scanLink(r scanner) (portal.Link, error) {
	var l portal.Link
	var ids []byte
	if err := r.Scan(&l.ID, &l.ProjectID, &l.Label, &l.Title, &l.TokenHash, &ids, &l.CreatedBy, &l.CreatedAt, &l.UpdatedAt, &l.ExpiresAt, &l.RevokedAt, &l.RevokedBy,
		&l.FirstViewedAt, &l.LastViewedAt); err != nil {
		return l, err
	}
	unmarshal(ids, &l.DeliverableIDs)
	l.DeliverableIDs = strs(l.DeliverableIDs)
	return l, nil
}

func (p *PortalStore) CreateLink(ctx context.Context, org string, l portal.Link) error {
	return p.S.exec(ctx, org, `INSERT INTO portal_links (org_id, `+linkCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		org, l.ID, l.ProjectID, l.Label, l.Title, l.TokenHash, jb(strs(l.DeliverableIDs)), l.CreatedBy, l.CreatedAt, l.UpdatedAt, l.ExpiresAt, l.RevokedAt, l.RevokedBy,
		l.FirstViewedAt, l.LastViewedAt)
}

func (p *PortalStore) UpdateLink(ctx context.Context, org string, l portal.Link) error {
	tag, err := p.S.execTag(ctx, org, `UPDATE portal_links SET label=$3, deliverable_ids=$4, updated_at=$5, title=$6 WHERE org_id=$1 AND id=$2`,
		org, l.ID, l.Label, jb(strs(l.DeliverableIDs)), l.UpdatedAt, l.Title)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (p *PortalStore) GetLink(ctx context.Context, org, id string) (portal.Link, error) {
	l, err := one(ctx, p.S, org, scanLink, `SELECT `+linkCols+` FROM portal_links WHERE org_id=$1 AND id=$2`, org, id)
	return l, mapErr(err)
}

func (p *PortalStore) ListLinks(ctx context.Context, org, projectID string) ([]portal.Link, error) {
	return many(ctx, p.S, org, scanLink, `SELECT `+linkCols+` FROM portal_links WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC, id DESC`, org, projectID)
}

func (p *PortalStore) RevokeLink(ctx context.Context, org, id, by string, at time.Time) error {
	return p.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE portal_links SET revoked_at=$4, revoked_by=$3 WHERE org_id=$1 AND id=$2 AND revoked_at IS NULL`, org, id, by, at)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM portal_links WHERE org_id=$1 AND id=$2)`, org, id).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return domain.ErrNotFound
			}
		}
		return nil
	})
}

// LinkByHash runs in a transaction that only announces the hash: the portal_lookup policy then shows that
// single row and nothing else (no tenant is set).
func (p *PortalStore) LinkByHash(ctx context.Context, hash string) (string, portal.Link, error) {
	var org string
	var l portal.Link
	err := pgx.BeginFunc(ctx, p.S.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.portal_hash', $1, true)`, hash); err != nil {
			return err
		}
		var err error
		l, err = scanLink(&orgScanner{row: tx.QueryRow(ctx, `SELECT org_id, `+linkCols+` FROM portal_links WHERE token_hash=$1`, hash), org: &org})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", portal.Link{}, domain.ErrNotFound
	}
	return org, l, err
}

// orgScanner reads a leading org_id column before the link columns.
type orgScanner struct {
	row scanner
	org *string
}

func (o *orgScanner) Scan(dest ...any) error { return o.row.Scan(append([]any{o.org}, dest...)...) }

func (p *PortalStore) MarkViewed(ctx context.Context, org, id string, at time.Time) (bool, error) {
	var first bool
	at = at.UTC().Truncate(time.Microsecond) // the precision of timestamptz, so the RETURNING comparison is exact
	err := p.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE portal_links SET first_viewed_at = COALESCE(first_viewed_at, $3), last_viewed_at = $3
			WHERE org_id=$1 AND id=$2 AND (last_viewed_at IS NULL OR last_viewed_at <= $3::timestamptz - interval '1 minute')
			RETURNING first_viewed_at = $3`, org, id, at).Scan(&first)
		if errors.Is(err, pgx.ErrNoRows) {
			first = false
			return nil
		}
		return err
	})
	return first, err
}
