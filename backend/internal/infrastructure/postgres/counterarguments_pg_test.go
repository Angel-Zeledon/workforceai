package postgres

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
)

func counterRow(approvalID, text string) domain.Counterargument {
	return domain.Counterargument{ApprovalID: approvalID, Counterargument: text, Risks: []string{"importe", "destinatario"}, Checks: []string{"verifica"},
		Locale: "es", Model: "sim", Provider: "simulation", CostUSD: 0.004, GeneratedBy: "ana", CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Advisory: true}
}

// Devil's advocate cache (migration 788): one review per approval (the first wins), JSON lists round trip,
// tenant isolation under RLS and cleanup by Reset.
func TestCounterargumentStorageAndIsolation(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")

	if _, err := st.GetCounterargument(ctx, "org-a", "ap1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get before put = %v", err)
	}
	first := counterRow("ap1", "primera")
	got, inserted, err := st.PutCounterargument(ctx, "org-a", first)
	if err != nil || !inserted || got.Counterargument != "primera" {
		t.Fatalf("put = %+v %v %v", got, inserted, err)
	}
	// A second generation does not overwrite the first one: the stored row is returned.
	got, inserted, err = st.PutCounterargument(ctx, "org-a", counterRow("ap1", "segunda"))
	if err != nil || inserted || got.Counterargument != "primera" {
		t.Fatalf("second put = %+v %v %v", got, inserted, err)
	}
	back, err := st.GetCounterargument(ctx, "org-a", "ap1")
	if err != nil || back.Counterargument != "primera" || len(back.Risks) != 2 || back.Checks[0] != "verifica" || back.CostUSD != 0.004 ||
		back.GeneratedBy != "ana" || !back.Advisory || !back.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("round trip = %+v %v", back, err)
	}

	// Concurrent writers keep exactly one row.
	var wg sync.WaitGroup
	var winners int
	var mu sync.Mutex
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ins, err := st.PutCounterargument(ctx, "org-a", counterRow("ap2", "x")); err == nil && ins {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("%d concurrent inserts won, want 1", winners)
	}

	// Tenant isolation: another organization neither reads nor overwrites the review, nor writes into org-a.
	if _, err := st.GetCounterargument(ctx, "org-b", "ap1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("org-b get = %v", err)
	}
	if _, ins, err := st.PutCounterargument(ctx, "org-b", counterRow("ap1", "de b")); err != nil || !ins {
		t.Fatalf("org-b put of the same approval id = %v %v", ins, err)
	}
	if got, _ := st.GetCounterargument(ctx, "org-a", "ap1"); got.Counterargument != "primera" {
		t.Error("org-b changed an org-a row")
	}
	err = st.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM approval_counterarguments WHERE org_id='org-a'`).Scan(&n); err != nil {
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
	if err := st.exec(ctx, "org-b", `INSERT INTO approval_counterarguments (org_id, approval_id, counterargument) VALUES ('org-a','x','y')`); err == nil {
		t.Error("org-b wrote a row for org-a")
	}

	// Reset clears the cache together with the approvals it belongs to.
	if err := st.Reset(ctx, "org-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetCounterargument(ctx, "org-a", "ap1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("after reset = %v", err)
	}
	if _, err := st.GetCounterargument(ctx, "org-b", "ap1"); err != nil {
		t.Errorf("reset of org-a removed an org-b row: %v", err)
	}
}

func TestMigration788DownThenUpAgain(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	if _, _, err := st.PutCounterargument(ctx, "org-a", counterRow("ap1", "x")); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../../migrations/down/788_approval_counterarguments_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	var reg *string
	_ = owner.QueryRow(ctx, `SELECT to_regclass('approval_counterarguments')::text`).Scan(&reg)
	if reg != nil {
		t.Fatalf("down left the table: %s", *reg)
	}
	up, err := migrationsFS.ReadFile("migrations/788_approval_counterarguments.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down (%d): %v", i, err)
		}
	}
	// RLS is back on after re-applying.
	var forced bool
	_ = owner.QueryRow(ctx, `SELECT relforcerowsecurity FROM pg_class WHERE relname='approval_counterarguments'`).Scan(&forced)
	if !forced {
		t.Error("row level security is not forced after the up migration")
	}
}
