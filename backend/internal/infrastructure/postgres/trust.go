package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/trust"
)

// TrustStore implements trust.Store (records and proposals, migration 791) and trust.Signals (the evidence, read
// from approvals, tasks and audit_logs) on Postgres. Every call runs through WithOrgTx, so Row-Level Security
// applies.
type TrustStore struct{ S *Store }

var (
	_ trust.Store   = (*TrustStore)(nil)
	_ trust.Signals = (*TrustStore)(nil)
)

func (q *TrustStore) GetRecord(ctx context.Context, org, agentID string) (trust.Record, bool, error) {
	r, err := one(ctx, q.S, org, func(s scanner) (trust.Record, error) {
		var r trust.Record
		err := s.Scan(&r.AgentID, &r.StartLevel, &r.Level, &r.LevelSince, &r.TrackingSince, &r.UpdatedAt)
		return r, err
	}, `SELECT agent_id, start_level, level, level_since, tracking_since, updated_at FROM agent_trust WHERE org_id=$1 AND agent_id=$2`, org, agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return trust.Record{}, false, nil
	}
	return r, err == nil, err
}

func (q *TrustStore) PutRecord(ctx context.Context, org string, r trust.Record) error {
	return q.S.exec(ctx, org, `INSERT INTO agent_trust (org_id, agent_id, start_level, level, level_since, tracking_since, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (org_id, agent_id) DO UPDATE SET level=EXCLUDED.level, level_since=EXCLUDED.level_since, updated_at=EXCLUDED.updated_at`,
		org, r.AgentID, r.StartLevel, r.Level, r.LevelSince, r.TrackingSince, r.UpdatedAt)
}

func (q *TrustStore) ListRecords(ctx context.Context, org string) ([]trust.Record, error) {
	return many(ctx, q.S, org, func(s scanner) (trust.Record, error) {
		var r trust.Record
		err := s.Scan(&r.AgentID, &r.StartLevel, &r.Level, &r.LevelSince, &r.TrackingSince, &r.UpdatedAt)
		return r, err
	}, `SELECT agent_id, start_level, level, level_since, tracking_since, updated_at FROM agent_trust WHERE org_id=$1 ORDER BY agent_id`, org)
}

const proposalCols = `id, agent_id, kind, from_level, to_level, status, auto, reasons, evidence, created_at, resolved_at, resolved_by, note`

func scanProposal(s scanner) (trust.Proposal, error) {
	var p trust.Proposal
	var reasons, ev []byte
	if err := s.Scan(&p.ID, &p.AgentID, &p.Kind, &p.FromLevel, &p.ToLevel, &p.Status, &p.Auto, &reasons, &ev, &p.CreatedAt, &p.ResolvedAt, &p.ResolvedBy, &p.Note); err != nil {
		return p, err
	}
	unmarshal(reasons, &p.Reasons)
	unmarshal(ev, &p.Evidence)
	if p.Reasons == nil {
		p.Reasons = []string{}
	}
	return p, nil
}

func (q *TrustStore) CreateProposal(ctx context.Context, org string, p trust.Proposal) error {
	if p.Reasons == nil {
		p.Reasons = []string{}
	}
	return q.S.exec(ctx, org, `INSERT INTO trust_proposals (org_id, `+proposalCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		org, p.ID, p.AgentID, p.Kind, p.FromLevel, p.ToLevel, p.Status, p.Auto, jb(p.Reasons), jb(p.Evidence), p.CreatedAt, p.ResolvedAt, p.ResolvedBy, p.Note)
}

func (q *TrustStore) GetProposal(ctx context.Context, org, id string) (trust.Proposal, error) {
	p, err := one(ctx, q.S, org, scanProposal, `SELECT `+proposalCols+` FROM trust_proposals WHERE org_id=$1 AND id=$2`, org, id)
	return p, mapErr(err)
}

// ResolveProposal is a compare-and-set on the status. Moving back to pending clears the resolution.
func (q *TrustStore) ResolveProposal(ctx context.Context, org, id, from, to, by, note string, at time.Time) (trust.Proposal, error) {
	var resolved *time.Time
	if to != trust.StatusPending {
		resolved = &at
	}
	p, err := one(ctx, q.S, org, scanProposal, `UPDATE trust_proposals SET status=$4, resolved_at=$5, resolved_by=$6, note=$7
		WHERE org_id=$1 AND id=$2 AND status=$3 RETURNING `+proposalCols, org, id, from, to, resolved, by, note)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, gerr := q.GetProposal(ctx, org, id); gerr != nil {
			return trust.Proposal{}, gerr
		}
		return trust.Proposal{}, fmt.Errorf("%w: proposal is not %s", domain.ErrConflict, from)
	}
	return p, err
}

func (q *TrustStore) ListProposals(ctx context.Context, org, agentID, status string, limit int) ([]trust.Proposal, error) {
	if limit <= 0 {
		limit = 50
	}
	return many(ctx, q.S, org, scanProposal, `SELECT `+proposalCols+` FROM trust_proposals
		WHERE org_id=$1 AND ($2='' OR agent_id=$2) AND ($3='' OR status=$3) ORDER BY created_at DESC, id DESC LIMIT $4`, org, agentID, status, limit)
}

func (q *TrustStore) Reset(ctx context.Context, org string) error {
	return q.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM trust_proposals WHERE org_id=$1`, org); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM agent_trust WHERE org_id=$1`, org)
		return err
	})
}

// Collect implements trust.Signals with the same definitions as trust.StoreSignals: evidence strictly after since,
// system rejections (timeout, restart) not held against the agent, high risk from approvals.risk, and only
// tool.denied entries whose reason names a policy rule ("policy:...").
func (q *TrustStore) Collect(ctx context.Context, org, agentID string, since, burstSince time.Time) (trust.Counts, error) {
	var c trust.Counts
	err := q.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT
				count(*) FILTER (WHERE status = 'approved'),
				count(*) FILTER (WHERE human),
				count(*) FILTER (WHERE human AND lower(risk) = 'high'),
				count(*) FILTER (WHERE human AND resolved_at >= $4)
			FROM (SELECT status, risk, resolved_at,
					status = 'rejected' AND lower(note) NOT LIKE 'sin respuesta%' AND lower(note) NOT LIKE 'superseded:%' AS human
				FROM approvals
				WHERE org_id=$1 AND agent_id=$2 AND status IN ('approved','rejected') AND resolved_at > $3) a`,
			org, agentID, since, burstSince).Scan(&c.Approved, &c.Rejected, &c.HighRiskRejected, &c.RecentRejected); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT
				count(*) FILTER (WHERE status = 'done'),
				count(*) FILTER (WHERE status = 'failed'),
				count(*) FILTER (WHERE status = 'blocked')
			FROM tasks
			WHERE org_id=$1 AND agent_id=$2 AND status IN ('done','failed','blocked') AND COALESCE(finished_at, started_at, created_at) > $3`,
			org, agentID, since).Scan(&c.TasksDone, &c.TasksFailed, &c.TasksBlocked); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_logs
			WHERE org_id=$1 AND actor=$2 AND action='tool.denied' AND ts > $3 AND COALESCE(details->>'reason','') LIKE $4`,
			org, agentID, since, trust.PolicyDenialReasonPrefix+"%").Scan(&c.PolicyDenials)
	})
	return c, err
}
