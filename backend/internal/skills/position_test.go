package skills

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"aiworkforce/backend/internal/roles"
)

// Skills of a position (docs/architecture/positions.md): they come after the direct assignments and before the
// trade's defaults, and the run list is cut at MaxRunSkills in that order.
func TestPositionSkillsPriorityAndCap(t *testing.T) {
	e := newEnv(t, false)
	active := func(name string) Skill {
		d, err := e.svc.Create(e.ctx, admin, in(name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Review(e.ctx, admin, d.ID, true, 1, ""); err != nil {
			t.Fatal(err)
		}
		return d.Skill
	}
	hr, _ := roles.Get("hr")
	var roleNames []string
	for _, n := range hr.DefaultSkills {
		active(n)
		roleNames = append(roleNames, n)
	}
	var posIDs []string
	for i := 0; i < 11; i++ {
		posIDs = append(posIDs, active(fmt.Sprintf("pos-%c", 'a'+i)).ID)
	}
	for _, n := range []string{"zz-direct", "zy-direct"} {
		if err := e.svc.Assign(e.ctx, admin, "hr", active(n).ID); err != nil {
			t.Fatal(err)
		}
	}
	e.svc.cfg.PositionSkills = func(_ context.Context, _, agent string) (string, []string, error) {
		if agent == "hr" {
			return "Asistente de gerente", posIDs, nil
		}
		return "", nil, nil
	}
	list, err := e.svc.AgentSkills(e.ctx, "hr")
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Name != "zy-direct" || list[1].Name != "zz-direct" || list[2].Via != ViaPosition || list[2].PositionName != "Asistente de gerente" || list[len(list)-1].Via != ViaRole {
		t.Fatalf("order: direct, position, trade: %+v", list[:3])
	}
	refs := e.svc.SkillRefs(e.ctx, org, "hr")
	if len(refs) != MaxRunSkills || refs[0].Name != "zy-direct" || refs[1].Name != "zz-direct" {
		t.Fatalf("refs: %+v", refs)
	}
	for _, r := range refs {
		if slices.Contains(roleNames, r.Name) {
			t.Fatalf("the trade's defaults come last and fall off the cap: %+v", refs)
		}
	}
	if err := e.svc.CanLoad(e.ctx, org, "hr", "pos-c"); err != nil {
		t.Fatalf("a position skill can be loaded: %v", err)
	}
	if err := e.svc.CanLoad(e.ctx, org, "sales", "pos-c"); err == nil {
		t.Fatal("another agent cannot load it")
	}
	// an unknown position gives nothing (fewer skills, never more)
	e.svc.cfg.PositionSkills = func(context.Context, string, string) (string, []string, error) { return "", nil, errors.New("down") }
	if err := e.svc.CanLoad(e.ctx, org, "hr", "pos-c"); err == nil {
		t.Fatal("a failing position source must not serve its skills")
	}
}
