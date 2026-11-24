package offices

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
)

// multiFix: Legal (lead Ana, member Luis) and Ventas (lead Marta) are offices; Elena is Legal's agent; the flag is on.
func multiFix(t *testing.T, on bool) *fixture {
	t.Helper()
	f := newFix(t)
	f.svc.cfg.MultiOffice = on
	f.enable(t, "d-legal", "")
	f.enable(t, "d-sales", "")
	if _, err := f.svc.AddMember(context.Background(), org, admin, "d-sales", "u-marta", RoleLead); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) grant(v Viewer, agent, office string) (AgentOfficesView, error) {
	return f.svc.GrantOffice(context.Background(), org, v, agent, office)
}

func wantForbidden(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v, want forbidden", err)
	}
}

func TestExtraOfficeLifecycle(t *testing.T) {
	f := multiFix(t, true)
	wantRights(t, f.model(t), marta, "a-elena", Rights{See: true}) // Legal is open: seen, not used
	v, err := f.grant(ana, "a-elena", "d-sales")
	if err != nil || len(v.Offices) != 1 || v.Offices[0].Status != StatusPending || v.BaseOffice.ID != "d-legal" {
		t.Fatalf("grant = %+v %v", v, err)
	}
	wantRights(t, f.model(t), marta, "a-elena", Rights{See: true}) // pending: no access yet
	v, err = f.svc.AcceptOffice(context.Background(), org, marta, "a-elena", "d-sales")
	if err != nil || v.Offices[0].Status != StatusAccepted || v.Offices[0].AcceptedBy != "u-marta" {
		t.Fatalf("accept = %+v %v", v, err)
	}
	wantRights(t, f.model(t), marta, "a-elena", Rights{See: true, Use: true}) // Use, never Edit
	if g := f.model(t).Agent("a-elena"); g.OfficeID != "d-legal" {
		t.Fatalf("the base office must not change: %+v", g)
	}
	// removing cuts the access at once
	if _, err := f.svc.RemoveOffice(context.Background(), org, ana, "a-elena", "d-sales"); err != nil {
		t.Fatal(err)
	}
	wantRights(t, f.model(t), marta, "a-elena", Rights{See: true})
	if !slices.Equal(f.audits[len(f.audits)-3:], []string{"agent.office_granted", "agent.office_accepted", "agent.office_removed"}) {
		t.Fatalf("audits = %v", f.audits)
	}
}

func TestExtraOfficeOnlyMembersOfThatOffice(t *testing.T) {
	f := multiFix(t, true)
	f.grant(admin, "a-elena", "d-sales") // admin leads both: accepted at once
	wantRights(t, f.model(t), marta, "a-elena", Rights{See: true, Use: true})
	wantRights(t, f.model(t), luis, "a-elena", Rights{See: true, Use: true}) // Luis was already in Legal
	f2 := multiFix(t, true)
	f2.grant(admin, "a-valeria", "d-legal") // Valeria is General's (Ventas is not her office)
	wantRights(t, f2.model(t), luis, "a-valeria", Rights{See: true, Use: true})
}

func TestExtraOfficePermissions(t *testing.T) {
	f := multiFix(t, true)
	ctx := context.Background()
	_, err := f.grant(luis, "a-elena", "d-sales") // a member, not a lead
	wantForbidden(t, err)
	_, err = f.grant(marta, "a-elena", "d-sales") // leads the other office, not the base one
	wantForbidden(t, err)
	if _, err := f.grant(ana, "a-elena", "d-sales"); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.AcceptOffice(ctx, org, ana, "a-elena", "d-sales") // leads the base office, not that one
	wantForbidden(t, err)
	_, err = f.svc.AcceptOffice(ctx, org, luis, "a-elena", "d-sales")
	wantForbidden(t, err)
	_, err = f.svc.RemoveOffice(ctx, org, luis, "a-elena", "d-sales")
	wantForbidden(t, err)
	if _, err := f.svc.AcceptOffice(ctx, org, admin, "a-elena", "d-sales"); err != nil { // an admin does both
		t.Fatal(err)
	}
	if _, err := f.svc.RemoveOffice(ctx, org, marta, "a-elena", "d-sales"); err != nil { // the other office may leave too
		t.Fatal(err)
	}
}

func TestExtraOfficeOtherOrganization(t *testing.T) {
	f := multiFix(t, true)
	f.grant(admin, "a-elena", "d-sales")
	other := "org-2"
	_, err := f.svc.GrantOffice(context.Background(), other, admin, "a-elena", "d-sales")
	if !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("grant in another organization: %v", err)
	}
	m, _ := f.svc.Model(context.Background(), other)
	if len(m.AgentOffices("a-elena")) != 0 {
		t.Fatal("extra offices leak to another organization")
	}
}

func TestExtraOfficeIdempotent(t *testing.T) {
	f := multiFix(t, true)
	ctx := context.Background()
	f.grant(ana, "a-elena", "d-sales")
	f.grant(ana, "a-elena", "d-sales")
	f.svc.AcceptOffice(ctx, org, marta, "a-elena", "d-sales")
	f.svc.AcceptOffice(ctx, org, marta, "a-elena", "d-sales")
	f.grant(ana, "a-elena", "d-sales") // offering an accepted agent again keeps it accepted
	v, err := f.svc.RemoveOffice(ctx, org, ana, "a-elena", "d-sales")
	if err != nil || len(v.Offices) != 0 {
		t.Fatalf("remove = %+v %v", v, err)
	}
	if _, err := f.svc.RemoveOffice(ctx, org, ana, "a-elena", "d-sales"); err != nil { // nothing there: no error
		t.Fatal(err)
	}
	n := map[string]int{}
	for _, a := range f.audits {
		n[a]++
	}
	if n["agent.office_granted"] != 1 || n["agent.office_accepted"] != 1 || n["agent.office_removed"] != 1 {
		t.Fatalf("audits = %v", n)
	}
	if _, err := f.svc.AcceptOffice(ctx, org, marta, "a-elena", "d-sales"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("accept what was not offered: %v", err)
	}
}

func TestExtraOfficeInvalid(t *testing.T) {
	f := multiFix(t, true)
	if _, err := f.grant(ana, "a-elena", "d-legal"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("own base office: %v", err)
	}
	if _, err := f.grant(admin, "a-elena", "d-nowhere"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing office: %v", err)
	}
	if _, err := f.grant(admin, "a-ghost", "d-sales"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing agent: %v", err)
	}
	f.setAccess(t, admin, "a-sofia", AccessInput{OwnerKind: OwnerPerson, OwnerID: "u-luis", Visibility: VisPrivate})
	if _, err := f.grant(admin, "a-sofia", "d-sales"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("personal agent: %v", err)
	}
}

func TestExtraOfficePrivateIsNotRevealed(t *testing.T) {
	f := multiFix(t, true)
	f.grant(admin, "a-elena", "d-sales")
	if _, err := f.svc.SetVisibility(context.Background(), org, admin, "d-legal", Private); err != nil {
		t.Fatal(err)
	}
	// Elena's base office is private and Marta is not in it: she may use Elena through Ventas, but cannot see her work
	// nor learn which offices Elena has
	wantRights(t, f.model(t), marta, "a-elena", Rights{See: true, Use: true})
	mv, err := f.svc.AgentOffices(context.Background(), org, marta, "a-elena")
	if err != nil || mv.BaseOffice.ID != "" || mv.BaseOffice.Name != "" {
		t.Fatalf("marta uses her through Ventas but must not learn Legal: %+v %v", mv, err)
	}
	// somebody with no way to the agent is told it does not exist
	if _, err := f.svc.AgentOffices(context.Background(), org, Viewer{UserID: "u-nobody"}, "a-elena"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("outsider: %v", err)
	}
	// a private extra office is not named to who cannot enter it
	f2 := multiFix(t, true)
	f2.svc.SetVisibility(context.Background(), org, admin, "d-sales", Private)
	f2.grant(admin, "a-elena", "d-sales")
	v, err := f2.svc.AgentOffices(context.Background(), org, luis, "a-elena")
	if err != nil || len(v.Offices) != 0 {
		t.Fatalf("luis sees %+v %v", v, err)
	}
	if v, _ := f2.svc.AgentOffices(context.Background(), org, ana, "a-elena"); len(v.Offices) != 1 { // she leads the base office
		t.Fatalf("ana sees %+v", v)
	}
}

func TestExtraOfficeFlagOff(t *testing.T) {
	f := multiFix(t, false)
	_, err := f.grant(admin, "a-elena", "d-sales")
	wantForbidden(t, err)
	_, err = f.svc.AcceptOffice(context.Background(), org, marta, "a-elena", "d-sales")
	wantForbidden(t, err)
	// a row stored before (or by hand) gives nothing while the flag is off
	now := time.Now()
	f.store.PutAgentOffice(context.Background(), org, AgentOffice{AgentID: "a-elena", OfficeID: "d-sales", GrantedBy: "u-ana", AcceptedBy: "u-marta", CreatedAt: now, AcceptedAt: &now})
	f.svc.invalidate(org)
	wantRights(t, f.model(t), marta, "a-elena", Rights{See: true})
	v, err := f.svc.AgentOffices(context.Background(), org, admin, "a-elena")
	if err != nil || v.Enabled || len(v.Offices) != 0 {
		t.Fatalf("view = %+v %v", v, err)
	}
	// ...and it can be cleaned up
	if _, err := f.svc.RemoveOffice(context.Background(), org, admin, "a-elena", "d-sales"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := f.store.ListAgentOffices(context.Background(), org); len(rows) != 0 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestOnlyGeneralBehavesAsBefore(t *testing.T) {
	f := newFix(t)
	f.svc.cfg.MultiOffice = true
	m := f.model(t)
	if m.Restricted() {
		t.Fatal("an organization with only General must stay unrestricted")
	}
	v, err := f.svc.AgentOffices(context.Background(), org, marta, "a-elena")
	if err != nil || v.BaseOffice.ID != GeneralID || v.BaseOffice.Name != "General" || len(v.Offices) != 0 {
		t.Fatalf("view = %+v %v", v, err)
	}
	wantRights(t, m, marta, "a-elena", Rights{See: true, Use: true})
}

func TestBaseOfficeViewIsTheModelsEffective(t *testing.T) {
	f := multiFix(t, true)
	for _, a := range f.agents {
		v, err := f.svc.AgentOffices(context.Background(), org, admin, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if want := f.model(t).Agent(a.ID).OfficeID; v.BaseOffice.ID != want {
			t.Fatalf("%s: view says %s, model says %s", a.ID, v.BaseOffice.ID, want)
		}
	}
}
