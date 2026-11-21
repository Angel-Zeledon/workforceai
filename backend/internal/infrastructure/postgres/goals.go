package postgres

import (
	"context"
	"fmt"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/goals"
)

// GoalStore implements goals.Store on Postgres (migration 822). Every call runs through WithOrgTx, so Row-Level
// Security isolates the organizations; the org_id filters are a second barrier.
type GoalStore struct{ S *Store }

var _ goals.Store = (*GoalStore)(nil)

const goalCols = `id, office_id, title, description, status, period, due_date, created_by, created_at, updated_at`

func scanGoal(r scanner) (goals.Goal, error) {
	var g goals.Goal
	err := r.Scan(&g.ID, &g.OfficeID, &g.Title, &g.Description, &g.Status, &g.Period, &g.DueDate, &g.CreatedBy, &g.CreatedAt, &g.UpdatedAt)
	if g.DueDate != nil {
		d := g.DueDate.UTC()
		g.DueDate = &d
	}
	return g, err
}

func (s *GoalStore) ListGoals(ctx context.Context, org string) ([]goals.Goal, error) {
	out, err := many(ctx, s.S, org, scanGoal, `SELECT `+goalCols+` FROM goals WHERE org_id=$1 ORDER BY created_at DESC, id DESC`, org)
	if out == nil {
		out = []goals.Goal{}
	}
	return out, err
}

func (s *GoalStore) GetGoal(ctx context.Context, org, id string) (goals.Goal, error) {
	g, err := one(ctx, s.S, org, scanGoal, `SELECT `+goalCols+` FROM goals WHERE org_id=$1 AND id=$2`, org, id)
	return g, mapErr(err)
}

func (s *GoalStore) CreateGoal(ctx context.Context, org string, g goals.Goal) error {
	err := s.S.exec(ctx, org, `INSERT INTO goals (org_id, id, office_id, title, description, status, period, due_date, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, org, g.ID, g.OfficeID, g.Title, g.Description, g.Status, g.Period, g.DueDate, g.CreatedBy, g.CreatedAt, g.UpdatedAt)
	if isUnique(err) {
		return fmt.Errorf("%w: goal exists", domain.ErrConflict)
	}
	return err
}

func (s *GoalStore) UpdateGoal(ctx context.Context, org string, g goals.Goal) error {
	tag, err := s.S.execTag(ctx, org, `UPDATE goals SET title=$3, description=$4, status=$5, period=$6, due_date=$7, updated_at=$8
		WHERE org_id=$1 AND id=$2`, org, g.ID, g.Title, g.Description, g.Status, g.Period, g.DueDate, g.UpdatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: goal", domain.ErrNotFound)
	}
	return nil
}
