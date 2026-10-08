package postgres

import (
	"context"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.ModelPolicyStore = (*Store)(nil)

// GetModelPolicy returns the organization's model policy (zero value when none).
func (s *Store) GetModelPolicy(ctx context.Context, orgID string) (application.ModelPolicy, error) {
	p, err := one(ctx, s, orgID, func(r scanner) (application.ModelPolicy, error) {
		var p application.ModelPolicy
		var raw []byte
		err := r.Scan(&raw)
		if err == nil {
			unmarshal(raw, &p)
		}
		return p, err
	}, `SELECT policy FROM org_model_policy WHERE org_id=$1`, orgID)
	if err != nil && mapErr(err) == domain.ErrNotFound {
		return application.ModelPolicy{}, nil
	}
	return p, err
}

// PutModelPolicy upserts the organization's model policy.
func (s *Store) PutModelPolicy(ctx context.Context, orgID string, p application.ModelPolicy) error {
	return s.exec(ctx, orgID, `INSERT INTO org_model_policy (org_id, policy, updated_by) VALUES ($1,$2,$3)
		ON CONFLICT (org_id) DO UPDATE SET policy=EXCLUDED.policy, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		orgID, jb(p), p.UpdatedBy)
}
