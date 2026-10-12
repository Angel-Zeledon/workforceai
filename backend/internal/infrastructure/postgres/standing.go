package postgres

import (
	"context"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.StandingStore = (*Store)(nil)

const standingCols = `id, project_id, agent_id, tool, action, approval_id, created_by, created_at, expires_at, revoked_at, revoked_by, use_count, last_used_at`

func scanStanding(r scanner) (application.StandingApproval, error) {
	var s application.StandingApproval
	err := r.Scan(&s.ID, &s.ProjectID, &s.AgentID, &s.Tool, &s.Action, &s.ApprovalID, &s.CreatedBy, &s.CreatedAt, &s.ExpiresAt,
		&s.RevokedAt, &s.RevokedBy, &s.UseCount, &s.LastUsedAt)
	return s, err
}

func (s *Store) CreateStanding(ctx context.Context, orgID string, st application.StandingApproval) error {
	return s.exec(ctx, orgID, `INSERT INTO standing_approvals (id, org_id, project_id, agent_id, tool, action, approval_id, created_by, created_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		st.ID, orgID, st.ProjectID, st.AgentID, st.Tool, st.Action, st.ApprovalID, st.CreatedBy, st.CreatedAt, st.ExpiresAt)
}

func (s *Store) ListStanding(ctx context.Context, orgID, projectID string) ([]application.StandingApproval, error) {
	return many(ctx, s, orgID, scanStanding, `SELECT `+standingCols+` FROM standing_approvals
		WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC, id DESC`, orgID, projectID)
}

func (s *Store) GetStanding(ctx context.Context, orgID, id string) (application.StandingApproval, error) {
	st, err := one(ctx, s, orgID, scanStanding, `SELECT `+standingCols+` FROM standing_approvals WHERE org_id=$1 AND id=$2`, orgID, id)
	return st, mapErr(err)
}

// RevokeStanding keeps the first revocation (the WHERE clause only matches live rows).
func (s *Store) RevokeStanding(ctx context.Context, orgID, id, by string, at time.Time) error {
	if _, err := s.GetStanding(ctx, orgID, id); err != nil {
		return domain.ErrNotFound
	}
	return s.exec(ctx, orgID, `UPDATE standing_approvals SET revoked_at=$3, revoked_by=$4 WHERE org_id=$1 AND id=$2 AND revoked_at IS NULL`,
		orgID, id, at, by)
}

func (s *Store) TouchStanding(ctx context.Context, orgID, id string, at time.Time) error {
	return s.exec(ctx, orgID, `UPDATE standing_approvals SET use_count=use_count+1, last_used_at=$3 WHERE org_id=$1 AND id=$2`, orgID, id, at)
}
