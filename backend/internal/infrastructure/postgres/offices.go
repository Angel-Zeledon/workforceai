package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
	"aiworkforce/backend/internal/rewards"
)

// OfficesStore implements offices.Store on Postgres (migration 815). Row-Level Security applies.
type OfficesStore struct{ S *Store }

var _ offices.Store = (*OfficesStore)(nil)

func (o *OfficesStore) ListOffices(ctx context.Context, org string) ([]offices.OfficeRow, error) {
	return many(ctx, o.S, org, func(r scanner) (offices.OfficeRow, error) {
		var x offices.OfficeRow
		err := r.Scan(&x.DepartmentID, &x.Visibility, &x.CreatedBy, &x.CreatedAt, &x.UpdatedAt)
		return x, err
	}, `SELECT department_id, visibility, created_by, created_at, updated_at FROM offices WHERE org_id=$1 ORDER BY created_at, department_id`, org)
}

func (o *OfficesStore) PutOffice(ctx context.Context, org string, x offices.OfficeRow) error {
	return o.S.exec(ctx, org, `INSERT INTO offices (org_id, department_id, visibility, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (org_id, department_id) DO UPDATE SET visibility=EXCLUDED.visibility, updated_at=EXCLUDED.updated_at`,
		org, x.DepartmentID, x.Visibility, x.CreatedBy, x.CreatedAt, x.UpdatedAt)
}

func (o *OfficesStore) DeleteOffice(ctx context.Context, org, id string) error {
	tag, err := o.S.execTag(ctx, org, `DELETE FROM offices WHERE org_id=$1 AND department_id=$2`, org, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: office %s", domain.ErrNotFound, id)
	}
	return o.S.exec(ctx, org, `DELETE FROM office_members WHERE org_id=$1 AND office_id=$2`, org, id)
}

func (o *OfficesStore) ListMembers(ctx context.Context, org string) ([]offices.MemberRow, error) {
	return many(ctx, o.S, org, func(r scanner) (offices.MemberRow, error) {
		var x offices.MemberRow
		err := r.Scan(&x.OfficeID, &x.UserID, &x.Role, &x.AddedBy, &x.AddedAt)
		return x, err
	}, `SELECT office_id, user_id, role, added_by, added_at FROM office_members WHERE org_id=$1 ORDER BY office_id, added_at, user_id`, org)
}

func (o *OfficesStore) PutMember(ctx context.Context, org string, x offices.MemberRow) error {
	return o.S.exec(ctx, org, `INSERT INTO office_members (org_id, office_id, user_id, role, added_by, added_at) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (org_id, office_id, user_id) DO UPDATE SET role=EXCLUDED.role`,
		org, x.OfficeID, x.UserID, x.Role, x.AddedBy, x.AddedAt)
}

func (o *OfficesStore) DeleteMember(ctx context.Context, org, office, user string) error {
	tag, err := o.S.execTag(ctx, org, `DELETE FROM office_members WHERE org_id=$1 AND office_id=$2 AND user_id=$3`, org, office, user)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: member %s", domain.ErrNotFound, user)
	}
	return nil
}

func (o *OfficesStore) ListAgentAccess(ctx context.Context, org string) ([]offices.AgentAccess, error) {
	return many(ctx, o.S, org, func(r scanner) (offices.AgentAccess, error) {
		var x offices.AgentAccess
		var raw []byte
		if err := r.Scan(&x.AgentID, &x.OwnerKind, &x.OwnerID, &x.HomeOffice, &x.Visibility, &raw, &x.UpdatedBy, &x.UpdatedAt); err != nil {
			return x, err
		}
		if err := json.Unmarshal(raw, &x.Shares); err != nil {
			return x, err
		}
		return x, nil
	}, `SELECT agent_id, owner_kind, owner_id, home_office, visibility, shares, updated_by, updated_at FROM agent_access
		WHERE org_id=$1 ORDER BY agent_id`, org)
}

func (o *OfficesStore) PutAgentAccess(ctx context.Context, org string, x offices.AgentAccess) error {
	shares := x.Shares
	if shares == nil {
		shares = []offices.Share{}
	}
	raw, err := json.Marshal(shares)
	if err != nil {
		return err
	}
	return o.S.exec(ctx, org, `INSERT INTO agent_access (org_id, agent_id, owner_kind, owner_id, home_office, visibility, shares, updated_by, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (org_id, agent_id) DO UPDATE SET owner_kind=EXCLUDED.owner_kind, owner_id=EXCLUDED.owner_id,
			home_office=EXCLUDED.home_office, visibility=EXCLUDED.visibility, shares=EXCLUDED.shares,
			updated_by=EXCLUDED.updated_by, updated_at=EXCLUDED.updated_at`,
		org, x.AgentID, x.OwnerKind, x.OwnerID, x.HomeOffice, x.Visibility, raw, x.UpdatedBy, x.UpdatedAt)
}

func (o *OfficesStore) DeleteAgentAccess(ctx context.Context, org, agentID string) error {
	return o.S.exec(ctx, org, `DELETE FROM agent_access WHERE org_id=$1 AND agent_id=$2`, org, agentID)
}

func (o *OfficesStore) ListNotes(ctx context.Context, org, scope, ref string) ([]offices.Note, error) {
	return many(ctx, o.S, org, func(r scanner) (offices.Note, error) {
		var x offices.Note
		err := r.Scan(&x.ID, &x.Scope, &x.OwnerRef, &x.Text, &x.PromotedFrom, &x.CreatedBy, &x.CreatedAt)
		return x, err
	}, `SELECT id, scope, owner_ref, text, promoted_from, created_by, created_at FROM team_notes
		WHERE org_id=$1 AND deleted_at IS NULL AND ($2 = '' OR scope=$2) AND ($3 = '' OR owner_ref=$3)
		ORDER BY created_at DESC, id DESC LIMIT 2000`, org, scope, ref)
}

func (o *OfficesStore) PutNote(ctx context.Context, org string, x offices.Note) error {
	return o.S.exec(ctx, org, `INSERT INTO team_notes (id, org_id, scope, owner_ref, text, promoted_from, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, x.ID, org, x.Scope, x.OwnerRef, x.Text, x.PromotedFrom, x.CreatedBy, x.CreatedAt)
}

func (o *OfficesStore) DeleteNote(ctx context.Context, org, id, by string) error {
	tag, err := o.S.execTag(ctx, org, `UPDATE team_notes SET deleted_at=now(), deleted_by=$3 WHERE org_id=$1 AND id=$2 AND deleted_at IS NULL`, org, id, by)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: note %s", domain.ErrNotFound, id)
	}
	return nil
}

var _ rewards.OfficeLayoutStore = (*Store)(nil)

// GetOfficeLayout implements rewards.OfficeLayoutStore (migration 815): the layout of one office.
func (s *Store) GetOfficeLayout(ctx context.Context, org, office string) (json.RawMessage, int, error) {
	var doc []byte
	var v int
	err := s.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT doc, version FROM office_layouts WHERE org_id=$1 AND office_id=$2`, org, office).Scan(&doc, &v)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	return doc, v, err
}

// SaveOfficeLayout implements rewards.OfficeLayoutStore: the same compare-and-set on the version as SaveLayout.
func (s *Store) SaveOfficeLayout(ctx context.Context, org, office string, doc json.RawMessage, base int, actor string) (int, error) {
	var out int
	err := s.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		var err error
		if base == 0 {
			err = tx.QueryRow(ctx, `INSERT INTO office_layouts (org_id, office_id, doc, version, updated_by) VALUES ($1,$2,$3,1,$4)
				ON CONFLICT (org_id, office_id) DO NOTHING RETURNING version`, org, office, []byte(doc), actor).Scan(&out)
		} else {
			err = tx.QueryRow(ctx, `UPDATE office_layouts SET doc=$3, version=version+1, updated_at=now(), updated_by=$5
				WHERE org_id=$1 AND office_id=$2 AND version=$4 RETURNING version`, org, office, []byte(doc), base, actor).Scan(&out)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		ce := &rewards.LayoutConflictError{}
		var cur []byte
		switch err := tx.QueryRow(ctx, `SELECT doc, version FROM office_layouts WHERE org_id=$1 AND office_id=$2`, org, office).Scan(&cur, &ce.Version); {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		default:
			ce.Layout = cur
		}
		return ce
	})
	return out, err
}
