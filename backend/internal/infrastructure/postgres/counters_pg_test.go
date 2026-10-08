package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/counters"
)

// Windowed counters (migration 360): round trip, upsert, tenant isolation
// under RLS, and the down migration.
func TestWindowCountersRoundTripAndIsolation(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")

	if b, err := st.LoadCounters(ctx, "org-a", counters.ScopeAnomaly); err != nil || b != nil {
		t.Fatalf("missing snapshot = %s, %v", b, err)
	}
	if err := st.SaveCounters(ctx, "org-a", counters.ScopePolicyLimits, []byte(`{"mail|sales":[{"at":"2026-10-08T10:00:00Z"}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCounters(ctx, "org-a", counters.ScopePolicyLimits, []byte(`{"k":[]}`)); err != nil { // upsert
		t.Fatal(err)
	}
	b, err := st.LoadCounters(ctx, "org-a", counters.ScopePolicyLimits)
	if err != nil || string(b) != `{"k": []}` {
		t.Fatalf("snapshot = %s, %v", b, err)
	}
	if b, err := st.LoadCounters(ctx, "org-b", counters.ScopePolicyLimits); err != nil || b != nil {
		t.Fatalf("org-b reads org-a counters: %s %v", b, err)
	}
	err = st.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM window_counters").Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("org-b transaction sees %d rows of org-a", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Writing a row of another org is rejected by the policy.
	if err := st.exec(ctx, "org-b", `INSERT INTO window_counters (org_id, scope, state) VALUES ('org-a','x','{}')`); err == nil {
		t.Error("org-b wrote a counter of org-a")
	}

	down, err := os.ReadFile("../../../migrations/down/360_window_counters_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	var reg *string
	_ = owner.QueryRow(ctx, `SELECT to_regclass('window_counters')::text`).Scan(&reg)
	if reg != nil {
		t.Fatalf("down left the table: %s", *reg)
	}
	up, err := migrationsFS.ReadFile("migrations/360_window_counters.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down (%d): %v", i, err)
		}
	}
}
