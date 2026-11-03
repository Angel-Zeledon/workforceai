package rewards_test

import (
	"errors"
	"strings"
	"testing"

	"aiworkforce/backend/internal/rewards"
)

// E2: each office has its own layout; General keeps the organization's one.
func TestOfficeLayoutPerOffice(t *testing.T) {
	f := layoutFixture(t, "plant_cactus")
	svc, ctx := f.svc, f.ctx
	legal := `{"v":1,"floor":{"kind":"wood"},"pieces":[{"uid":"p1","item":"plant_cactus","x":1,"z":1}]}`
	if d, err := svc.GetLayoutFor(ctx, "org-a", "d-legal"); err != nil || d.Version != 0 || d.Layout != nil {
		t.Fatalf("new office = %+v %v", d, err)
	}
	if d, err := svc.SaveLayoutFor(ctx, "org-a", "d-legal", "u1", []byte(legal), 0); err != nil || d.Version != 1 {
		t.Fatalf("save office: %+v %v", d, err)
	}
	if d, _ := svc.GetLayout(ctx, "org-a"); d.Version != 0 {
		t.Fatal("an office's layout must not touch the organization's (General) one")
	}
	if d, _ := svc.GetLayoutFor(ctx, "org-a", "general"); d.Version != 0 {
		t.Fatal("General is the organization's layout")
	}
	if d, _ := svc.GetLayoutFor(ctx, "org-a", "d-legal"); d.Version != 1 || !strings.Contains(string(d.Layout), "plant_cactus") {
		t.Fatalf("office layout = %s", d.Layout)
	}
	if _, err := svc.SaveLayoutFor(ctx, "org-a", "d-legal", "u2", []byte(`{"v":1}`), 0); !errors.Is(err, rewards.ErrLayoutConflict) {
		t.Fatalf("stale base: %v", err)
	}
	if _, err := svc.SaveLayoutFor(ctx, "org-b", "d-legal", "u1", []byte(legal), 0); !errors.Is(err, rewards.ErrNotOwned) {
		t.Fatalf("items of another organization: %v", err)
	}
	if d, err := svc.SaveLayoutFor(ctx, "org-a", "", "u1", []byte(`{"v":1}`), 0); err != nil || d.Version != 1 {
		t.Fatalf("empty office = General: %+v %v", d, err)
	}
}
