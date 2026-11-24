package postgres

import (
	"context"

	"aiworkforce/backend/internal/offices"
)

// Extra offices of the agents (migration 832).

func (o *OfficesStore) ListAgentOffices(ctx context.Context, org string) ([]offices.AgentOffice, error) {
	return many(ctx, o.S, org, func(r scanner) (offices.AgentOffice, error) {
		var x offices.AgentOffice
		err := r.Scan(&x.AgentID, &x.OfficeID, &x.GrantedBy, &x.AcceptedBy, &x.CreatedAt, &x.AcceptedAt)
		return x, err
	}, `SELECT agent_id, office_id, granted_by, accepted_by, created_at, accepted_at FROM agent_offices
		WHERE org_id=$1 ORDER BY agent_id, office_id`, org)
}

func (o *OfficesStore) PutAgentOffice(ctx context.Context, org string, x offices.AgentOffice) error {
	return o.S.exec(ctx, org, `INSERT INTO agent_offices (org_id, agent_id, office_id, granted_by, accepted_by, created_at, accepted_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (org_id, agent_id, office_id) DO UPDATE SET granted_by=EXCLUDED.granted_by,
			accepted_by=EXCLUDED.accepted_by, accepted_at=EXCLUDED.accepted_at`,
		org, x.AgentID, x.OfficeID, x.GrantedBy, x.AcceptedBy, x.CreatedAt, x.AcceptedAt)
}

func (o *OfficesStore) DeleteAgentOffice(ctx context.Context, org, agentID, officeID string) error {
	return o.S.exec(ctx, org, `DELETE FROM agent_offices WHERE org_id=$1 AND agent_id=$2 AND office_id=$3`, org, agentID, officeID)
}
