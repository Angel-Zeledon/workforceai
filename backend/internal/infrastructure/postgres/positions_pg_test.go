package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/positions"
)

// Integration test of migration 830 (TEST_DATABASE_URL, see store_rls_test.go): positions round trip, deleting one
// re-parents its subordinates and frees its holders, names are unique per department, and another organization
// neither reads nor assigns them.
func TestPositionsPostgres(t *testing.T) {
	env := testStore(t)
	st := &PositionsStore{S: env.Store}
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	now := time.Now().UTC()
	boss := positions.Position{ID: "pos_boss", OfficeID: "d-acct", Name: "Gerente", Skills: []string{"s1"},
		Library: []positions.Folder{{Path: "/Offices/Contabilidad", Level: positions.LevelEdit}}, Template: positions.TemplateManager, CreatedAt: now, UpdatedAt: now}
	mid := positions.Position{ID: "pos_mid", OfficeID: "d-acct", Name: "Medio", ReportsTo: boss.ID, CreatedAt: now.Add(time.Second), UpdatedAt: now}
	low := positions.Position{ID: "pos_low", OfficeID: "d-acct", Name: "Bajo", ReportsTo: mid.ID, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now}
	if err := st.SavePositions(ctx, "org-a", []positions.Position{boss, mid, low}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetPosition(ctx, "org-a", boss.ID)
	if err != nil || got.Library[0].Level != positions.LevelEdit || got.Skills[0] != "s1" || got.Template != positions.TemplateManager {
		t.Fatalf("round trip = %+v %v", got, err)
	}
	dup := positions.Position{ID: "pos_dup", OfficeID: "d-acct", Name: "gerente", CreatedAt: now, UpdatedAt: now}
	if err := st.SavePositions(ctx, "org-a", []positions.Position{dup}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate name = %v", err)
	}
	if err := st.SetAssignment(ctx, "org-a", positions.Assignment{AgentID: "valeria", PositionID: mid.ID, OfficeID: "d-acct", AssignedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAssignment(ctx, "org-b", positions.Assignment{AgentID: "valeria", PositionID: mid.ID, OfficeID: "d-acct", AssignedAt: now}); err == nil {
		t.Fatal("org-b assigns org-a's position")
	}
	if err := st.DeletePosition(ctx, "org-a", mid.ID); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.GetPosition(ctx, "org-a", low.ID); p.ReportsTo != boss.ID {
		t.Fatalf("re-parented = %+v", p)
	}
	if _, err := st.AssignmentOf(ctx, "org-a", "valeria"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("holder freed = %v", err)
	}
	if _, err := st.GetPosition(ctx, "org-b", boss.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("org-b reads org-a's position: %v", err)
	}
	err = env.Store.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM office_positions WHERE org_id='org-a'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("org-b sees %d org-a positions", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
