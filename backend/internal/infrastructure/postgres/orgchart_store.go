package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/orgchart"
)

// OrgChartStore implements orgchart.Store on Postgres (migrations 750-754). Row-Level Security applies.
type OrgChartStore struct{ S *Store }

var _ orgchart.Store = (*OrgChartStore)(nil)

func (o *OrgChartStore) ListDepartments(ctx context.Context, org string) ([]orgchart.Department, error) {
	var out []orgchart.Department
	err := o.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, parent_id, head_kind, head_id, cost_center, created_at, updated_at
			FROM departments WHERE org_id=$1 ORDER BY created_at, id`, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d orgchart.Department
			var hk string
			if err := rows.Scan(&d.ID, &d.Name, &d.ParentID, &hk, &d.Head.ID, &d.CostCenter, &d.CreatedAt, &d.UpdatedAt); err != nil {
				return err
			}
			d.Head.Kind = orgchart.Kind(hk)
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, err
}

func (o *OrgChartStore) PutDepartment(ctx context.Context, org string, d orgchart.Department) error {
	return o.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO departments (org_id, id, name, parent_id, head_kind, head_id, cost_center, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (org_id, id) DO UPDATE SET name=EXCLUDED.name, parent_id=EXCLUDED.parent_id, head_kind=EXCLUDED.head_kind,
				head_id=EXCLUDED.head_id, cost_center=EXCLUDED.cost_center, updated_at=EXCLUDED.updated_at`,
			org, d.ID, d.Name, d.ParentID, string(d.Head.Kind), d.Head.ID, d.CostCenter, d.CreatedAt, d.UpdatedAt)
		return err
	})
}

func (o *OrgChartStore) DeleteDepartment(ctx context.Context, org, id string) error {
	tag, err := o.S.execTag(ctx, org, `DELETE FROM departments WHERE org_id=$1 AND id=$2`, org, id)
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: department %s", domain.ErrNotFound, id)
	}
	return err
}

func (o *OrgChartStore) ListJobs(ctx context.Context, org string) ([]orgchart.Job, error) {
	var out []orgchart.Job
	err := o.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT agent_id, department_id, reports_to_kind, reports_to_id, escalation_kind, escalation_id,
			responsibilities, allowed_tools, schedule, budget_usd, updated_by, updated_at FROM agent_jobs WHERE org_id=$1 ORDER BY agent_id`, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var j orgchart.Job
			var rk, ek string
			var resp, tools, sched []byte
			if err := rows.Scan(&j.AgentID, &j.DepartmentID, &rk, &j.ReportsTo.ID, &ek, &j.Escalation.ID, &resp, &tools, &sched, &j.BudgetUSD, &j.UpdatedBy, &j.UpdatedAt); err != nil {
				return err
			}
			j.ReportsTo.Kind, j.Escalation.Kind = orgchart.Kind(rk), orgchart.Kind(ek)
			unmarshal(resp, &j.Responsibilities)
			unmarshal(tools, &j.AllowedTools)
			_ = json.Unmarshal(sched, &j.Schedule)
			j.Responsibilities, j.AllowedTools = strs(j.Responsibilities), strs(j.AllowedTools)
			if j.Schedule.Days == nil {
				j.Schedule.Days = []int{}
			}
			out = append(out, j)
		}
		return rows.Err()
	})
	return out, err
}

func (o *OrgChartStore) PutJob(ctx context.Context, org string, j orgchart.Job) error {
	return o.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO agent_jobs (org_id, agent_id, department_id, reports_to_kind, reports_to_id, escalation_kind, escalation_id,
			responsibilities, allowed_tools, schedule, budget_usd, updated_by, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT (org_id, agent_id) DO UPDATE SET department_id=EXCLUDED.department_id, reports_to_kind=EXCLUDED.reports_to_kind,
				reports_to_id=EXCLUDED.reports_to_id, escalation_kind=EXCLUDED.escalation_kind, escalation_id=EXCLUDED.escalation_id,
				responsibilities=EXCLUDED.responsibilities, allowed_tools=EXCLUDED.allowed_tools, schedule=EXCLUDED.schedule,
				budget_usd=EXCLUDED.budget_usd, updated_by=EXCLUDED.updated_by, updated_at=EXCLUDED.updated_at`,
			org, j.AgentID, j.DepartmentID, string(j.ReportsTo.Kind), j.ReportsTo.ID, string(j.Escalation.Kind), j.Escalation.ID,
			jb(strs(j.Responsibilities)), jb(strs(j.AllowedTools)), jb(j.Schedule), j.BudgetUSD, j.UpdatedBy, j.UpdatedAt)
		return err
	})
}

func (o *OrgChartStore) DeleteJob(ctx context.Context, org, agentID string) error {
	return o.S.exec(ctx, org, `DELETE FROM agent_jobs WHERE org_id=$1 AND agent_id=$2`, org, agentID)
}

func (o *OrgChartStore) ListPlacements(ctx context.Context, org string) ([]orgchart.Placement, error) {
	var out []orgchart.Placement
	err := o.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT user_id, department_id, title, reports_to_kind, reports_to_id, updated_at
			FROM org_people WHERE org_id=$1 ORDER BY user_id`, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p orgchart.Placement
			var rk string
			if err := rows.Scan(&p.UserID, &p.DepartmentID, &p.Title, &rk, &p.ReportsTo.ID, &p.UpdatedAt); err != nil {
				return err
			}
			p.ReportsTo.Kind = orgchart.Kind(rk)
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, err
}

func (o *OrgChartStore) PutPlacement(ctx context.Context, org string, p orgchart.Placement) error {
	return o.S.exec(ctx, org, `INSERT INTO org_people (org_id, user_id, department_id, title, reports_to_kind, reports_to_id, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (org_id, user_id) DO UPDATE SET department_id=EXCLUDED.department_id, title=EXCLUDED.title,
			reports_to_kind=EXCLUDED.reports_to_kind, reports_to_id=EXCLUDED.reports_to_id, updated_at=EXCLUDED.updated_at`,
		org, p.UserID, p.DepartmentID, p.Title, string(p.ReportsTo.Kind), p.ReportsTo.ID, p.UpdatedAt)
}

func (o *OrgChartStore) ListRules(ctx context.Context, org string) ([]orgchart.Rule, error) {
	var out []orgchart.Rule
	err := o.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, from_type, from_id, to_type, to_id, effect, require_approval, approver_kind, approver_id, note, created_by, created_at
			FROM delegation_rules WHERE org_id=$1 ORDER BY created_at, id`, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r orgchart.Rule
			var ft, tt, ak string
			if err := rows.Scan(&r.ID, &ft, &r.From.ID, &tt, &r.To.ID, &r.Effect, &r.RequireApproval, &ak, &r.Approver.ID, &r.Note, &r.CreatedBy, &r.CreatedAt); err != nil {
				return err
			}
			r.From.Type, r.To.Type, r.Approver.Kind = orgchart.SelectorType(ft), orgchart.SelectorType(tt), orgchart.Kind(ak)
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

func (o *OrgChartStore) PutRule(ctx context.Context, org string, r orgchart.Rule) error {
	return o.S.exec(ctx, org, `INSERT INTO delegation_rules (org_id, id, from_type, from_id, to_type, to_id, effect, require_approval, approver_kind, approver_id, note, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (org_id, id) DO UPDATE SET from_type=EXCLUDED.from_type, from_id=EXCLUDED.from_id, to_type=EXCLUDED.to_type, to_id=EXCLUDED.to_id,
			effect=EXCLUDED.effect, require_approval=EXCLUDED.require_approval, approver_kind=EXCLUDED.approver_kind, approver_id=EXCLUDED.approver_id, note=EXCLUDED.note`,
		org, r.ID, string(r.From.Type), r.From.ID, string(r.To.Type), r.To.ID, r.Effect, r.RequireApproval, string(r.Approver.Kind), r.Approver.ID, r.Note, r.CreatedBy, r.CreatedAt)
}

func (o *OrgChartStore) DeleteRule(ctx context.Context, org, id string) error {
	tag, err := o.S.execTag(ctx, org, `DELETE FROM delegation_rules WHERE org_id=$1 AND id=$2`, org, id)
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: rule %s", domain.ErrNotFound, id)
	}
	return err
}

func (o *OrgChartStore) ListApplied(ctx context.Context, org string) ([]orgchart.Applied, error) {
	var out []orgchart.Applied
	err := o.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT industry_key, version, locale, applied_by, applied_at, detail FROM org_industries WHERE org_id=$1 ORDER BY industry_key`, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a orgchart.Applied
			var d []byte
			if err := rows.Scan(&a.Key, &a.Version, &a.Locale, &a.AppliedBy, &a.AppliedAt, &d); err != nil {
				return err
			}
			_ = json.Unmarshal(d, &a.Detail)
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

func (o *OrgChartStore) GetApplied(ctx context.Context, org, key string) (orgchart.Applied, bool, error) {
	all, err := o.ListApplied(ctx, org)
	if err != nil {
		return orgchart.Applied{}, false, err
	}
	for _, a := range all {
		if a.Key == key {
			return a, true, nil
		}
	}
	return orgchart.Applied{}, false, nil
}

func (o *OrgChartStore) PutApplied(ctx context.Context, org string, a orgchart.Applied) error {
	return o.S.exec(ctx, org, `INSERT INTO org_industries (org_id, industry_key, version, locale, applied_by, applied_at, detail)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (org_id, industry_key) DO UPDATE SET version=EXCLUDED.version, locale=EXCLUDED.locale, applied_by=EXCLUDED.applied_by,
			applied_at=EXCLUDED.applied_at, detail=EXCLUDED.detail`,
		org, a.Key, a.Version, a.Locale, a.AppliedBy, a.AppliedAt, jb(a.Detail))
}

// SetAgentDepartment sets the department tag (a plain string; nil clears it) of an agent.
func (s *Store) SetAgentDepartment(ctx context.Context, orgID, id string, dept *string) error {
	tag, err := s.execTag(ctx, orgID, `UPDATE agents SET department=$3 WHERE org_id=$1 AND id=$2`, orgID, id, dept)
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: agent %s", domain.ErrNotFound, id)
	}
	return err
}

// SetAgentResponsibilities replaces the responsibilities of an agent (its job description).
func (s *Store) SetAgentResponsibilities(ctx context.Context, orgID, id string, resp []string) error {
	tag, err := s.execTag(ctx, orgID, `UPDATE agents SET responsibilities=$3 WHERE org_id=$1 AND id=$2`, orgID, id, jb(strs(resp)))
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: agent %s", domain.ErrNotFound, id)
	}
	return err
}

// SetAgentTools replaces the tool labels of an agent (a job description may only narrow them).
func (s *Store) SetAgentTools(ctx context.Context, orgID, id string, tools []string) error {
	tag, err := s.execTag(ctx, orgID, `UPDATE agents SET tools=$3 WHERE org_id=$1 AND id=$2`, orgID, id, jb(strs(tools)))
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: agent %s", domain.ErrNotFound, id)
	}
	return err
}
