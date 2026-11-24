package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.HelperStore = (*Store)(nil)

// storedPart is a part as kept in helper_groups.parts: the API shape plus the material of the part.
type storedPart struct {
	Index  int              `json:"index"`
	Label  string           `json:"label"`
	TaskID string           `json:"task_id,omitempty"`
	Status string           `json:"status,omitempty"`
	Text   string           `json:"text,omitempty"`
	Files  []domain.FileRef `json:"files,omitempty"`
}

const helperGroupCols = `id, request_id, owner_agent_id, mode, count, max_count, parallel, parts, estimate_min, estimate_max, cap_usd, spent_usd, status, accepted_by, union_task_id, stop_reason, created_at, finished_at`

func scanHelperGroup(r scanner) (domain.HelperGroup, error) {
	var g domain.HelperGroup
	var parts []byte
	if err := r.Scan(&g.ID, &g.RequestID, &g.OwnerAgentID, &g.Mode, &g.Count, &g.MaxCount, &g.Parallel, &parts, &g.Estimate.MinUSD, &g.Estimate.MaxUSD,
		&g.CapUSD, &g.SpentUSD, &g.Status, &g.AcceptedBy, &g.UnionTaskID, &g.StopReason, &g.CreatedAt, &g.FinishedAt); err != nil {
		return g, err
	}
	var sp []storedPart
	if err := json.Unmarshal(parts, &sp); err != nil {
		return g, fmt.Errorf("helper group %s parts: %w", g.ID, err)
	}
	g.Parts = make([]domain.HelperPart, len(sp))
	for i, p := range sp {
		g.Parts[i] = domain.HelperPart{Index: p.Index, Label: p.Label, TaskID: p.TaskID, Status: p.Status, Text: p.Text, Files: p.Files}
	}
	return g, nil
}

func partsJSON(parts []domain.HelperPart) []byte {
	sp := make([]storedPart, len(parts))
	for i, p := range parts {
		sp[i] = storedPart{Index: p.Index, Label: p.Label, TaskID: p.TaskID, Status: p.Status, Text: p.Text, Files: p.Files}
	}
	return jb(sp)
}

func (s *Store) CreateHelperGroup(ctx context.Context, orgID string, g domain.HelperGroup) error {
	err := s.exec(ctx, orgID, `INSERT INTO helper_groups (org_id, id, request_id, owner_agent_id, mode, count, max_count, parallel, parts, estimate_min, estimate_max,
		cap_usd, spent_usd, status, accepted_by, union_task_id, stop_reason, created_at, finished_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		orgID, g.ID, g.RequestID, g.OwnerAgentID, g.Mode, g.Count, g.MaxCount, max(g.Parallel, 1), partsJSON(g.Parts), g.Estimate.MinUSD, g.Estimate.MaxUSD,
		g.CapUSD, g.SpentUSD, g.Status, g.AcceptedBy, g.UnionTaskID, g.StopReason, g.CreatedAt, g.FinishedAt)
	if isUnique(err) {
		return fmt.Errorf("%w: the request already has helpers", domain.ErrConflict)
	}
	return err
}

func (s *Store) GetHelperGroup(ctx context.Context, orgID, id string) (domain.HelperGroup, error) {
	g, err := one(ctx, s, orgID, scanHelperGroup, `SELECT `+helperGroupCols+` FROM helper_groups WHERE org_id=$1 AND id=$2`, orgID, id)
	return g, mapErr(err)
}

func (s *Store) GetHelperGroupByRequest(ctx context.Context, orgID, requestID string) (domain.HelperGroup, error) {
	g, err := one(ctx, s, orgID, scanHelperGroup, `SELECT `+helperGroupCols+` FROM helper_groups WHERE org_id=$1 AND request_id=$2`, orgID, requestID)
	return g, mapErr(err)
}

func (s *Store) UpdateHelperGroup(ctx context.Context, orgID string, g domain.HelperGroup) error {
	tag, err := s.execTag(ctx, orgID, `UPDATE helper_groups SET owner_agent_id=$3, mode=$4, count=$5, max_count=$6, parallel=$7, parts=$8, estimate_min=$9,
		estimate_max=$10, cap_usd=$11, status=$12, accepted_by=$13, union_task_id=$14, stop_reason=$15, finished_at=$16 WHERE org_id=$1 AND id=$2`,
		orgID, g.ID, g.OwnerAgentID, g.Mode, g.Count, g.MaxCount, max(g.Parallel, 1), partsJSON(g.Parts), g.Estimate.MinUSD, g.Estimate.MaxUSD,
		g.CapUSD, g.Status, g.AcceptedBy, g.UnionTaskID, g.StopReason, g.FinishedAt)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (s *Store) AddHelperSpend(ctx context.Context, orgID, id string, usd float64) error {
	tag, err := s.execTag(ctx, orgID, `UPDATE helper_groups SET spent_usd = spent_usd + $3 WHERE org_id=$1 AND id=$2`, orgID, id, usd)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (s *Store) ListHelperGroups(ctx context.Context, orgID string, statuses ...string) ([]domain.HelperGroup, error) {
	return many(ctx, s, orgID, scanHelperGroup, `SELECT `+helperGroupCols+` FROM helper_groups
		WHERE org_id=$1 AND (cardinality($2::text[]) = 0 OR status = ANY($2::text[])) ORDER BY created_at, id`, orgID, statuses)
}
