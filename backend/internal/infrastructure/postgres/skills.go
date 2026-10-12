package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/skills"
)

// SkillStore implements skills.Store on Postgres (migration 510). Every call
// runs through WithOrgTx, so Row-Level Security applies; skill versions are
// append-only and skills are only soft-deleted.
type SkillStore struct{ S *Store }

var _ skills.Store = (*SkillStore)(nil)

const skillCols = `id, name, description, status, source, builtin, current_version, active_version, active_description,
	created_by, created_at, updated_at, reviewed_by, reviewed_at, review_note, deleted_at`

func scanSkill(r scanner) (skills.Skill, error) {
	var s skills.Skill
	err := r.Scan(&s.ID, &s.Name, &s.Description, &s.Status, &s.Source, &s.Builtin, &s.CurrentVersion, &s.ActiveVersion, &s.ActiveDescription,
		&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt, &s.ReviewedBy, &s.ReviewedAt, &s.ReviewNote, &s.DeletedAt)
	return s, err
}

const skillVersionCols = `skill_id, version, author, created_at, changelog, description, allowed_tools, files, warnings, source, restored_from, bytes`

func scanSkillVersion(r scanner) (skills.Version, error) {
	var v skills.Version
	var tools, files, warns []byte
	if err := r.Scan(&v.SkillID, &v.Version, &v.Author, &v.CreatedAt, &v.Changelog, &v.Description, &tools, &files, &warns, &v.Source, &v.RestoredFrom, &v.Bytes); err != nil {
		return v, err
	}
	unmarshal(tools, &v.AllowedTools)
	unmarshal(files, &v.Files)
	unmarshal(warns, &v.Warnings)
	if v.AllowedTools == nil {
		v.AllowedTools = []string{}
	}
	if v.Files == nil {
		v.Files = []skills.FileMeta{}
	}
	if v.Warnings == nil {
		v.Warnings = []skills.Warning{}
	}
	return v, nil
}

func insertSkillVersion(ctx context.Context, tx pgx.Tx, org string, v skills.Version) error {
	_, err := tx.Exec(ctx, `INSERT INTO skill_versions (org_id, skill_id, version, author, created_at, changelog, description, allowed_tools, files, warnings, source, restored_from, bytes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		org, v.SkillID, v.Version, v.Author, v.CreatedAt, v.Changelog, v.Description, jb(strs(v.AllowedTools)), jb(v.Files), jb(v.Warnings), v.Source, v.RestoredFrom, v.Bytes)
	return err
}

func (d *SkillStore) CreateSkill(ctx context.Context, org string, s skills.Skill, v skills.Version) error {
	return mapDrive(d.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO skills (org_id, id, name, description, status, source, builtin, current_version, active_version, active_description,
				created_by, created_at, updated_at, reviewed_by, reviewed_at, review_note)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
			org, s.ID, s.Name, s.Description, s.Status, s.Source, s.Builtin, s.CurrentVersion, s.ActiveVersion, s.ActiveDescription,
			s.CreatedBy, s.CreatedAt, s.UpdatedAt, s.ReviewedBy, s.ReviewedAt, s.ReviewNote)
		if err != nil {
			return err
		}
		return insertSkillVersion(ctx, tx, org, v)
	}))
}

func (d *SkillStore) GetSkill(ctx context.Context, org, id string) (skills.Skill, error) {
	s, err := one(ctx, d.S, org, scanSkill, `SELECT `+skillCols+` FROM skills WHERE org_id=$1 AND id=$2 AND deleted_at IS NULL`, org, id)
	return s, mapErr(err)
}

func (d *SkillStore) SkillByName(ctx context.Context, org, name string) (skills.Skill, error) {
	s, err := one(ctx, d.S, org, scanSkill, `SELECT `+skillCols+` FROM skills WHERE org_id=$1 AND lower(name)=lower($2)
		ORDER BY (deleted_at IS NULL) DESC, created_at DESC LIMIT 1`, org, name)
	return s, mapErr(err)
}

func (d *SkillStore) ListSkills(ctx context.Context, org string) ([]skills.Skill, error) {
	return many(ctx, d.S, org, func(r scanner) (skills.Skill, error) {
		var s skills.Skill
		err := r.Scan(&s.ID, &s.Name, &s.Description, &s.Status, &s.Source, &s.Builtin, &s.CurrentVersion, &s.ActiveVersion, &s.ActiveDescription,
			&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt, &s.ReviewedBy, &s.ReviewedAt, &s.ReviewNote, &s.DeletedAt, &s.Agents)
		return s, err
	}, `SELECT `+skillCols+`, (SELECT count(*) FROM skill_assignments a WHERE a.org_id = skills.org_id AND a.skill_id = skills.id)
		FROM skills WHERE org_id=$1 AND deleted_at IS NULL ORDER BY name`, org)
}

func (d *SkillStore) CountSkills(ctx context.Context, org string) (int, error) {
	var n int
	err := d.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM skills WHERE org_id=$1`, org).Scan(&n)
	})
	return n, err
}

func (d *SkillStore) AddVersion(ctx context.Context, org string, expected int, s skills.Skill, v skills.Version) error {
	return mapDrive(d.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE skills SET description=$4, status=$5, source=$6, builtin=$7, current_version=$8, active_version=$9, active_description=$10,
				updated_at=$11, reviewed_by=$12, reviewed_at=$13, review_note=$14, deleted_at=NULL
			WHERE org_id=$1 AND id=$2 AND current_version=$3`,
			org, s.ID, expected, s.Description, s.Status, s.Source, s.Builtin, s.CurrentVersion, s.ActiveVersion, s.ActiveDescription,
			s.UpdatedAt, s.ReviewedBy, s.ReviewedAt, s.ReviewNote)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrConflict
		}
		return insertSkillVersion(ctx, tx, org, v)
	}))
}

func (d *SkillStore) UpdateSkill(ctx context.Context, org string, s skills.Skill) error {
	tag, err := d.S.execTag(ctx, org, `UPDATE skills SET status=$3, active_version=$4, active_description=$5, updated_at=$6, reviewed_by=$7, reviewed_at=$8, review_note=$9
		WHERE org_id=$1 AND id=$2 AND deleted_at IS NULL`,
		org, s.ID, s.Status, s.ActiveVersion, s.ActiveDescription, s.UpdatedAt, s.ReviewedBy, s.ReviewedAt, s.ReviewNote)
	if err != nil {
		return mapDrive(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (d *SkillStore) DeleteSkill(ctx context.Context, org, id string, at time.Time) error {
	return d.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE skills SET deleted_at=$3, updated_at=$3 WHERE org_id=$1 AND id=$2 AND deleted_at IS NULL`, org, id, at)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		_, err = tx.Exec(ctx, `DELETE FROM skill_assignments WHERE org_id=$1 AND skill_id=$2`, org, id)
		return err
	})
}

func (d *SkillStore) Versions(ctx context.Context, org, skillID string) ([]skills.Version, error) {
	return many(ctx, d.S, org, scanSkillVersion, `SELECT `+skillVersionCols+` FROM skill_versions WHERE org_id=$1 AND skill_id=$2 ORDER BY version`, org, skillID)
}

func (d *SkillStore) VersionOf(ctx context.Context, org, skillID string, n int) (skills.Version, error) {
	v, err := one(ctx, d.S, org, scanSkillVersion, `SELECT `+skillVersionCols+` FROM skill_versions WHERE org_id=$1 AND skill_id=$2 AND version=$3`, org, skillID, n)
	return v, mapErr(err)
}

func (d *SkillStore) Assign(ctx context.Context, org string, a skills.Assignment) error {
	return d.S.exec(ctx, org, `INSERT INTO skill_assignments (org_id, agent_id, skill_id, assigned_by, assigned_at) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (org_id, agent_id, skill_id) DO NOTHING`, org, a.AgentID, a.SkillID, a.AssignedBy, a.AssignedAt)
}

func (d *SkillStore) Unassign(ctx context.Context, org, agentID, skillID string) error {
	return d.S.exec(ctx, org, `DELETE FROM skill_assignments WHERE org_id=$1 AND agent_id=$2 AND skill_id=$3`, org, agentID, skillID)
}

func scanAssignment(r scanner) (skills.Assignment, error) {
	var a skills.Assignment
	err := r.Scan(&a.AgentID, &a.SkillID, &a.AssignedBy, &a.AssignedAt)
	return a, err
}

func (d *SkillStore) AssignmentsOfAgent(ctx context.Context, org, agentID string) ([]skills.Assignment, error) {
	return many(ctx, d.S, org, scanAssignment, `SELECT agent_id, skill_id, assigned_by, assigned_at FROM skill_assignments a
		WHERE org_id=$1 AND agent_id=$2 AND EXISTS (SELECT 1 FROM skills s WHERE s.org_id=a.org_id AND s.id=a.skill_id AND s.deleted_at IS NULL)
		ORDER BY assigned_at`, org, agentID)
}

func (d *SkillStore) AssignmentsOfSkill(ctx context.Context, org, skillID string) ([]skills.Assignment, error) {
	return many(ctx, d.S, org, scanAssignment, `SELECT agent_id, skill_id, assigned_by, assigned_at FROM skill_assignments WHERE org_id=$1 AND skill_id=$2 ORDER BY assigned_at`, org, skillID)
}
