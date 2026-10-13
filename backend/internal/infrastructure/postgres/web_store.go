package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/webtool"
)

// WebStore implements webtool.Store on Postgres (migration 685): the per-organization web setting and the call
// ledger (query or URL only, never content) that backs the daily and per-task quotas. Row-Level Security applies.
type WebStore struct{ S *Store }

var _ webtool.Store = (*WebStore)(nil)

func (w *WebStore) GetSettings(ctx context.Context, org string) (webtool.Settings, error) {
	var s webtool.Settings
	err := w.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT doc FROM web_settings WHERE org_id=$1`, org).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // no row: the web tool is off
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &s)
	})
	return s, err
}

func (w *WebStore) PutSettings(ctx context.Context, org string, s webtool.Settings) error {
	return w.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO web_settings (org_id, doc, updated_at) VALUES ($1,$2,now())
			ON CONFLICT (org_id) DO UPDATE SET doc=EXCLUDED.doc, updated_at=now()`, org, jb(s))
		return err
	})
}

func (w *WebStore) RecordCall(ctx context.Context, org string, c webtool.Call) error {
	return w.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO web_calls (org_id, id, agent_id, task_id, action, target, at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			org, c.ID, c.AgentID, c.TaskID, c.Action, c.Target, c.At)
		return err
	})
}

func (w *WebStore) CountSince(ctx context.Context, org string, since time.Time) (search, fetch int, err error) {
	err = w.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE action='search'), count(*) FILTER (WHERE action<>'search')
			FROM web_calls WHERE org_id=$1 AND at >= $2`, org, since).Scan(&search, &fetch)
	})
	return search, fetch, err
}

func (w *WebStore) CountTask(ctx context.Context, org, taskID string) (int, error) {
	n := 0
	err := w.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM web_calls WHERE org_id=$1 AND task_id=$2`, org, taskID).Scan(&n)
	})
	return n, err
}
