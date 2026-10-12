package postgres

import (
	"context"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.LLMStore = (*Store)(nil)

const llmCols = `provider, model, base_url, org_ref, project_ref, sealed_key, key_last4, updated_by, created_at, updated_at, last_test_code, last_test_at`

func scanLLM(r scanner) (application.LLMCredentialRecord, error) {
	var x application.LLMCredentialRecord
	err := r.Scan(&x.Provider, &x.Model, &x.BaseURL, &x.OrgRef, &x.ProjectRef, &x.Sealed, &x.Last4, &x.UpdatedBy,
		&x.CreatedAt, &x.UpdatedAt, &x.LastTestCode, &x.LastTestAt)
	return x, err
}

func (s *Store) ListLLMCredentials(ctx context.Context, orgID string) ([]application.LLMCredentialRecord, error) {
	return many(ctx, s, orgID, scanLLM, `SELECT `+llmCols+` FROM org_llm_credentials WHERE org_id=$1 ORDER BY provider`, orgID)
}

func (s *Store) PutLLMCredential(ctx context.Context, orgID string, r application.LLMCredentialRecord) error {
	return s.exec(ctx, orgID, `INSERT INTO org_llm_credentials (org_id, provider, model, base_url, org_ref, project_ref, sealed_key, key_last4, updated_by, created_at, updated_at, last_test_code, last_test_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (org_id, provider) DO UPDATE SET model=EXCLUDED.model, base_url=EXCLUDED.base_url, org_ref=EXCLUDED.org_ref,
		  project_ref=EXCLUDED.project_ref, sealed_key=EXCLUDED.sealed_key, key_last4=EXCLUDED.key_last4, updated_by=EXCLUDED.updated_by,
		  updated_at=EXCLUDED.updated_at, last_test_code=EXCLUDED.last_test_code, last_test_at=EXCLUDED.last_test_at`,
		orgID, r.Provider, r.Model, r.BaseURL, r.OrgRef, r.ProjectRef, r.Sealed, r.Last4, r.UpdatedBy, r.CreatedAt, r.UpdatedAt, r.LastTestCode, r.LastTestAt)
}

func (s *Store) DeleteLLMCredential(ctx context.Context, orgID, provider string) error {
	tag, err := s.execTag(ctx, orgID, `DELETE FROM org_llm_credentials WHERE org_id=$1 AND provider=$2`, orgID, provider)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) RecordLLMTest(ctx context.Context, orgID, provider, code string, at time.Time) error {
	return s.exec(ctx, orgID, `UPDATE org_llm_credentials SET last_test_code=$3, last_test_at=$4 WHERE org_id=$1 AND provider=$2`, orgID, provider, code, at)
}

func (s *Store) GetLLMActive(ctx context.Context, orgID string) (string, error) {
	v, err := one(ctx, s, orgID, func(r scanner) (string, error) {
		var a string
		err := r.Scan(&a)
		return a, err
	}, `SELECT active_provider FROM org_llm_settings WHERE org_id=$1`, orgID)
	if err != nil && mapErr(err) == domain.ErrNotFound {
		return "", nil
	}
	return v, err
}

func (s *Store) PutLLMActive(ctx context.Context, orgID, provider, by string) error {
	return s.exec(ctx, orgID, `INSERT INTO org_llm_settings (org_id, active_provider, updated_by) VALUES ($1,$2,$3)
		ON CONFLICT (org_id) DO UPDATE SET active_provider=EXCLUDED.active_provider, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		orgID, provider, by)
}
