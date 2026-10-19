package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application"
)

// HoldStore implements application.HoldStore on Postgres (migration 760). Row-Level Security applies.
type HoldStore struct{ S *Store }

var _ application.HoldStore = (*HoldStore)(nil)

// PutHold inserts or refreshes the hold of a task; created_at keeps the first value.
func (h *HoldStore) PutHold(ctx context.Context, org string, hd application.Hold) error {
	if hd.CreatedAt.IsZero() {
		hd.CreatedAt = time.Now().UTC()
	}
	return h.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO schedule_holds (org_id, task_id, request_id, agent_id, reason, wake_at, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (org_id, task_id) DO UPDATE SET request_id=EXCLUDED.request_id, agent_id=EXCLUDED.agent_id,
				reason=EXCLUDED.reason, wake_at=EXCLUDED.wake_at`,
			org, hd.TaskID, hd.RequestID, hd.AgentID, hd.Reason, hd.WakeAt, hd.CreatedAt)
		return err
	})
}

// DeleteHold removes the hold of a task; a missing hold is not an error.
func (h *HoldStore) DeleteHold(ctx context.Context, org, taskID string) error {
	_, err := h.S.execTag(ctx, org, `DELETE FROM schedule_holds WHERE org_id=$1 AND task_id=$2`, org, taskID)
	return err
}

// ListHolds returns the holds of the organization ordered by wake-up time.
func (h *HoldStore) ListHolds(ctx context.Context, org string) ([]application.Hold, error) {
	out := []application.Hold{}
	err := h.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT task_id, request_id, agent_id, reason, wake_at, created_at
			FROM schedule_holds WHERE org_id=$1 ORDER BY wake_at, task_id`, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var hd application.Hold
			if err := rows.Scan(&hd.TaskID, &hd.RequestID, &hd.AgentID, &hd.Reason, &hd.WakeAt, &hd.CreatedAt); err != nil {
				return err
			}
			out = append(out, hd)
		}
		return rows.Err()
	})
	return out, err
}
