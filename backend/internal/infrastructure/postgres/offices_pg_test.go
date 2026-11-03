package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"aiworkforce/backend/internal/rewards"

	"aiworkforce/backend/internal/offices/officestest"
)

// E2 offices (migration 815): the store contract, with tenant isolation.
func TestPGOfficesContract(t *testing.T) {
	st := testStore(t).Store
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")
	officestest.Run(t, &OfficesStore{S: st}, "org-a", "org-b")
}

// E2: one 3D layout per office (migration 815), compare-and-set on the version, apart from the organization's one.
func TestPGOfficeLayouts(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	if v, err := st.SaveOfficeLayout(ctx, "org-a", "d1", json.RawMessage(`{"v":1}`), 0, "u1"); err != nil || v != 1 {
		t.Fatalf("first save: %d %v", v, err)
	}
	if _, err := st.SaveOfficeLayout(ctx, "org-a", "d1", json.RawMessage(`{"v":1}`), 0, "u2"); !errors.Is(err, rewards.ErrLayoutConflict) {
		t.Fatalf("stale: %v", err)
	}
	if doc, v, _ := st.GetLayout(ctx, "org-a"); v != 0 || doc != nil {
		t.Fatal("an office layout must not touch office_layout")
	}
	if _, v, _ := st.GetOfficeLayout(ctx, "org-a", "d1"); v != 1 {
		t.Fatalf("version = %d", v)
	}
}
