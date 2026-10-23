package postgres

import (
	"context"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.RehearsalStore = (*Store)(nil)

// Rehearsal (B9, migration 794): per-agent switches and the "would have done" log.

func scanAgentRehearsal(r scanner) (domain.AgentRehearsal, error) {
	var a domain.AgentRehearsal
	err := r.Scan(&a.AgentID, &a.Enabled, &a.SetBy, &a.UpdatedAt)
	return a, err
}

func (s *Store) GetAgentRehearsal(ctx context.Context, orgID, agentID string) (domain.AgentRehearsal, error) {
	a, err := one(ctx, s, orgID, scanAgentRehearsal, `SELECT agent_id, enabled, set_by, updated_at FROM agent_rehearsal WHERE org_id=$1 AND agent_id=$2`, orgID, agentID)
	return a, mapErr(err)
}

func (s *Store) ListAgentRehearsals(ctx context.Context, orgID string) ([]domain.AgentRehearsal, error) {
	return many(ctx, s, orgID, scanAgentRehearsal, `SELECT agent_id, enabled, set_by, updated_at FROM agent_rehearsal WHERE org_id=$1 ORDER BY agent_id`, orgID)
}

func (s *Store) PutAgentRehearsal(ctx context.Context, orgID string, r domain.AgentRehearsal) error {
	return s.exec(ctx, orgID, `INSERT INTO agent_rehearsal (org_id, agent_id, enabled, set_by, updated_at) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (org_id, agent_id) DO UPDATE SET enabled=EXCLUDED.enabled, set_by=EXCLUDED.set_by, updated_at=EXCLUDED.updated_at`,
		orgID, r.AgentID, r.Enabled, r.SetBy, r.UpdatedAt)
}

const rehearsalCols = `id, request_id, task_id, agent_id, tool, action, source, scope, would_decision, deny_reason, risk, reversibility, account,
	recipients, amount, flags, preview, args_hash, verdict, verdict_by, verdict_at, created_at`

func scanRehearsalAction(r scanner) (domain.RehearsalAction, error) {
	var a domain.RehearsalAction
	var rcpt, flags, preview []byte
	err := r.Scan(&a.ID, &a.RequestID, &a.TaskID, &a.AgentID, &a.Tool, &a.Action, &a.Source, &a.Scope, &a.WouldDecision, &a.DenyReason,
		&a.Risk, &a.Reversibility, &a.Account, &rcpt, &a.Amount, &flags, &preview, &a.ArgsHash, &a.Verdict, &a.VerdictBy, &a.VerdictAt, &a.CreatedAt)
	unmarshal(rcpt, &a.Recipients)
	unmarshal(flags, &a.Flags)
	unmarshal(preview, &a.Preview)
	a.Recipients, a.Flags = strs(a.Recipients), strs(a.Flags)
	if a.Preview == nil {
		a.Preview = map[string]string{}
	}
	return a, err
}

func (s *Store) AddRehearsalAction(ctx context.Context, orgID string, a domain.RehearsalAction) error {
	if a.Preview == nil {
		a.Preview = map[string]string{}
	}
	return s.exec(ctx, orgID, `INSERT INTO rehearsal_actions (org_id, `+rehearsalCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`,
		orgID, a.ID, a.RequestID, a.TaskID, a.AgentID, a.Tool, a.Action, a.Source, a.Scope, a.WouldDecision, a.DenyReason, a.Risk, a.Reversibility,
		a.Account, jb(strs(a.Recipients)), a.Amount, jb(strs(a.Flags)), jb(a.Preview), a.ArgsHash, a.Verdict, a.VerdictBy, a.VerdictAt, a.CreatedAt)
}

func (s *Store) GetRehearsalAction(ctx context.Context, orgID, id string) (domain.RehearsalAction, error) {
	a, err := one(ctx, s, orgID, scanRehearsalAction, `SELECT `+rehearsalCols+` FROM rehearsal_actions WHERE org_id=$1 AND id=$2`, orgID, id)
	return a, mapErr(err)
}

func (s *Store) ListRehearsalActions(ctx context.Context, orgID string, q domain.RehearsalQuery) ([]domain.RehearsalAction, error) {
	limit := q.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	return many(ctx, s, orgID, scanRehearsalAction, `SELECT `+rehearsalCols+` FROM rehearsal_actions
		WHERE org_id=$1 AND ($2='' OR request_id=$2) AND ($3='' OR agent_id=$3) ORDER BY created_at DESC, id DESC LIMIT $4`,
		orgID, q.RequestID, q.AgentID, limit)
}

func (s *Store) SetRehearsalVerdict(ctx context.Context, orgID, id, verdict, by string, at time.Time) (domain.RehearsalAction, error) {
	var when *time.Time
	if verdict != "" {
		when = &at
	} else {
		by = ""
	}
	tag, err := s.execTag(ctx, orgID, `UPDATE rehearsal_actions SET verdict=$3, verdict_by=$4, verdict_at=$5 WHERE org_id=$1 AND id=$2`, orgID, id, verdict, by, when)
	if err != nil {
		return domain.RehearsalAction{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.RehearsalAction{}, domain.ErrNotFound
	}
	return s.GetRehearsalAction(ctx, orgID, id)
}
