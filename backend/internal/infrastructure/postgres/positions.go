package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/positions"
)

// PositionsStore implements positions.Store on Postgres (migration 830). Every call runs through WithOrgTx, so
// Row-Level Security isolates the organizations; the org_id filters are a second barrier.
type PositionsStore struct{ S *Store }

var _ positions.Store = (*PositionsStore)(nil)

const positionCols = `id, office_id, name, reports_to, skills, library, template, created_by, created_at, updated_by, updated_at`

func scanPosition(r scanner) (positions.Position, error) {
	var p positions.Position
	var skills, library []byte
	if err := r.Scan(&p.ID, &p.OfficeID, &p.Name, &p.ReportsTo, &skills, &library, &p.Template, &p.CreatedBy, &p.CreatedAt, &p.UpdatedBy, &p.UpdatedAt); err != nil {
		return p, err
	}
	if err := json.Unmarshal(skills, &p.Skills); err != nil {
		return p, fmt.Errorf("position %s skills: %w", p.ID, err)
	}
	if err := json.Unmarshal(library, &p.Library); err != nil {
		return p, fmt.Errorf("position %s library: %w", p.ID, err)
	}
	if p.Skills == nil {
		p.Skills = []string{}
	}
	if p.Library == nil {
		p.Library = []positions.Folder{}
	}
	return p, nil
}

func (s *PositionsStore) ListPositions(ctx context.Context, org, officeID string) ([]positions.Position, error) {
	out, err := many(ctx, s.S, org, scanPosition, `SELECT `+positionCols+` FROM office_positions
		WHERE org_id=$1 AND ($2 = '' OR office_id=$2) ORDER BY created_at, id`, org, officeID)
	if out == nil {
		out = []positions.Position{}
	}
	return out, err
}

func (s *PositionsStore) GetPosition(ctx context.Context, org, id string) (positions.Position, error) {
	p, err := one(ctx, s.S, org, scanPosition, `SELECT `+positionCols+` FROM office_positions WHERE org_id=$1 AND id=$2`, org, id)
	return p, mapErr(err)
}

func (s *PositionsStore) SavePositions(ctx context.Context, org string, ps []positions.Position) error {
	return s.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		for _, p := range ps {
			skills, err := json.Marshal(nonNilStrings(p.Skills))
			if err != nil {
				return err
			}
			lib := p.Library
			if lib == nil {
				lib = []positions.Folder{}
			}
			library, err := json.Marshal(lib)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `INSERT INTO office_positions (org_id, id, office_id, name, reports_to, skills, library, template, created_by, created_at, updated_by, updated_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
				ON CONFLICT (org_id, id) DO UPDATE SET name=EXCLUDED.name, reports_to=EXCLUDED.reports_to, skills=EXCLUDED.skills,
					library=EXCLUDED.library, template=EXCLUDED.template, updated_by=EXCLUDED.updated_by, updated_at=EXCLUDED.updated_at`,
				org, p.ID, p.OfficeID, p.Name, p.ReportsTo, skills, library, p.Template, p.CreatedBy, p.CreatedAt, p.UpdatedBy, p.UpdatedAt)
			if isUnique(err) {
				return fmt.Errorf("%w: the department already has a position with that name", domain.ErrConflict)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *PositionsStore) DeletePosition(ctx context.Context, org, id string) error {
	return s.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var up string
		if err := tx.QueryRow(ctx, `SELECT reports_to FROM office_positions WHERE org_id=$1 AND id=$2 FOR UPDATE`, org, id).Scan(&up); err != nil {
			return mapErr(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE office_positions SET reports_to=$3 WHERE org_id=$1 AND reports_to=$2`, org, id, up); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM agent_positions WHERE org_id=$1 AND position_id=$2`, org, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM office_positions WHERE org_id=$1 AND id=$2`, org, id)
		return err
	})
}

const positionAssignmentCols = `agent_id, position_id, office_id, assigned_by, assigned_at`

func scanPositionAssignment(r scanner) (positions.Assignment, error) {
	var a positions.Assignment
	err := r.Scan(&a.AgentID, &a.PositionID, &a.OfficeID, &a.AssignedBy, &a.AssignedAt)
	return a, err
}

func (s *PositionsStore) ListAssignments(ctx context.Context, org string) ([]positions.Assignment, error) {
	out, err := many(ctx, s.S, org, scanPositionAssignment, `SELECT `+positionAssignmentCols+` FROM agent_positions WHERE org_id=$1 ORDER BY agent_id`, org)
	if out == nil {
		out = []positions.Assignment{}
	}
	return out, err
}

func (s *PositionsStore) AssignmentOf(ctx context.Context, org, agentID string) (positions.Assignment, error) {
	a, err := one(ctx, s.S, org, scanPositionAssignment, `SELECT `+positionAssignmentCols+` FROM agent_positions WHERE org_id=$1 AND agent_id=$2`, org, agentID)
	return a, mapErr(err)
}

func (s *PositionsStore) SetAssignment(ctx context.Context, org string, a positions.Assignment) error {
	err := s.S.exec(ctx, org, `INSERT INTO agent_positions (org_id, agent_id, position_id, office_id, assigned_by, assigned_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (org_id, agent_id) DO UPDATE SET position_id=EXCLUDED.position_id, office_id=EXCLUDED.office_id,
			assigned_by=EXCLUDED.assigned_by, assigned_at=EXCLUDED.assigned_at`,
		org, a.AgentID, a.PositionID, a.OfficeID, a.AssignedBy, a.AssignedAt)
	if isForeignKey(err) {
		return fmt.Errorf("%w: position", domain.ErrNotFound)
	}
	return err
}

func (s *PositionsStore) DeleteAssignment(ctx context.Context, org, agentID string) error {
	return s.S.exec(ctx, org, `DELETE FROM agent_positions WHERE org_id=$1 AND agent_id=$2`, org, agentID)
}

func isForeignKey(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23503"
}
