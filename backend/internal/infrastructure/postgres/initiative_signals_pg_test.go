package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
)

// Migration 800: suggestion params and per-signal settings round trip; rows saved before 800 read the defaults;
// down drops both columns and up restores them (idempotent).
func TestInitiativeSignalsPostgres(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-s1")
	at := time.Now().UTC().Truncate(time.Microsecond)

	it := initiativeRow("p1", "accounting", "art:s1!b5", at)
	it.Signal, it.Params = application.SignalMetricChange, map[string]string{"change": "7.0", "unit": "points"}
	if err := st.CreateInitiative(ctx, "org-s1", it); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateInitiative(ctx, "org-s1", initiativeRow("p2", "sales", "x", at)); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetInitiative(ctx, "org-s1", "p1")
	if err != nil || got.Params["change"] != "7.0" || got.Params["unit"] != "points" {
		t.Fatalf("params = %+v %v", got, err)
	}
	if plain, _ := st.GetInitiative(ctx, "org-s1", "p2"); plain.Params != nil {
		t.Fatalf("a suggestion without params reads nil, got %+v", plain.Params)
	}

	// Saved without signals ('{}'): the defaults are read back.
	if err := st.PutInitiativeSettings(ctx, "org-s1", application.InitiativeSettings{Enabled: true, DailyLimit: 3, MonthlyLimit: 30}); err != nil {
		t.Fatal(err)
	}
	s, _, err := st.GetInitiativeSettings(ctx, "org-s1")
	if err != nil || s.Signals == nil || *s.Signals != application.DefaultSignalSettings() {
		t.Fatalf("default signals = %+v %v", s.Signals, err)
	}
	custom := application.DefaultSignalSettings()
	custom.SpendPace.Enabled, custom.ApprovalAging.MinAgeHours = false, 48
	if err := st.PutInitiativeSettings(ctx, "org-s1", application.InitiativeSettings{Enabled: true, DailyLimit: 3, MonthlyLimit: 30, Signals: &custom}); err != nil {
		t.Fatal(err)
	}
	s, _, _ = st.GetInitiativeSettings(ctx, "org-s1")
	if s.Signals == nil || *s.Signals != custom {
		t.Fatalf("signals = %+v", s.Signals)
	}

	down, err := os.ReadFile("../../../migrations/down/800_initiative_signals_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	var n int
	_ = owner.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE (table_name='initiatives' AND column_name='params') OR (table_name='initiative_settings' AND column_name='signals')`).Scan(&n)
	if n != 0 {
		t.Fatalf("down left %d columns", n)
	}
	up, err := migrationsFS.ReadFile("migrations/800_initiative_signals.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down (%d): %v", i, err)
		}
	}
	// Rows survive the round trip (without their params / signals, which were dropped).
	if got, err := st.GetInitiative(ctx, "org-s1", "p1"); err != nil || got.Params != nil || got.Title == "" {
		t.Fatalf("after down/up = %+v %v", got, err)
	}
	if s, ok, err := st.GetInitiativeSettings(ctx, "org-s1"); err != nil || !ok || *s.Signals != application.DefaultSignalSettings() {
		t.Fatalf("settings after down/up = %+v %v %v", s, ok, err)
	}
}
