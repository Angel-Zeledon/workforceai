package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/away"
)

// Last-seen marks of the "while you were away" summary (migration 792, docs/architecture/away-summary.md).

var (
	_ away.MarkStore = (*Store)(nil)
	_ away.History   = (*Store)(nil)
)

// GetAwayMark returns the mark of one person in one organization (found=false when there is none).
func (s *Store) GetAwayMark(ctx context.Context, orgID, userID string) (away.Mark, bool, error) {
	m, err := one(ctx, s, orgID, func(r scanner) (away.Mark, error) {
		var m away.Mark
		err := r.Scan(&m.LastSeenAt, &m.VisitStartedAt, &m.AwaySince, &m.DismissedAt)
		return m, err
	}, `SELECT last_seen_at, visit_started_at, away_since, dismissed_at FROM away_marks WHERE org_id=$1 AND user_id=$2`, orgID, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return away.Mark{}, false, nil
	}
	if err != nil {
		return away.Mark{}, false, err
	}
	toUTC(&m)
	return m, true, nil
}

// PutAwayMark upserts the mark.
func (s *Store) PutAwayMark(ctx context.Context, orgID, userID string, m away.Mark) error {
	return s.exec(ctx, orgID, `INSERT INTO away_marks (org_id, user_id, last_seen_at, visit_started_at, away_since, dismissed_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,now())
		ON CONFLICT (org_id, user_id) DO UPDATE SET last_seen_at=EXCLUDED.last_seen_at, visit_started_at=EXCLUDED.visit_started_at,
			away_since=EXCLUDED.away_since, dismissed_at=EXCLUDED.dismissed_at, updated_at=now()`,
		orgID, userID, m.LastSeenAt, m.VisitStartedAt, m.AwaySince, m.DismissedAt)
}

func toUTC(m *away.Mark) {
	m.LastSeenAt, m.VisitStartedAt = m.LastSeenAt.UTC(), m.VisitStartedAt.UTC()
	if m.AwaySince != nil {
		t := m.AwaySince.UTC()
		m.AwaySince = &t
	}
	if m.DismissedAt != nil {
		t := m.DismissedAt.UTC()
		m.DismissedAt = &t
	}
}
