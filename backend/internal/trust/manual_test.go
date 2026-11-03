package trust_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"aiworkforce/backend/internal/domain"
)

func TestChooseLevelRespectsCeilingControlsAndPrincipals(t *testing.T) {
	f := newFixture(t)
	// data_analyst starts at approve_each, ceiling autonomous; sales has ceiling rules.
	if err := f.svc.ChooseLevel(f.ctx, "sales", "autonomous", "owner-1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("above the ceiling: %v", err)
	}
	if err := f.svc.ChooseLevel(f.ctx, "data_analyst", "rules", "agent:data_analyst"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("agents never choose: %v", err)
	}
	f.blocked = "kill_switch_active"
	if err := f.svc.ChooseLevel(f.ctx, "data_analyst", "rules", "owner-1"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("raising waits while the kill switch is on: %v", err)
	}
	if err := f.svc.ChooseLevel(f.ctx, "sales", "approve_each", "owner-1"); err != nil || f.level("sales") != "approve_each" {
		t.Fatalf("lowering is never blocked: %v %s", err, f.level("sales"))
	}
	f.blocked = ""
	if err := f.svc.ChooseLevel(f.ctx, "data_analyst", "autonomous", "owner-1"); err != nil || f.level("data_analyst") != "autonomous" {
		t.Fatalf("a person may raise up to the ceiling: %v %s", err, f.level("data_analyst"))
	}
	found := false
	for _, a := range f.mem.Audit() {
		if a.Action == "agent.autonomy_changed" && a.EntityID == "data_analyst" && fmt.Sprint(a.Details) != "" && strings.Contains(fmt.Sprint(a.Details), "permissions.manual") && a.Actor == "owner-1" {
			found = true
		}
	}
	if !found {
		t.Fatal("the change is audited with its source and actor")
	}
}
