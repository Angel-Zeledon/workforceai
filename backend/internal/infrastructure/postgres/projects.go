package postgres

import (
	"context"
	"encoding/json"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
)

// ProjectStore implements projects.Store on Postgres (migration 270). Every
// call runs through WithOrgTx, so Row-Level Security applies; the project
// itself is a JSONB document (the live state is derived from the orchestrator).
type ProjectStore struct{ S *Store }

var _ projects.Store = (*ProjectStore)(nil)

func scanProject(r scanner) (projects.Record, error) {
	var raw []byte
	err := r.Scan(&raw)
	var rec projects.Record
	if err == nil {
		err = json.Unmarshal(raw, &rec)
	}
	return rec, err
}

func (p *ProjectStore) Put(ctx context.Context, r projects.Record) error {
	return p.S.exec(ctx, r.OrgID, `INSERT INTO projects (id, org_id, status, request_id, data, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (org_id, id) DO UPDATE SET status=EXCLUDED.status, request_id=EXCLUDED.request_id, data=EXCLUDED.data, updated_at=now()`,
		r.ID, r.OrgID, r.Status, r.RequestID, jb(r), r.CreatedAt)
}

func (p *ProjectStore) Get(ctx context.Context, org, id string) (projects.Record, error) {
	r, err := one(ctx, p.S, org, scanProject, `SELECT data FROM projects WHERE org_id=$1 AND id=$2`, org, id)
	return r, mapErr(err)
}

func (p *ProjectStore) List(ctx context.Context, org string) ([]projects.Record, error) {
	return many(ctx, p.S, org, scanProject, `SELECT data FROM projects WHERE org_id=$1 ORDER BY created_at DESC, id`, org)
}

func (p *ProjectStore) ByRequest(ctx context.Context, org, requestID string) (projects.Record, error) {
	if requestID == "" {
		return projects.Record{}, domain.ErrNotFound
	}
	r, err := one(ctx, p.S, org, scanProject, `SELECT data FROM projects WHERE org_id=$1 AND request_id=$2`, org, requestID)
	return r, mapErr(err)
}

func (p *ProjectStore) PutTemplate(ctx context.Context, org string, t projects.Template) error {
	return p.S.exec(ctx, org, `INSERT INTO project_templates (id, org_id, key, version, data)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (org_id, id) DO UPDATE SET key=EXCLUDED.key, version=EXCLUDED.version, data=EXCLUDED.data`,
		t.ID, org, t.Key, t.Version, jb(t))
}

func (p *ProjectStore) ListTemplates(ctx context.Context, org string) ([]projects.Template, error) {
	return many(ctx, p.S, org, func(r scanner) (projects.Template, error) {
		var raw []byte
		var t projects.Template
		if err := r.Scan(&raw); err != nil {
			return t, err
		}
		return t, json.Unmarshal(raw, &t)
	}, `SELECT data FROM project_templates WHERE org_id=$1 ORDER BY created_at, id`, org)
}

var _ projects.ChangeStore = (*ProjectStore)(nil)

func scanChange(r scanner) (projects.PlanChange, error) {
	var raw []byte
	var c projects.PlanChange
	if err := r.Scan(&raw); err != nil {
		return c, err
	}
	return c, json.Unmarshal(raw, &c)
}

// PutChange upserts a plan change (migration 440).
func (p *ProjectStore) PutChange(ctx context.Context, c projects.PlanChange) error {
	return p.S.exec(ctx, c.OrgID, `INSERT INTO plan_changes (id, org_id, project_id, status, data, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (org_id, id) DO UPDATE SET status=EXCLUDED.status, data=EXCLUDED.data, updated_at=now()`,
		c.ID, c.OrgID, c.ProjectID, c.Status, jb(c), c.CreatedAt)
}

func (p *ProjectStore) GetChange(ctx context.Context, org, project, id string) (projects.PlanChange, error) {
	c, err := one(ctx, p.S, org, scanChange, `SELECT data FROM plan_changes WHERE org_id=$1 AND project_id=$2 AND id=$3`, org, project, id)
	return c, mapErr(err)
}

func (p *ProjectStore) ListChanges(ctx context.Context, org, project string) ([]projects.PlanChange, error) {
	return many(ctx, p.S, org, scanChange, `SELECT data FROM plan_changes WHERE org_id=$1 AND project_id=$2 ORDER BY created_at, id`, org, project)
}
