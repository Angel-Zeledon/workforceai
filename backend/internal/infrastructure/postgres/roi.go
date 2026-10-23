package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.ROIStore = (*Store)(nil)

// GetROISettings reads the org's hours-saved model (migration 796).
func (s *Store) GetROISettings(ctx context.Context, orgID string) (application.ROISettings, bool, error) {
	var raw []byte
	v, err := one(ctx, s, orgID, func(r scanner) (application.ROISettings, error) {
		var v application.ROISettings
		var at time.Time
		err := r.Scan(&v.HourlyRateUSD, &v.DefaultMinutes, &raw, &v.UpdatedBy, &at)
		v.UpdatedAt = &at
		return v, err
	}, `SELECT hourly_rate_usd, default_minutes, role_minutes, updated_by, updated_at FROM roi_settings WHERE org_id=$1`, orgID)
	if err = mapErr(err); errors.Is(err, domain.ErrNotFound) {
		return application.ROISettings{}, false, nil
	} else if err != nil {
		return application.ROISettings{}, false, err
	}
	v.RoleMinutes = map[string]int{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &v.RoleMinutes)
	}
	return v, true, nil
}

// PutROISettings upserts the model.
func (s *Store) PutROISettings(ctx context.Context, orgID string, v application.ROISettings) error {
	if v.RoleMinutes == nil {
		v.RoleMinutes = map[string]int{}
	}
	at := time.Now().UTC()
	if v.UpdatedAt != nil {
		at = *v.UpdatedAt
	}
	return s.exec(ctx, orgID, `INSERT INTO roi_settings (org_id, hourly_rate_usd, default_minutes, role_minutes, updated_by, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (org_id) DO UPDATE SET hourly_rate_usd=EXCLUDED.hourly_rate_usd, default_minutes=EXCLUDED.default_minutes,
			role_minutes=EXCLUDED.role_minutes, updated_by=EXCLUDED.updated_by, updated_at=EXCLUDED.updated_at`,
		orgID, v.HourlyRateUSD, v.DefaultMinutes, jb(v.RoleMinutes), v.UpdatedBy, at)
}
