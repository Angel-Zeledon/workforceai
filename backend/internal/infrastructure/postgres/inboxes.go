package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/inboxes"
)

// InboxStore implements inboxes.Store on Postgres (migration 803). Every call but InboxByHash runs through
// WithOrgTx, so Row-Level Security applies; InboxByHash announces only the token hash (inbox_lookup policy).
type InboxStore struct{ S *Store }

var _ inboxes.Store = (*InboxStore)(nil)

const inboxCols = `id, name, slug, folder, customer_id, allowed, max_bytes, rate_per_min, token_hash, token_hint, created_by, created_at,
	updated_at, rotated_at, revoked_at, revoked_by, last_push_at, push_count`

func scanInbox(r scanner) (inboxes.Inbox, error) {
	var i inboxes.Inbox
	var allowed []byte
	if err := r.Scan(&i.ID, &i.Name, &i.Slug, &i.Folder, &i.CustomerID, &allowed, &i.MaxBytes, &i.RatePerMin, &i.TokenHash, &i.TokenHint,
		&i.CreatedBy, &i.CreatedAt, &i.UpdatedAt, &i.RotatedAt, &i.RevokedAt, &i.RevokedBy, &i.LastPushAt, &i.PushCount); err != nil {
		return i, err
	}
	unmarshal(allowed, &i.Allowed)
	i.Allowed = strs(i.Allowed)
	return i, nil
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func (p *InboxStore) CreateInbox(ctx context.Context, org string, i inboxes.Inbox) error {
	err := p.S.exec(ctx, org, `INSERT INTO data_inboxes (org_id, `+inboxCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		org, i.ID, i.Name, i.Slug, i.Folder, i.CustomerID, jb(strs(i.Allowed)), i.MaxBytes, i.RatePerMin, i.TokenHash, i.TokenHint, i.CreatedBy,
		i.CreatedAt, i.UpdatedAt, i.RotatedAt, i.RevokedAt, i.RevokedBy, i.LastPushAt, i.PushCount)
	if isUniqueViolation(err) {
		return domain.ErrConflict
	}
	return err
}

func (p *InboxStore) UpdateInbox(ctx context.Context, org string, i inboxes.Inbox) error {
	tag, err := p.S.execTag(ctx, org, `UPDATE data_inboxes SET name=$3, allowed=$4, max_bytes=$5, rate_per_min=$6, updated_at=$7 WHERE org_id=$1 AND id=$2`,
		org, i.ID, i.Name, jb(strs(i.Allowed)), i.MaxBytes, i.RatePerMin, i.UpdatedAt)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (p *InboxStore) GetInbox(ctx context.Context, org, id string) (inboxes.Inbox, error) {
	i, err := one(ctx, p.S, org, scanInbox, `SELECT `+inboxCols+` FROM data_inboxes WHERE org_id=$1 AND id=$2`, org, id)
	return i, mapErr(err)
}

func (p *InboxStore) ListInboxes(ctx context.Context, org string) ([]inboxes.Inbox, error) {
	return many(ctx, p.S, org, scanInbox, `SELECT `+inboxCols+` FROM data_inboxes WHERE org_id=$1 ORDER BY created_at DESC, id DESC`, org)
}

func (p *InboxStore) SetToken(ctx context.Context, org, id, hash, hint string, at time.Time) error {
	tag, err := p.S.execTag(ctx, org, `UPDATE data_inboxes SET token_hash=$3, token_hint=$4, rotated_at=$5, updated_at=$5 WHERE org_id=$1 AND id=$2`,
		org, id, hash, hint, at)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (p *InboxStore) RevokeInbox(ctx context.Context, org, id, by string, at time.Time) error {
	return p.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE data_inboxes SET revoked_at=$4, revoked_by=$3 WHERE org_id=$1 AND id=$2 AND revoked_at IS NULL`, org, id, by, at)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM data_inboxes WHERE org_id=$1 AND id=$2)`, org, id).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return domain.ErrNotFound
			}
		}
		return nil
	})
}

// InboxByHash runs in a transaction that only announces the hash: the inbox_lookup policy then shows that single
// row and nothing else (no tenant is set).
func (p *InboxStore) InboxByHash(ctx context.Context, hash string) (string, inboxes.Inbox, error) {
	var org string
	var i inboxes.Inbox
	err := pgx.BeginFunc(ctx, p.S.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.inbox_hash', $1, true)`, hash); err != nil {
			return err
		}
		var err error
		i, err = scanInbox(&orgScanner{row: tx.QueryRow(ctx, `SELECT org_id, `+inboxCols+` FROM data_inboxes WHERE token_hash=$1`, hash), org: &org})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", inboxes.Inbox{}, domain.ErrNotFound
	}
	return org, i, err
}

const pushCols = `id, inbox_id, idem_key, status, size_bytes, files, received_at`

func scanPush(r scanner) (inboxes.Push, error) {
	var p inboxes.Push
	var files []byte
	if err := r.Scan(&p.ID, &p.InboxID, &p.IdemKey, &p.Status, &p.Size, &files, &p.ReceivedAt); err != nil {
		return p, err
	}
	unmarshal(files, &p.Files)
	if p.Files == nil {
		p.Files = []inboxes.PushedFile{}
	}
	return p, nil
}

func (p *InboxStore) ReservePush(ctx context.Context, org string, ps inboxes.Push) error {
	err := p.S.exec(ctx, org, `INSERT INTO data_inbox_pushes (org_id, `+pushCols+`) VALUES ($1,$2,$3,$4,'pending',$5,'[]',$6)`,
		org, ps.ID, ps.InboxID, ps.IdemKey, ps.Size, ps.ReceivedAt)
	if isUniqueViolation(err) {
		return domain.ErrConflict
	}
	return err
}

func (p *InboxStore) CompletePush(ctx context.Context, org string, ps inboxes.Push) error {
	files := ps.Files
	if files == nil {
		files = []inboxes.PushedFile{}
	}
	return p.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE data_inbox_pushes SET status='done', files=$4, size_bytes=$5 WHERE org_id=$1 AND inbox_id=$2 AND id=$3`,
			org, ps.InboxID, ps.ID, jb(files), ps.Size)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		_, err = tx.Exec(ctx, `UPDATE data_inboxes SET last_push_at=GREATEST(COALESCE(last_push_at, $3), $3), push_count=push_count+1
			WHERE org_id=$1 AND id=$2`, org, ps.InboxID, ps.ReceivedAt)
		return err
	})
}

func (p *InboxStore) ReleasePush(ctx context.Context, org, inboxID, id string) error {
	return p.S.exec(ctx, org, `DELETE FROM data_inbox_pushes WHERE org_id=$1 AND inbox_id=$2 AND id=$3 AND status='pending'`, org, inboxID, id)
}

func (p *InboxStore) PushByKey(ctx context.Context, org, inboxID, idemKey string) (inboxes.Push, error) {
	if idemKey == "" {
		return inboxes.Push{}, domain.ErrNotFound
	}
	ps, err := one(ctx, p.S, org, scanPush, `SELECT `+pushCols+` FROM data_inbox_pushes WHERE org_id=$1 AND inbox_id=$2 AND idem_key=$3`, org, inboxID, idemKey)
	return ps, mapErr(err)
}

func (p *InboxStore) Pushes(ctx context.Context, org, inboxID string, limit int) ([]inboxes.Push, error) {
	if limit <= 0 {
		limit = inboxes.MaxPushesListed
	}
	return many(ctx, p.S, org, scanPush, `SELECT `+pushCols+` FROM data_inbox_pushes WHERE org_id=$1 AND inbox_id=$2 AND status='done'
		ORDER BY received_at DESC, id DESC LIMIT $3`, org, inboxID, limit)
}
