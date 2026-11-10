package offices

import (
	"context"
	"errors"
	"testing"

	"aiworkforce/backend/internal/domain"
)

func (f *fixture) setAccess(t *testing.T, v Viewer, agent string, in AccessInput) EffectiveAccess {
	t.Helper()
	ea, err := f.svc.SetAgentAccess(context.Background(), org, v, agent, in)
	if err != nil {
		t.Fatal(err)
	}
	return ea
}

func wantRights(t *testing.T, m *Model, v Viewer, agent string, want Rights) {
	t.Helper()
	if got := m.Rights(v, agent); got != want {
		t.Fatalf("rights of %s on %s = %+v, want %+v", v.UserID, agent, got, want)
	}
}

func TestAgentDefaultsToGeneral(t *testing.T) {
	f := newFix(t)
	m := f.model(t)
	if ea := m.Agent("a-elena"); ea.OfficeID != GeneralID || ea.Visibility != VisOffice || ea.Explicit {
		t.Fatalf("default access = %+v", ea)
	}
	wantRights(t, m, marta, "a-elena", Rights{See: true, Use: true})
	wantRights(t, m, admin, "a-elena", Rights{See: true, Use: true, Edit: true})
	wantRights(t, m, marta, "a-new-agent", Rights{See: true, Use: true}) // an agent hired a second ago
}

func TestAgentInOfficeDepartment(t *testing.T) {
	f := newFix(t)
	f.enable(t, "d-legal", "")
	m := f.model(t)
	if ea := m.Agent("a-elena"); ea.OfficeID != "d-legal" || ea.OwnerKind != OwnerOffice {
		t.Fatalf("elena = %+v", ea)
	}
	wantRights(t, m, marta, "a-elena", Rights{See: true}) // open office: seen, not used
	wantRights(t, m, luis, "a-elena", Rights{See: true, Use: true})
	wantRights(t, m, ana, "a-elena", Rights{See: true, Use: true, Edit: true})
	wantRights(t, m, marta, "a-valeria", Rights{See: true, Use: true}) // Ventas is not an office: General
	if _, err := f.svc.SetVisibility(context.Background(), org, admin, "d-legal", Private); err != nil {
		t.Fatal(err)
	}
	wantRights(t, f.model(t), marta, "a-elena", Rights{})
}

func TestPrivatePersonalAgent(t *testing.T) {
	f := newFix(t)
	f.enable(t, "d-legal", "")
	ea := f.setAccess(t, admin, "a-sofia", AccessInput{OwnerKind: OwnerPerson, OwnerID: "u-luis", Visibility: VisPrivate})
	if ea.OfficeID != "d-legal" {
		t.Fatalf("home office of luis's agent = %q", ea.OfficeID)
	}
	m := f.model(t)
	wantRights(t, m, luis, "a-sofia", Rights{See: true, Use: true, Edit: true})
	wantRights(t, m, ana, "a-sofia", Rights{}) // the lead does not open a person's private agent
	wantRights(t, m, marta, "a-sofia", Rights{})
	wantRights(t, m, admin, "a-sofia", Rights{See: true, Edit: true}) // listed and reassignable, not used
}

func TestSharedWithPeople(t *testing.T) {
	f := newFix(t)
	f.setAccess(t, admin, "a-sofia", AccessInput{OwnerKind: OwnerPerson, OwnerID: "u-luis", Visibility: VisPeople,
		Shares: []Share{{UserID: "u-marta", Level: ShareChat}, {UserID: "u-ana", Level: ShareEdit}}})
	m := f.model(t)
	wantRights(t, m, marta, "a-sofia", Rights{See: true, Use: true})
	wantRights(t, m, ana, "a-sofia", Rights{See: true, Use: true, Edit: true})
	// a personal agent shared with people is not the whole office (here General = everybody)
	wantRights(t, m, Viewer{UserID: "u-owner"}, "a-sofia", Rights{})
}

func TestDirectoryAgent(t *testing.T) {
	f := newFix(t)
	f.enable(t, "d-legal", Private)
	f.setAccess(t, ana, "a-elena", AccessInput{Visibility: VisDirectory})
	wantRights(t, f.model(t), marta, "a-elena", Rights{See: true, Use: true})
}

func TestDepartedOwnerPassesToLead(t *testing.T) {
	f := newFix(t)
	f.enable(t, "d-legal", "")
	f.setAccess(t, admin, "a-sofia", AccessInput{OwnerKind: OwnerPerson, OwnerID: "u-luis", Visibility: VisPrivate})
	f.people = f.people[:2] // Luis (and Marta) left the organization
	f.svc.invalidate(org)
	m := f.model(t)
	ea := m.Agent("a-sofia")
	if !ea.Departed || ea.PreviousOwner != "u-luis" || ea.OwnerKind != OwnerOffice || ea.OwnerID != "d-legal" {
		t.Fatalf("departed = %+v", ea)
	}
	wantRights(t, m, ana, "a-sofia", Rights{See: true, Use: true, Edit: true})
}

func TestRightsTwoOffices(t *testing.T) {
	f := newFix(t)
	f.enable(t, "d-legal", Private)
	f.enable(t, "d-sales", Private)
	m := f.model(t)
	wantRights(t, m, marta, "a-elena", Rights{})
	if _, err := f.svc.AddMember(context.Background(), org, ana, "d-legal", "u-marta", RoleMember); err != nil {
		t.Fatal(err)
	}
	m = f.model(t)
	wantRights(t, m, marta, "a-elena", Rights{See: true, Use: true})
	wantRights(t, m, marta, "a-valeria", Rights{See: true, Use: true})
}

func TestOnlyEditorsChangeAccess(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	f.enable(t, "d-legal", "")
	f.enable(t, "d-sales", "")
	try := func(v Viewer, agent string, in AccessInput) error {
		_, err := f.svc.SetAgentAccess(ctx, org, v, agent, in)
		return err
	}
	if err := try(marta, "a-elena", AccessInput{Visibility: VisDirectory}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("outsider: %v", err)
	}
	if err := try(luis, "a-elena", AccessInput{OwnerKind: OwnerPerson, OwnerID: "u-luis"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member takes it: %v", err)
	}
	if err := try(ana, "a-elena", AccessInput{OwnerKind: OwnerOffice, OwnerID: "d-sales"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("lead gives it to an office she is not in: %v", err)
	}
	if err := try(ana, "a-elena", AccessInput{Visibility: "everyone"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("bad visibility: %v", err)
	}
	if err := try(ana, "a-elena", AccessInput{Shares: []Share{{UserID: "u-ghost", Level: ShareChat}}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown share: %v", err)
	}
	if err := try(ana, "a-ghost", AccessInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown agent: %v", err)
	}
	ea := f.setAccess(t, ana, "a-elena", AccessInput{OwnerKind: OwnerPerson, OwnerID: "u-luis", Visibility: VisOffice})
	if ea.OwnerID != "u-luis" || ea.OfficeID != "d-legal" || !ea.Explicit {
		t.Fatalf("lead gives it to a member: %+v", ea)
	}
	if !contains(f.audits, "agent.access_changed") {
		t.Fatalf("audits = %v", f.audits)
	}
	// keeping the owner: only the visibility changes
	if ea := f.setAccess(t, luis, "a-elena", AccessInput{Visibility: VisDirectory}); ea.OwnerID != "u-luis" || ea.Visibility != VisDirectory {
		t.Fatalf("visibility only: %+v", ea)
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func TestCanSeeWork(t *testing.T) {
	f := newFix(t)
	m := f.model(t)
	if !m.CanSeeWork(marta, "a-elena") { // no offices: General
		t.Fatal("General agent hidden")
	}
	f.enable(t, "d-legal", "")
	m = f.model(t)
	if !m.CanSeeWork(marta, "a-elena") { // open office
		t.Fatal("open office agent hidden from a non member")
	}
	if !m.CanSeeWork(marta, "a-unknown") || !m.CanSeeWork(marta, "a-valeria") { // unknown / not an office: General
		t.Fatal("General agent hidden")
	}
	if _, err := f.svc.SetVisibility(context.Background(), org, admin, "d-legal", Private); err != nil {
		t.Fatal(err)
	}
	m = f.model(t)
	if !m.CanSeeWork(luis, "a-elena") || !m.CanSeeWork(ana, "a-elena") {
		t.Fatal("private office agent hidden from a member")
	}
	if m.CanSeeWork(marta, "a-elena") {
		t.Fatal("private office agent seen by a non member")
	}
	if m.CanSeeWork(admin, "a-elena") {
		t.Fatal("private office agent seen by an admin who is not a member")
	}
	if !m.CanSeeWork(admin, "a-valeria") {
		t.Fatal("General agent hidden from the admin")
	}
}
