package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// OrgTx runs fn in a transaction bound to one organization (RLS). postgres.Store.WithOrgTx has this shape.
type OrgTx interface {
	WithOrgTx(ctx context.Context, orgID string, fn func(tx pgx.Tx) error) error
}

// PgStore implements Store on Postgres (migration 711).
type PgStore struct{ DB OrgTx }

var _ Store = (*PgStore)(nil)

func (p *PgStore) PutEndpoint(ctx context.Context, org string, e Endpoint, sealed []byte) error {
	ev, _ := json.Marshal(e.Events)
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO webhook_endpoints (org_id, id, url, description, events, secret_enc, secret_hint, active, created_by, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, org, e.ID, e.URL, e.Description, ev, sealed, e.SecretHint, e.Active, e.CreatedBy, e.CreatedAt)
		return err
	})
}

const epCols = `id, url, description, events, active, disabled_reason, secret_hint, failure_streak, created_by, created_at`

func scanEndpoint(row pgx.Row, extra ...any) (Endpoint, error) {
	var e Endpoint
	var ev []byte
	dest := append([]any{&e.ID, &e.URL, &e.Description, &ev, &e.Active, &e.DisabledReason, &e.SecretHint, &e.FailureStreak, &e.CreatedBy, &e.CreatedAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return e, err
	}
	_ = json.Unmarshal(ev, &e.Events)
	if e.Events == nil {
		e.Events = []string{}
	}
	return e, nil
}

func (p *PgStore) ListEndpoints(ctx context.Context, org string) ([]Endpoint, error) {
	out := []Endpoint{}
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+epCols+` FROM webhook_endpoints WHERE org_id=$1 ORDER BY created_at`, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEndpoint(rows)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

func (p *PgStore) GetEndpoint(ctx context.Context, org, id string) (Endpoint, []byte, error) {
	var e Endpoint
	var sealed []byte
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var err error
		e, err = scanEndpoint(tx.QueryRow(ctx, `SELECT `+epCols+`, secret_enc FROM webhook_endpoints WHERE org_id=$1 AND id=$2`, org, id), &sealed)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return e, nil, ErrNotFound
	}
	return e, sealed, err
}

func (p *PgStore) SetActive(ctx context.Context, org, id string, active bool, reason string) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE webhook_endpoints SET active=$3, disabled_reason=$4, failure_streak = CASE WHEN $3 THEN 0 ELSE failure_streak END
			WHERE org_id=$1 AND id=$2`, org, id, active, reason)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

func (p *PgStore) DeleteEndpoint(ctx context.Context, org, id string) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM webhook_endpoints WHERE org_id=$1 AND id=$2`, org, id)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}

func (p *PgStore) AddDeliveries(ctx context.Context, org string, ds []Delivery) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		for _, d := range ds {
			if _, err := tx.Exec(ctx, `INSERT INTO webhook_deliveries (org_id, id, endpoint_id, event, payload, status, next_attempt_at, created_at)
				VALUES ($1,$2,$3,$4,$5,'pending',$6,$7)`, org, d.ID, d.EndpointID, d.Event, []byte(d.Payload), d.NextAttemptAt, d.CreatedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

const dCols = `id, endpoint_id, event, payload, status, attempts, next_attempt_at, last_status, last_error, created_at, delivered_at`

func scanDelivery(row pgx.Row) (Delivery, error) {
	var d Delivery
	var payload []byte
	err := row.Scan(&d.ID, &d.EndpointID, &d.Event, &payload, &d.Status, &d.Attempts, &d.NextAttemptAt, &d.LastStatus, &d.LastError, &d.CreatedAt, &d.DeliveredAt)
	d.Payload = payload
	return d, err
}

func (p *PgStore) Claim(ctx context.Context, org string, now time.Time, limit int, lease time.Duration) ([]Delivery, error) {
	var out []Delivery
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE webhook_deliveries SET next_attempt_at = $2::timestamptz + make_interval(secs => $4)
			WHERE org_id=$1 AND id IN (
				SELECT id FROM webhook_deliveries WHERE org_id=$1 AND status='pending' AND next_attempt_at <= $2
				ORDER BY next_attempt_at LIMIT $3 FOR UPDATE SKIP LOCKED)
			RETURNING `+dCols, org, now, limit, lease.Seconds())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			d, err := scanDelivery(rows)
			if err != nil {
				return err
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, err
}

func (p *PgStore) Record(ctx context.Context, org, id string, r Result) error {
	return p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var ep string
		err := tx.QueryRow(ctx, `UPDATE webhook_deliveries SET status=$3, attempts=$4,
				next_attempt_at = CASE WHEN $3='pending' THEN $5 ELSE next_attempt_at END,
				last_status=$6, last_error=$7, delivered_at=$8
			WHERE org_id=$1 AND id=$2 RETURNING endpoint_id`, org, id, r.Status, r.Attempts, r.Next, r.HTTPStatus, r.Err, r.DeliveredAt).Scan(&ep)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if r.Status == StatusDelivered {
			_, err = tx.Exec(ctx, `UPDATE webhook_endpoints SET failure_streak=0 WHERE org_id=$1 AND id=$2`, org, ep)
		} else {
			_, err = tx.Exec(ctx, `UPDATE webhook_endpoints SET failure_streak=failure_streak+1,
					active = active AND failure_streak+1 < $3,
					disabled_reason = CASE WHEN active AND failure_streak+1 >= $3 THEN 'too_many_failures' ELSE disabled_reason END
				WHERE org_id=$1 AND id=$2`, org, ep, FailureStreakOff)
		}
		return err
	})
}

func (p *PgStore) HasPending(ctx context.Context, org string) (bool, error) {
	var ok bool
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM webhook_deliveries WHERE org_id=$1 AND status='pending')`, org).Scan(&ok)
	})
	return ok, err
}

func (p *PgStore) ListDeliveries(ctx context.Context, org, endpointID string, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := []Delivery{}
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+dCols+` FROM webhook_deliveries WHERE org_id=$1 AND ($2='' OR endpoint_id=$2) ORDER BY created_at DESC LIMIT $3`, org, endpointID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			d, err := scanDelivery(rows)
			if err != nil {
				return err
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, err
}

func (p *PgStore) Prune(ctx context.Context, org string, before time.Time) (int, error) {
	n := 0
	err := p.DB.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM webhook_deliveries WHERE org_id=$1 AND status IN ('delivered','dead') AND created_at < $2`, org, before)
		n = int(tag.RowsAffected())
		return err
	})
	return n, err
}
