package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/quality"
)

// QualityStore implements quality.Store (golden cases and runs, migration 789) and quality.Signals
// (the score inputs, read from approvals, tasks and events) on Postgres. Every call runs through
// WithOrgTx, so Row-Level Security applies.
type QualityStore struct{ S *Store }

var (
	_ quality.Store   = (*QualityStore)(nil)
	_ quality.Signals = (*QualityStore)(nil)
)

const caseCols = `id, agent_id, name, input, checks, active, created_by, created_at, updated_at`

func scanCase(r scanner) (quality.Case, error) {
	var c quality.Case
	var checks []byte
	if err := r.Scan(&c.ID, &c.AgentID, &c.Name, &c.Input, &checks, &c.Active, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return c, err
	}
	unmarshal(checks, &c.Checks)
	if c.Checks == nil {
		c.Checks = []quality.Check{}
	}
	return c, nil
}

func (q *QualityStore) CreateCase(ctx context.Context, org string, c quality.Case) error {
	return q.S.exec(ctx, org, `INSERT INTO quality_golden_cases (org_id, `+caseCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		org, c.ID, c.AgentID, c.Name, c.Input, jb(c.Checks), c.Active, c.CreatedBy, c.CreatedAt, c.UpdatedAt)
}

func (q *QualityStore) GetCase(ctx context.Context, org, id string) (quality.Case, error) {
	c, err := one(ctx, q.S, org, scanCase, `SELECT `+caseCols+` FROM quality_golden_cases WHERE org_id=$1 AND id=$2`, org, id)
	return c, mapErr(err)
}

func (q *QualityStore) UpdateCase(ctx context.Context, org string, c quality.Case) error {
	tag, err := q.S.execTag(ctx, org, `UPDATE quality_golden_cases SET name=$3, input=$4, checks=$5, active=$6, updated_at=$7 WHERE org_id=$1 AND id=$2`,
		org, c.ID, c.Name, c.Input, jb(c.Checks), c.Active, c.UpdatedAt)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (q *QualityStore) DeleteCase(ctx context.Context, org, id string) error {
	tag, err := q.S.execTag(ctx, org, `DELETE FROM quality_golden_cases WHERE org_id=$1 AND id=$2`, org, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (q *QualityStore) ListCases(ctx context.Context, org, agentID string) ([]quality.Case, error) {
	return many(ctx, q.S, org, scanCase, `SELECT `+caseCols+` FROM quality_golden_cases WHERE org_id=$1 AND ($2='' OR agent_id=$2) ORDER BY created_at, id`, org, agentID)
}

func (q *QualityStore) AddRun(ctx context.Context, org string, r quality.Run) error {
	return q.S.exec(ctx, org, `INSERT INTO quality_golden_runs (org_id, id, case_id, agent_id, passed, checks_passed, checks_total, task_id, note, recorded_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		org, r.ID, r.CaseID, r.AgentID, r.Passed, r.ChecksPassed, r.ChecksTotal, r.TaskID, r.Note, r.RecordedBy, r.CreatedAt)
}

func (q *QualityStore) ListRuns(ctx context.Context, org, caseID string, limit int) ([]quality.Run, error) {
	return many(ctx, q.S, org, func(r scanner) (quality.Run, error) {
		var x quality.Run
		err := r.Scan(&x.ID, &x.CaseID, &x.AgentID, &x.Passed, &x.ChecksPassed, &x.ChecksTotal, &x.TaskID, &x.Note, &x.RecordedBy, &x.CreatedAt)
		return x, err
	}, `SELECT id, case_id, agent_id, passed, checks_passed, checks_total, task_id, note, recorded_by, created_at
		FROM quality_golden_runs WHERE org_id=$1 AND case_id=$2 ORDER BY created_at DESC, id LIMIT $3`, org, caseID, limit)
}

func (q *QualityStore) GoldenCounts(ctx context.Context, org string, from, to time.Time) (map[string]quality.GoldenCount, error) {
	out := map[string]quality.GoldenCount{}
	err := q.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT agent_id, count(*), count(*) FILTER (WHERE passed) FROM quality_golden_runs
			WHERE org_id=$1 AND created_at >= $2 AND created_at < $3 GROUP BY agent_id`, org, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var g quality.GoldenCount
			if err := rows.Scan(&id, &g.Runs, &g.Passed); err != nil {
				return err
			}
			out[id] = g
		}
		return rows.Err()
	})
	return out, err
}

// Collect implements quality.Signals with three grouped queries per range over tables that already
// exist. System rejections (timeout, restart) are not counted against the agent, matching
// quality.SystemRejection; review fields come from the output the Q1 review wrote on the task.
func (q *QualityStore) Collect(ctx context.Context, org string, ranges []quality.Range) ([]map[string]quality.Counts, error) {
	out := make([]map[string]quality.Counts, len(ranges))
	err := q.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		for i, r := range ranges {
			m := map[string]quality.Counts{}
			out[i] = m
			rows, err := tx.Query(ctx, `SELECT agent_id,
					count(*) FILTER (WHERE status = 'approved'),
					count(*) FILTER (WHERE status = 'rejected' AND lower(note) NOT LIKE 'sin respuesta%' AND lower(note) NOT LIKE 'superseded:%')
				FROM approvals
				WHERE org_id=$1 AND status IN ('approved','rejected') AND resolved_at >= $2 AND resolved_at < $3
				GROUP BY agent_id`, org, r.From, r.To)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id string
				var c quality.Counts
				if err := rows.Scan(&id, &c.Approved, &c.Rejected); err != nil {
					rows.Close()
					return err
				}
				m[id] = c
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}

			rows, err = tx.Query(ctx, `SELECT agent_id,
					count(*) FILTER (WHERE status = 'done'),
					count(*) FILTER (WHERE status = 'failed'),
					count(*) FILTER (WHERE COALESCE(output #>> '{review,verdict}', '') <> ''),
					count(*) FILTER (WHERE output #>> '{review,verdict}' = 'pass' AND COALESCE(NULLIF(output #>> '{review,reworks}', '')::int, 0) = 0)
				FROM tasks
				WHERE org_id=$1 AND status IN ('done','failed') AND COALESCE(finished_at, started_at, created_at) >= $2 AND COALESCE(finished_at, started_at, created_at) < $3
				GROUP BY agent_id`, org, r.From, r.To)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id string
				var done, failed, reviewed, first int
				if err := rows.Scan(&id, &done, &failed, &reviewed, &first); err != nil {
					rows.Close()
					return err
				}
				c := m[id]
				c.TasksDone, c.TasksFailed, c.Reviewed, c.ReviewFirstPass = done, failed, reviewed, first
				m[id] = c
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}

			rows, err = tx.Query(ctx, `SELECT agent_id, count(*) FROM events
				WHERE org_id=$1 AND type = 'task.retrying' AND agent_id IS NOT NULL AND ts >= $2 AND ts < $3 GROUP BY agent_id`, org, r.From, r.To)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id string
				var n int
				if err := rows.Scan(&id, &n); err != nil {
					rows.Close()
					return err
				}
				c := m[id]
				c.Retries = n
				m[id] = c
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			// Retries are tracked for every agent in Postgres, so an agent with finished tasks and no event
			// has genuinely zero retries.
			for id, c := range m {
				c.RetriesTracked = true
				m[id] = c
			}
		}
		return nil
	})
	return out, err
}
