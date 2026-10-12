package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/sandbox"
)

// CodeRunStore implements sandbox.Store on Postgres (migration 490). Every call
// runs through WithOrgTx (Row-Level Security). Status transitions lock the row
// and check the current status, so an approval starts a run at most once even
// with several backend replicas.
type CodeRunStore struct{ S *Store }

var _ sandbox.Store = (*CodeRunStore)(nil)

func (c *CodeRunStore) CreateRun(ctx context.Context, r sandbox.Run) error {
	return c.S.WithOrgTx(ctx, r.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO code_runs (org_id, id, approval_id, status, started_at, created_at, doc) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			r.OrgID, r.ID, r.ApprovalID, r.Status, r.StartedAt, r.CreatedAt, jb(r))
		return err
	})
}

func scanRun(row scanner) (sandbox.Run, error) {
	var raw []byte
	var r sandbox.Run
	if err := row.Scan(&raw); err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	return r, nil
}

func (c *CodeRunStore) GetRun(ctx context.Context, org, id string) (sandbox.Run, error) {
	r, err := one(ctx, c.S, org, scanRun, `SELECT doc FROM code_runs WHERE org_id=$1 AND id=$2`, org, id)
	return r, mapErr(err)
}

func (c *CodeRunStore) GetRunByApproval(ctx context.Context, org, approvalID string) (sandbox.Run, error) {
	r, err := one(ctx, c.S, org, scanRun, `SELECT doc FROM code_runs WHERE org_id=$1 AND approval_id=$2 ORDER BY created_at DESC LIMIT 1`, org, approvalID)
	return r, mapErr(err)
}

func (c *CodeRunStore) ListRuns(ctx context.Context, org string, limit int) ([]sandbox.Run, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return many(ctx, c.S, org, scanRun, `SELECT doc FROM code_runs WHERE org_id=$1 ORDER BY created_at DESC LIMIT $2`, org, limit)
}

// update locks the run, lets fn change it (ok=false: leave it as is) and saves it.
func (c *CodeRunStore) update(ctx context.Context, org, id string, fn func(r *sandbox.Run) (bool, error)) (bool, error) {
	changed := false
	err := c.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT doc FROM code_runs WHERE org_id=$1 AND id=$2 FOR UPDATE`, org, id).Scan(&raw); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			return err
		}
		var r sandbox.Run
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		ok, err := fn(&r)
		if err != nil || !ok {
			return err
		}
		changed = true
		_, err = tx.Exec(ctx, `UPDATE code_runs SET status=$3, started_at=$4, doc=$5 WHERE org_id=$1 AND id=$2`, org, id, r.Status, r.StartedAt, jb(r))
		return err
	})
	return changed, err
}

func (c *CodeRunStore) BeginRun(ctx context.Context, org, id, approvalID string, approvers []string, at time.Time) (bool, error) {
	return c.update(ctx, org, id, func(r *sandbox.Run) (bool, error) {
		if r.Status != sandbox.StatusAwaiting || r.ApprovalID != approvalID {
			return false, nil
		}
		r.Status, r.Approvers, r.StartedAt = sandbox.StatusRunning, append([]string(nil), approvers...), &at
		return true, nil
	})
}

func (c *CodeRunStore) FinishRun(ctx context.Context, org string, in sandbox.Run) error {
	ok, err := c.update(ctx, org, in.ID, func(r *sandbox.Run) (bool, error) {
		if r.Status != sandbox.StatusRunning {
			return false, domain.ErrConflict
		}
		r.Status, r.DenyReason, r.ExitCode, r.DurationMS = in.Status, in.DenyReason, in.ExitCode, in.DurationMS
		r.Stdout, r.Stderr, r.StdoutTruncated, r.StderrTruncated = in.Stdout, in.Stderr, in.StdoutTruncated, in.StderrTruncated
		r.Outputs, r.FinishedAt = in.Outputs, in.FinishedAt
		return true, nil
	})
	if err == nil && !ok {
		return domain.ErrConflict
	}
	return err
}

func (c *CodeRunStore) CloseRun(ctx context.Context, org, id, status, reason string, at time.Time) (bool, error) {
	return c.update(ctx, org, id, func(r *sandbox.Run) (bool, error) {
		if r.Status != sandbox.StatusAwaiting {
			return false, nil
		}
		r.Status, r.DenyReason, r.FinishedAt = status, reason, &at
		return true, nil
	})
}

func (c *CodeRunStore) CountStartedSince(ctx context.Context, org string, since time.Time) (int, error) {
	n := 0
	err := c.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM code_runs WHERE org_id=$1 AND started_at IS NOT NULL AND started_at >= $2`, org, since).Scan(&n)
	})
	return n, err
}

func (c *CodeRunStore) GetSettings(ctx context.Context, org string) (sandbox.Settings, error) {
	var s sandbox.Settings
	err := c.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT doc FROM code_settings WHERE org_id=$1`, org).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // no row: code execution is off
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &s)
	})
	return s, err
}

func (c *CodeRunStore) PutSettings(ctx context.Context, org string, s sandbox.Settings) error {
	return c.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO code_settings (org_id, doc, updated_at) VALUES ($1,$2,now())
			ON CONFLICT (org_id) DO UPDATE SET doc=EXCLUDED.doc, updated_at=now()`, org, jb(s))
		return err
	})
}
