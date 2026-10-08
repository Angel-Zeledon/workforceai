package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/push"
)

// PushStore implements push.Store on Postgres (migration 330). Every call runs
// through WithOrgTx, so Row-Level Security isolates organizations.
type PushStore struct{ S *Store }

var _ push.Store = (*PushStore)(nil)

func (p *PushStore) SavePushSubscription(ctx context.Context, s push.Subscription) (push.Subscription, error) {
	err := p.S.WithOrgTx(ctx, s.OrgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO push_subscriptions (id, org_id, user_id, endpoint, p256dh, auth, user_agent, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (org_id, endpoint) DO UPDATE SET user_id = EXCLUDED.user_id, p256dh = EXCLUDED.p256dh,
				auth = EXCLUDED.auth, user_agent = EXCLUDED.user_agent
			RETURNING id, created_at`,
			s.ID, s.OrgID, s.UserID, s.Endpoint, s.P256dh, s.Auth, s.UserAgent, s.CreatedAt).Scan(&s.ID, &s.CreatedAt)
	})
	return s, err
}

func (p *PushStore) DeletePushSubscription(ctx context.Context, orgID, userID, endpoint string) error {
	return p.S.exec(ctx, orgID, `DELETE FROM push_subscriptions WHERE org_id = $1 AND endpoint = $2 AND ($3 = '' OR user_id = $3)`,
		orgID, endpoint, userID)
}

func (p *PushStore) ListPushSubscriptions(ctx context.Context, orgID string) ([]push.Subscription, error) {
	return many(ctx, p.S, orgID, func(r scanner) (push.Subscription, error) {
		var s push.Subscription
		err := r.Scan(&s.ID, &s.OrgID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth, &s.UserAgent, &s.CreatedAt)
		return s, err
	}, `SELECT id, org_id, user_id, endpoint, p256dh, auth, user_agent, created_at FROM push_subscriptions WHERE org_id = $1 ORDER BY created_at`, orgID)
}
