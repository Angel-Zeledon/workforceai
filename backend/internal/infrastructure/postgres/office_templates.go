package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/officetemplates"
)

// OfficeTemplateStore implements officetemplates.Store on Postgres (migration 826). Every call runs through
// WithOrgTx, so Row-Level Security isolates the organizations; the org_id filters are a second barrier.
type OfficeTemplateStore struct{ S *Store }

var _ officetemplates.Store = (*OfficeTemplateStore)(nil)

const officeTemplateCols = `id, name, kind, fingerprint, body, created_by, created_at`

func scanOfficeTemplate(r scanner) (officetemplates.Stored, error) {
	var t officetemplates.Stored
	var body []byte
	if err := r.Scan(&t.ID, &t.Name, &t.Kind, &t.Fingerprint, &body, &t.CreatedBy, &t.CreatedAt); err != nil {
		return t, err
	}
	if err := json.Unmarshal(body, &t.Template); err != nil {
		return t, fmt.Errorf("office template %s: %w", t.ID, err)
	}
	return t, nil
}

func (s *OfficeTemplateStore) PutTemplate(ctx context.Context, org string, t officetemplates.Stored) error {
	body, err := json.Marshal(t.Template)
	if err != nil {
		return err
	}
	err = s.S.exec(ctx, org, `INSERT INTO office_templates (org_id, id, name, kind, fingerprint, body, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, org, t.ID, t.Name, t.Kind, t.Fingerprint, body, t.CreatedBy, t.CreatedAt)
	if isUnique(err) {
		return fmt.Errorf("%w: this template is already imported", domain.ErrConflict)
	}
	return err
}

func (s *OfficeTemplateStore) ListTemplates(ctx context.Context, org string) ([]officetemplates.Stored, error) {
	out, err := many(ctx, s.S, org, scanOfficeTemplate,
		`SELECT `+officeTemplateCols+` FROM office_templates WHERE org_id=$1 ORDER BY created_at DESC, id`, org)
	if out == nil {
		out = []officetemplates.Stored{}
	}
	return out, err
}

func (s *OfficeTemplateStore) GetTemplate(ctx context.Context, org, id string) (officetemplates.Stored, error) {
	t, err := one(ctx, s.S, org, scanOfficeTemplate, `SELECT `+officeTemplateCols+` FROM office_templates WHERE org_id=$1 AND id=$2`, org, id)
	return t, mapErr(err)
}

func (s *OfficeTemplateStore) DeleteTemplate(ctx context.Context, org, id string) error {
	tag, err := s.S.execTag(ctx, org, `DELETE FROM office_templates WHERE org_id=$1 AND id=$2`, org, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: office template", domain.ErrNotFound)
	}
	return nil
}
