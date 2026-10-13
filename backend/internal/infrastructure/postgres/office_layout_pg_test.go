package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/rewards"
)

// Office layout (migration 660): the same store contract as the in-memory store, a real compare-and-set under
// concurrency, tenant isolation under RLS and the rollback.
func TestOfficeLayoutStorePostgres(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-l1")
	seedOrg(t, st, "org-l2")

	// Contract (mirrors TestMemStoreLayoutContract in internal/rewards).
	if doc, v, err := st.GetLayout(ctx, "org-l1"); err != nil || doc != nil || v != 0 {
		t.Fatalf("empty = %s %d %v", doc, v, err)
	}
	if v, err := st.SaveLayout(ctx, "org-l1", json.RawMessage(`{"v":1,"a":1}`), 0, "u1"); err != nil || v != 1 {
		t.Fatalf("first save = %d %v", v, err)
	}
	if v, err := st.SaveLayout(ctx, "org-l1", json.RawMessage(`{"v":1,"a":2}`), 1, "u1"); err != nil || v != 2 {
		t.Fatalf("second save = %d %v", v, err)
	}
	_, err := st.SaveLayout(ctx, "org-l1", json.RawMessage(`{"v":1,"a":3}`), 1, "u2")
	var ce *rewards.LayoutConflictError
	if !errors.As(err, &ce) || !errors.Is(err, rewards.ErrLayoutConflict) || ce.Version != 2 || !strings.Contains(string(ce.Layout), `"a": 2`) {
		t.Fatalf("stale save = %v %+v", err, ce)
	}
	if _, err := st.SaveLayout(ctx, "org-l1", json.RawMessage(`{}`), 0, "u2"); !errors.Is(err, rewards.ErrLayoutConflict) {
		t.Fatalf("base 0 over a stored layout = %v", err)
	}
	if _, err := st.SaveLayout(ctx, "org-l2", json.RawMessage(`{}`), 3, "u2"); !errors.As(err, &ce) || ce.Version != 0 || ce.Layout != nil {
		t.Fatalf("base>0 with nothing stored = %v %+v", err, ce)
	}
	if doc, v, _ := st.GetLayout(ctx, "org-l1"); v != 2 || !strings.Contains(string(doc), `"a": 2`) {
		t.Fatalf("stored = %s %d", doc, v)
	}
	var by string
	if err := st.WithOrgTx(ctx, "org-l1", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT updated_by FROM office_layout`).Scan(&by)
	}); err != nil || by != "u1" {
		t.Fatalf("updated_by = %q %v", by, err)
	}

	// Tenant isolation.
	if _, v, _ := st.GetLayout(ctx, "org-l2"); v != 0 {
		t.Fatal("org-l2 sees org-l1's layout")
	}
	err = st.WithOrgTx(ctx, "org-l2", func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM office_layout`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("org-l2 transaction sees %d rows", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.exec(ctx, "org-l2", `INSERT INTO office_layout (org_id, doc, version) VALUES ('org-l1', '{}', 9)`); err == nil {
		t.Error("org-l2 wrote a layout of org-l1")
	}
	if err := st.exec(ctx, "org-l1", `UPDATE office_layout SET version = 0`); err == nil {
		t.Error("version 0 accepted")
	}

	// Concurrency: exactly one of many saves from the same version wins; the first save races on the key.
	for _, base := range []int{2, 0} {
		org := "org-l1"
		if base == 0 {
			org = "org-l2"
		}
		var wg sync.WaitGroup
		var wins, conflicts atomic.Int32
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := st.SaveLayout(ctx, org, json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)), base, "u")
				switch {
				case err == nil:
					wins.Add(1)
				case errors.Is(err, rewards.ErrLayoutConflict):
					conflicts.Add(1)
				default:
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if _, v, _ := st.GetLayout(ctx, org); wins.Load() != 1 || conflicts.Load() != 11 || v != base+1 {
			t.Fatalf("base %d: wins=%d conflicts=%d version=%d", base, wins.Load(), conflicts.Load(), v)
		}
	}

	// Rollback and re-apply (idempotent).
	down, err := os.ReadFile("../../../migrations/down/660_office_layout_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	var reg *string
	_ = owner.QueryRow(ctx, `SELECT to_regclass('office_layout')::text`).Scan(&reg)
	if reg != nil {
		t.Fatalf("down left the table: %s", *reg)
	}
	up, err := migrationsFS.ReadFile("migrations/660_office_layout.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down (%d): %v", i, err)
		}
	}
}
