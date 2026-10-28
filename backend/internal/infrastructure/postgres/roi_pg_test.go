package postgres

import (
	"context"
	"os"
	"testing"

	"aiworkforce/backend/internal/application"
)

// AI payroll model (migration 796): upsert round trip, nullable rate, tenant isolation, and down then up again.
func TestROISettingsPostgres(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-r1")
	seedOrg(t, st, "org-r2")
	if _, ok, err := st.GetROISettings(ctx, "org-r1"); err != nil || ok {
		t.Fatalf("empty = %v %v", ok, err)
	}
	rate := 42.5
	if err := st.PutROISettings(ctx, "org-r1", application.ROISettings{HourlyRateUSD: &rate, DefaultMinutes: 20, RoleMinutes: map[string]int{"legal": 90}, UpdatedBy: "ana"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.GetROISettings(ctx, "org-r1")
	if err != nil || !ok || got.HourlyRateUSD == nil || *got.HourlyRateUSD != 42.5 || got.DefaultMinutes != 20 || got.RoleMinutes["legal"] != 90 || got.UpdatedBy != "ana" {
		t.Fatalf("round trip = %+v %v %v", got, ok, err)
	}
	// Clearing the rate stores NULL (hours only).
	if err := st.PutROISettings(ctx, "org-r1", application.ROISettings{DefaultMinutes: 20}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := st.GetROISettings(ctx, "org-r1"); got.HourlyRateUSD != nil || got.RoleMinutes == nil {
		t.Fatalf("cleared = %+v", got)
	}
	if _, ok, _ := st.GetROISettings(ctx, "org-r2"); ok {
		t.Error("org-r2 reads org-r1's model")
	}
	if err := st.exec(ctx, "org-r2", `INSERT INTO roi_settings (org_id, default_minutes) VALUES ('org-r1', 5) ON CONFLICT (org_id) DO UPDATE SET default_minutes=5`); err == nil {
		t.Error("org-r2 wrote org-r1's row")
	}

	down, err := os.ReadFile("../../../migrations/down/796_roi_settings_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	up, err := migrationsFS.ReadFile("migrations/796_roi_settings.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down (%d): %v", i, err)
		}
	}
	var forced bool
	_ = owner.QueryRow(ctx, `SELECT relforcerowsecurity FROM pg_class WHERE relname='roi_settings'`).Scan(&forced)
	if !forced {
		t.Error("RLS not forced after re-applying 796")
	}
}
