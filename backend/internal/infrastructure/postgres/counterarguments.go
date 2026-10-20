package postgres

import (
	"context"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.CounterargumentStore = (*Store)(nil)

const counterargumentCols = `approval_id, counterargument, risks, checks, locale, model, provider, cost_usd, generated_by, created_at`

func scanCounterargument(r scanner) (domain.Counterargument, error) {
	var c domain.Counterargument
	var risks, checks []byte
	err := r.Scan(&c.ApprovalID, &c.Counterargument, &risks, &checks, &c.Locale, &c.Model, &c.Provider, &c.CostUSD, &c.GeneratedBy, &c.CreatedAt)
	unmarshal(risks, &c.Risks)
	unmarshal(checks, &c.Checks)
	if c.Risks == nil {
		c.Risks = []string{}
	}
	if c.Checks == nil {
		c.Checks = []string{}
	}
	c.Advisory = true
	return c, err
}

func (s *Store) GetCounterargument(ctx context.Context, orgID, approvalID string) (domain.Counterargument, error) {
	c, err := one(ctx, s, orgID, scanCounterargument, `SELECT `+counterargumentCols+` FROM approval_counterarguments WHERE org_id=$1 AND approval_id=$2`, orgID, approvalID)
	return c, mapErr(err)
}

// PutCounterargument inserts the review unless the approval already has one (the first generation wins, so two
// replicas never keep two different answers); it returns the stored row and whether this call inserted it.
func (s *Store) PutCounterargument(ctx context.Context, orgID string, c domain.Counterargument) (domain.Counterargument, bool, error) {
	tag, err := s.execTag(ctx, orgID, `INSERT INTO approval_counterarguments (org_id, approval_id, counterargument, risks, checks, locale, model, provider, cost_usd, generated_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (org_id, approval_id) DO NOTHING`,
		orgID, c.ApprovalID, c.Counterargument, jb(strs(c.Risks)), jb(strs(c.Checks)), c.Locale, c.Model, c.Provider, c.CostUSD, c.GeneratedBy, c.CreatedAt)
	if err != nil {
		return domain.Counterargument{}, false, err
	}
	if tag.RowsAffected() == 1 {
		c.Advisory = true
		return c, true, nil
	}
	cur, err := s.GetCounterargument(ctx, orgID, c.ApprovalID)
	return cur, false, err
}
