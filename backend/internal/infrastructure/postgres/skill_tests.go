package postgres

import (
	"context"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/skills"
)

// SkillTestStore implements skills.TestStore on Postgres (migration 829). Every call runs through WithOrgTx, so
// Row-Level Security applies; the runs are append-only.
type SkillTestStore struct{ S *Store }

var _ skills.TestStore = (*SkillTestStore)(nil)

const skillCaseCols = `id, skill_id, title, prompt, expect, created_by, created_at, updated_at`

func scanSkillCase(r scanner) (skills.TestCase, error) {
	var c skills.TestCase
	err := r.Scan(&c.ID, &c.SkillID, &c.Title, &c.Prompt, &c.Expect, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

const skillRunCols = `id, skill_id, case_id, case_title, pair_id, version, active, agent_id, status, fail_code, output, used_skill, blocked,
	estimate_usd, cost_usd, input_tokens, output_tokens, duration_ms, model, created_by, created_at`

func scanSkillRun(r scanner) (skills.TestRun, error) {
	var t skills.TestRun
	var blocked []byte
	if err := r.Scan(&t.ID, &t.SkillID, &t.CaseID, &t.CaseTitle, &t.PairID, &t.Version, &t.Active, &t.AgentID, &t.Status, &t.FailCode, &t.Output,
		&t.UsedSkill, &blocked, &t.EstimateUSD, &t.CostUSD, &t.InputTokens, &t.OutputTokens, &t.DurationMS, &t.Model, &t.CreatedBy, &t.CreatedAt); err != nil {
		return t, err
	}
	unmarshal(blocked, &t.Blocked)
	if t.Blocked == nil {
		t.Blocked = []string{}
	}
	return t, nil
}

func (d *SkillTestStore) ListCases(ctx context.Context, org, skillID string) ([]skills.TestCase, error) {
	return many(ctx, d.S, org, scanSkillCase, `SELECT `+skillCaseCols+` FROM skill_test_cases WHERE org_id=$1 AND skill_id=$2 ORDER BY created_at, id`, org, skillID)
}

func (d *SkillTestStore) GetCase(ctx context.Context, org, skillID, id string) (skills.TestCase, error) {
	c, err := one(ctx, d.S, org, scanSkillCase, `SELECT `+skillCaseCols+` FROM skill_test_cases WHERE org_id=$1 AND skill_id=$2 AND id=$3`, org, skillID, id)
	return c, mapErr(err)
}

func (d *SkillTestStore) PutCase(ctx context.Context, org string, c skills.TestCase) error {
	_, err := d.S.execTag(ctx, org, `INSERT INTO skill_test_cases (org_id, id, skill_id, title, prompt, expect, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (org_id, id) DO UPDATE SET title=EXCLUDED.title, prompt=EXCLUDED.prompt, expect=EXCLUDED.expect, updated_at=EXCLUDED.updated_at`,
		org, c.ID, c.SkillID, c.Title, c.Prompt, c.Expect, c.CreatedBy, c.CreatedAt, c.UpdatedAt)
	return err
}

func (d *SkillTestStore) DeleteCase(ctx context.Context, org, skillID, id string) error {
	tag, err := d.S.execTag(ctx, org, `DELETE FROM skill_test_cases WHERE org_id=$1 AND skill_id=$2 AND id=$3`, org, skillID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (d *SkillTestStore) AddRun(ctx context.Context, org string, r skills.TestRun) error {
	_, err := d.S.execTag(ctx, org, `INSERT INTO skill_test_runs (org_id, id, skill_id, case_id, case_title, pair_id, version, active, agent_id, status, fail_code,
			output, used_skill, blocked, estimate_usd, cost_usd, input_tokens, output_tokens, duration_ms, model, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`,
		org, r.ID, r.SkillID, r.CaseID, r.CaseTitle, r.PairID, r.Version, r.Active, r.AgentID, r.Status, r.FailCode, r.Output, r.UsedSkill,
		jb(strs(r.Blocked)), r.EstimateUSD, r.CostUSD, r.InputTokens, r.OutputTokens, r.DurationMS, r.Model, r.CreatedBy, r.CreatedAt)
	return err
}

func (d *SkillTestStore) ListRuns(ctx context.Context, org, skillID, caseID string, limit int) ([]skills.TestRun, error) {
	if limit <= 0 {
		limit = 100
	}
	if caseID == "" {
		return many(ctx, d.S, org, scanSkillRun, `SELECT `+skillRunCols+` FROM skill_test_runs WHERE org_id=$1 AND skill_id=$2
			ORDER BY created_at DESC, id DESC LIMIT $3`, org, skillID, limit)
	}
	return many(ctx, d.S, org, scanSkillRun, `SELECT `+skillRunCols+` FROM skill_test_runs WHERE org_id=$1 AND skill_id=$2 AND case_id=$3
		ORDER BY created_at DESC, id DESC LIMIT $4`, org, skillID, caseID, limit)
}
