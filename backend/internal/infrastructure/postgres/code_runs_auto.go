package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/sandbox"
)

var _ sandbox.TaskRunCounter = (*CodeRunStore)(nil)

// CountAutoRunsOfTask implements sandbox.TaskRunCounter: the desk-mode runs
// (no human approval) of one task that started. Under RLS like every query.
func (c *CodeRunStore) CountAutoRunsOfTask(ctx context.Context, org, taskID string) (int, error) {
	n := 0
	err := c.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM code_runs WHERE org_id=$1 AND started_at IS NOT NULL
			AND doc->>'task_id'=$2 AND doc->>'auto_run'='true'`, org, taskID).Scan(&n)
	})
	return n, err
}
