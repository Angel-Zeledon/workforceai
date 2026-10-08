package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/counters"
)

// Persistent windowed counters (internal/counters; migration 360_window_counters.sql).

var _ counters.Store = (*Store)(nil)

// LoadCounters implements counters.Store.
func (s *Store) LoadCounters(ctx context.Context, orgID, scope string) ([]byte, error) {
	var b []byte
	err := s.WithOrgTx(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT state FROM window_counters WHERE org_id=$1 AND scope=$2`, orgID, scope).Scan(&b)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

// SaveCounters implements counters.Store.
func (s *Store) SaveCounters(ctx context.Context, orgID, scope string, state []byte) error {
	return s.exec(ctx, orgID, `INSERT INTO window_counters (org_id, scope, state, updated_at) VALUES ($1,$2,$3,now())
		ON CONFLICT (org_id, scope) DO UPDATE SET state=EXCLUDED.state, updated_at=now()`, orgID, scope, state)
}
