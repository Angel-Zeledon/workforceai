package postgres

import (
	"context"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/catalog"
	"aiworkforce/backend/internal/domain"
)

// Organization configuration (migration 230): settings, agent tones, schedules.
// Every call runs through WithOrgTx, so Row-Level Security applies.

var (
	_ application.ConfigStore = (*Store)(nil)
	_ application.OrgLister   = (*Store)(nil)
)

func (s *Store) GetOrgSettings(ctx context.Context, orgID string) (application.OrgSettings, error) {
	st, err := one(ctx, s, orgID, func(r scanner) (application.OrgSettings, error) {
		var o application.OrgSettings
		var rules []byte
		err := r.Scan(&o.Locale, &o.Tone, &o.BusinessType, &o.PackVersion, &o.OnboardedAt, &rules)
		if err == nil {
			unmarshal(rules, &o.Rules)
			o.Configured, o.Onboarded = true, o.OnboardedAt != nil
		}
		return o, err
	}, `SELECT locale, tone, business_type, pack_version, onboarding_completed_at, rules FROM org_settings WHERE org_id=$1`, orgID)
	if err != nil {
		if mapErr(err) == domain.ErrNotFound {
			return application.DefaultOrgSettings(), nil
		}
		return application.OrgSettings{}, err
	}
	if st.Rules.AlwaysApprove == nil {
		st.Rules.AlwaysApprove = []string{}
	}
	st.AgentTones, st.Recommendation = map[string]string{}, []string{}
	return st, nil
}

func (s *Store) PutOrgSettings(ctx context.Context, orgID string, o application.OrgSettings) error {
	return s.exec(ctx, orgID, `INSERT INTO org_settings (org_id, locale, tone, business_type, pack_version, onboarding_completed_at, rules)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (org_id) DO UPDATE SET locale=EXCLUDED.locale, tone=EXCLUDED.tone, business_type=EXCLUDED.business_type,
			pack_version=EXCLUDED.pack_version, onboarding_completed_at=EXCLUDED.onboarding_completed_at,
			rules=EXCLUDED.rules, updated_at=now()`,
		orgID, o.Locale, o.Tone, o.BusinessType, o.PackVersion, o.OnboardedAt, jb(catalogRules(o.Rules)))
}

func catalogRules(r catalog.PackRules) catalog.PackRules {
	if r.AlwaysApprove == nil {
		r.AlwaysApprove = []string{}
	}
	return r
}

func (s *Store) ListAgentTones(ctx context.Context, orgID string) (map[string]string, error) {
	type kv struct{ k, v string }
	rows, err := many(ctx, s, orgID, func(r scanner) (kv, error) {
		var x kv
		err := r.Scan(&x.k, &x.v)
		return x, err
	}, `SELECT agent_id, tone FROM agent_settings WHERE org_id=$1`, orgID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, x := range rows {
		out[x.k] = x.v
	}
	return out, nil
}

func (s *Store) SetAgentTone(ctx context.Context, orgID, agentID, tone string) error {
	if tone == "" {
		return s.exec(ctx, orgID, `DELETE FROM agent_settings WHERE org_id=$1 AND agent_id=$2`, orgID, agentID)
	}
	return s.exec(ctx, orgID, `INSERT INTO agent_settings (org_id, agent_id, tone) VALUES ($1,$2,$3)
		ON CONFLICT (org_id, agent_id) DO UPDATE SET tone=EXCLUDED.tone, updated_at=now()`, orgID, agentID, tone)
}

func (s *Store) SetAgentAutonomy(ctx context.Context, orgID, agentID, autonomy string) error {
	tag, err := s.execTag(ctx, orgID, `UPDATE agents SET autonomy=$3 WHERE org_id=$1 AND id=$2`, orgID, agentID, autonomy)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

const scheduleCols = `id, template_key, params, hour, minute, timezone, weekdays, enabled, next_run_at, last_run_at, created_at`

func scanSchedule(r scanner) (application.Schedule, error) {
	var sc application.Schedule
	var params, weekdays []byte
	if err := r.Scan(&sc.ID, &sc.TemplateKey, &params, &sc.Hour, &sc.Minute, &sc.Timezone, &weekdays,
		&sc.Enabled, &sc.NextRunAt, &sc.LastRunAt, &sc.CreatedAt); err != nil {
		return sc, err
	}
	unmarshal(params, &sc.Params)
	unmarshal(weekdays, &sc.Weekdays)
	if sc.Params == nil {
		sc.Params = map[string]string{}
	}
	if sc.Weekdays == nil {
		sc.Weekdays = []int{}
	}
	return sc, nil
}

func (s *Store) ListSchedules(ctx context.Context, orgID string) ([]application.Schedule, error) {
	return many(ctx, s, orgID, scanSchedule,
		`SELECT `+scheduleCols+` FROM schedules WHERE org_id=$1 ORDER BY created_at, id`, orgID)
}

func (s *Store) GetSchedule(ctx context.Context, orgID, id string) (application.Schedule, error) {
	sc, err := one(ctx, s, orgID, scanSchedule, `SELECT `+scheduleCols+` FROM schedules WHERE org_id=$1 AND id=$2`, orgID, id)
	return sc, mapErr(err)
}

func (s *Store) PutSchedule(ctx context.Context, orgID string, sc application.Schedule) error {
	if sc.Params == nil {
		sc.Params = map[string]string{}
	}
	if sc.Weekdays == nil {
		sc.Weekdays = []int{}
	}
	return s.exec(ctx, orgID, `INSERT INTO schedules (id, org_id, template_key, params, hour, minute, timezone, weekdays, enabled, next_run_at, last_run_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (id) DO UPDATE SET template_key=EXCLUDED.template_key, params=EXCLUDED.params, hour=EXCLUDED.hour,
			minute=EXCLUDED.minute, timezone=EXCLUDED.timezone, weekdays=EXCLUDED.weekdays, enabled=EXCLUDED.enabled,
			next_run_at=EXCLUDED.next_run_at, last_run_at=EXCLUDED.last_run_at`,
		sc.ID, orgID, sc.TemplateKey, jb(sc.Params), sc.Hour, sc.Minute, sc.Timezone, jb(sc.Weekdays), sc.Enabled,
		sc.NextRunAt, sc.LastRunAt, sc.CreatedAt)
}

func (s *Store) DeleteSchedule(ctx context.Context, orgID, id string) error {
	tag, err := s.execTag(ctx, orgID, `DELETE FROM schedules WHERE org_id=$1 AND id=$2`, orgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ClaimSchedule is an atomic compare-and-set on next_run_at.
func (s *Store) ClaimSchedule(ctx context.Context, orgID, id string, expected, next, ranAt time.Time) (bool, error) {
	tag, err := s.execTag(ctx, orgID, `UPDATE schedules SET next_run_at=$4, last_run_at=$5
		WHERE org_id=$1 AND id=$2 AND enabled AND next_run_at=$3`, orgID, id, expected, next, ranAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ListOrgIDs lists organizations through memberships, which (like users) is
// deliberately outside RLS (migration 201); tenant tables are never read here.
func (s *Store) ListOrgIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT org_id FROM memberships`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
