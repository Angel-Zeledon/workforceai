package offices

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
)

const org = "org-1"

type fixture struct {
	svc       *Service
	store     *MemStore
	depts     []Dept
	place     map[string]string
	agentDept map[string]string
	people    []Person
	agents    []AgentRef
	audits    []string
}

var (
	admin = Viewer{UserID: "u-owner", Admin: true}
	ana   = Viewer{UserID: "u-ana"}
	luis  = Viewer{UserID: "u-luis"}
	marta = Viewer{UserID: "u-marta"}
)

func newFix(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{store: NewMemStore(),
		depts:     []Dept{{ID: "d-legal", Name: "Legal", HeadUser: "u-ana"}, {ID: "d-sales", Name: "Ventas"}},
		place:     map[string]string{"u-ana": "d-legal", "u-luis": "d-legal", "u-marta": "d-sales"},
		agentDept: map[string]string{"a-elena": "d-legal", "a-valeria": "d-sales"},
		people: []Person{{UserID: "u-owner", Name: "Dueña", Admin: true}, {UserID: "u-ana", Name: "Ana"}, {UserID: "u-luis", Name: "Luis"},
			{UserID: "u-marta", Name: "Marta"}},
		agents: []AgentRef{{ID: "a-elena", Name: "Elena"}, {ID: "a-valeria", Name: "Valeria"}, {ID: "a-sofia", Name: "Sofía"}},
	}
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	f.svc = New(Config{Store: f.store,
		Departments: func(context.Context, string) ([]Dept, error) { return f.depts, nil },
		Placements:  func(context.Context, string) (map[string]string, error) { return f.place, nil },
		AgentDepts:  func(context.Context, string) (map[string]string, error) { return f.agentDept, nil },
		People:      func(context.Context, string) ([]Person, error) { return f.people, nil },
		Agents:      func(context.Context, string) ([]AgentRef, error) { return f.agents, nil },
		Audit: func(_ context.Context, _, _, action, _ string, _ map[string]any) {
			f.audits = append(f.audits, action)
		},
		Now: func() time.Time { now = now.Add(time.Second); return now },
	})
	return f
}

func (f *fixture) model(t *testing.T) *Model {
	t.Helper()
	m, err := f.svc.Model(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *fixture) enable(t *testing.T, dept, vis string) Office {
	t.Helper()
	o, err := f.svc.Enable(context.Background(), org, admin, dept, vis)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestGeneralOfficeWithoutRows(t *testing.T) {
	f := newFix(t)
	m := f.model(t)
	offs := m.Offices()
	if len(offs) != 1 || offs[0].ID != GeneralID || !offs[0].General || offs[0].Visibility != Open {
		t.Fatalf("offices = %+v", offs)
	}
	if !slices.Equal(offs[0].Members, []string{"u-ana", "u-luis", "u-marta", "u-owner"}) {
		t.Fatalf("general members = %v", offs[0].Members)
	}
	if !slices.Equal(offs[0].Leads, []string{"u-owner"}) {
		t.Fatalf("general leads = %v", offs[0].Leads)
	}
	if m.Restricted() {
		t.Fatal("an organization without offices nor rules must not be restricted")
	}
}

func TestEnableSeedsMembersAndLead(t *testing.T) {
	f := newFix(t)
	o := f.enable(t, "d-legal", "")
	if o.Visibility != Open || o.Name != "Legal" {
		t.Fatalf("office = %+v", o)
	}
	if !slices.Equal(o.Leads, []string{"u-ana"}) || !slices.Equal(o.Members, []string{"u-ana", "u-luis"}) {
		t.Fatalf("leads %v members %v", o.Leads, o.Members)
	}
	if !slices.Contains(f.audits, "office.enabled") {
		t.Fatalf("audits = %v", f.audits)
	}
	m := f.model(t)
	if len(m.Offices()) != 2 || !m.Restricted() {
		t.Fatalf("offices = %+v", m.Offices())
	}
	if got := m.OfficesOf("u-luis"); !slices.Equal(got, []string{GeneralID, "d-legal"}) {
		t.Fatalf("offices of luis = %v", got)
	}
	if _, err := f.svc.Enable(context.Background(), org, admin, "d-legal", ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("enable twice: %v", err)
	}
}

func TestEnableRules(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	if _, err := f.svc.Enable(ctx, org, admin, "d-none", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown department: %v", err)
	}
	if _, err := f.svc.Enable(ctx, org, ana, "d-legal", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-admin: %v", err)
	}
	if _, err := f.svc.Enable(ctx, org, admin, GeneralID, ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("general: %v", err)
	}
	if _, err := f.svc.Enable(ctx, org, admin, "d-legal", "secret"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("bad visibility: %v", err)
	}
}

func TestPrivateOfficeOnlyMembersEnter(t *testing.T) {
	f := newFix(t)
	f.enable(t, "d-legal", Private)
	m := f.model(t)
	if m.CanEnter(marta, "d-legal") || !m.CanEnter(luis, "d-legal") || !m.CanEnter(admin, "d-legal") {
		t.Fatal("private: only members and admins enter")
	}
	if !m.CanEnter(marta, GeneralID) {
		t.Fatal("general is open to everybody")
	}
	if _, err := f.svc.SetVisibility(context.Background(), org, ana, "d-legal", Open); err != nil {
		t.Fatal(err)
	}
	if !f.model(t).CanEnter(marta, "d-legal") {
		t.Fatal("open: anybody looks inside")
	}
	if _, err := f.svc.SetVisibility(context.Background(), org, admin, GeneralID, Private); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("general private: %v", err)
	}
}

func TestLeadManagesOwnOffice(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	f.enable(t, "d-legal", "")
	if _, err := f.svc.AddMember(ctx, org, luis, "d-legal", "u-marta", RoleMember); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member adds: %v", err)
	}
	o, err := f.svc.AddMember(ctx, org, ana, "d-legal", "u-marta", RoleMember)
	if err != nil || !slices.Contains(o.Members, "u-marta") {
		t.Fatalf("lead adds: %v %+v", err, o)
	}
	if _, err := f.svc.AddMember(ctx, org, ana, "d-legal", "u-ghost", RoleMember); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown person: %v", err)
	}
	if _, err := f.svc.SetVisibility(ctx, org, marta, "d-legal", Private); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member changes visibility: %v", err)
	}
	// anybody may leave an office
	if _, err := f.svc.RemoveMember(ctx, org, marta, "d-legal", "u-marta"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AddMember(ctx, org, luis, GeneralID, "u-marta", RoleMember); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("general members are everybody: %v", err)
	}
}

func TestLastLeadRemoved(t *testing.T) {
	f := newFix(t)
	f.enable(t, "d-legal", "")
	o, err := f.svc.RemoveMember(context.Background(), org, ana, "d-legal", "u-ana")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(o.Leads, []string{"u-owner"}) {
		t.Fatalf("an office without a lead is managed by the admins: %v", o.Leads)
	}
	if f.model(t).CanManage(ana, "d-legal") || !f.model(t).CanManage(admin, "d-legal") {
		t.Fatal("manage rights after the last lead left")
	}
}

func TestDisableOfficeWithAgents(t *testing.T) {
	f := newFix(t)
	ctx := context.Background()
	f.enable(t, "d-legal", "")
	_ = f.store.PutAgentAccess(ctx, org, AgentAccess{AgentID: "a-elena", OwnerKind: OwnerOffice, OwnerID: "d-legal", Visibility: VisOffice})
	f.svc.invalidate(org)
	if err := f.svc.Disable(ctx, org, admin, "d-legal"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("disable with agents: %v", err)
	}
	_ = f.store.DeleteAgentAccess(ctx, org, "a-elena")
	if err := f.svc.Disable(ctx, org, ana, "d-legal"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("lead disables: %v", err)
	}
	if err := f.svc.Disable(ctx, org, admin, "d-legal"); err != nil {
		t.Fatal(err)
	}
	if len(f.model(t).Offices()) != 1 {
		t.Fatal("office still there")
	}
}

func TestDeletedDepartmentDropsOffice(t *testing.T) {
	f := newFix(t)
	f.enable(t, "d-legal", "")
	f.depts = f.depts[1:]
	f.svc.invalidate(org)
	if len(f.model(t).Offices()) != 1 {
		t.Fatal("an office whose department is gone is not listed")
	}
}
