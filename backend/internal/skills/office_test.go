package skills

import (
	"context"
	"errors"
	"strings"
	"testing"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
)

// Skills of a department: who sees them, who writes them, who may give them to an agent.

type officeEnv struct {
	*env
	off *offices.Service
}

var (
	ana   = Actor{ID: "u-ana"}   // responsible of Legal (private)
	luis  = Actor{ID: "u-luis"}  // plain member of Legal
	marta = Actor{ID: "u-marta"} // Ventas, outside Legal
	boss  = Actor{ID: "u-owner", Admin: true}
)

func newOfficeEnv(t *testing.T) *officeEnv {
	t.Helper()
	e := newEnv(t, false)
	depts := []offices.Dept{{ID: "d-legal", Name: "Legal", HeadUser: "u-ana"}, {ID: "d-sales", Name: "Ventas"}}
	place := map[string]string{"u-ana": "d-legal", "u-luis": "d-legal", "u-marta": "d-sales"}
	agentDept := map[string]string{"a-elena": "d-legal", "a-valeria": "d-sales"}
	people := []offices.Person{{UserID: "u-owner", Name: "Dueña", Admin: true}, {UserID: "u-ana", Name: "Ana"}, {UserID: "u-luis", Name: "Luis"}, {UserID: "u-marta", Name: "Marta"}}
	agents := []offices.AgentRef{{ID: "a-elena", Name: "Elena"}, {ID: "a-valeria", Name: "Valeria"}, {ID: "a-sofia", Name: "Sofía"}}
	off := offices.New(offices.Config{Store: offices.NewMemStore(), TTL: 1,
		Departments: func(context.Context, string) ([]offices.Dept, error) { return depts, nil },
		Placements:  func(context.Context, string) (map[string]string, error) { return place, nil },
		AgentDepts:  func(context.Context, string) (map[string]string, error) { return agentDept, nil },
		People:      func(context.Context, string) ([]offices.Person, error) { return people, nil },
		Agents:      func(context.Context, string) ([]offices.AgentRef, error) { return agents, nil }})
	for dept, vis := range map[string]string{"d-legal": offices.Private, "d-sales": offices.Open} {
		if _, err := off.Enable(context.Background(), org, offices.Viewer{UserID: "u-owner", Admin: true}, dept, vis); err != nil {
			t.Fatal(err)
		}
	}
	e.svc.SetOffices(off.Model)
	prev := e.svc.cfg.AgentRole
	e.svc.cfg.AgentRole = func(c context.Context, o, id string) (string, error) {
		switch id {
		case "a-elena", "a-valeria", "a-sofia":
			return "assistant", nil
		}
		return prev(c, o, id)
	}
	return &officeEnv{env: e, off: off}
}

func officeIn(name, office string) Input {
	i := in(name)
	i.OfficeID = office
	return i
}

func names(l []Skill) map[string]bool {
	out := map[string]bool{}
	for _, s := range l {
		out[s.Name] = true
	}
	return out
}

func TestOfficeSkillCreatePermissions(t *testing.T) {
	e := newOfficeEnv(t)
	d, err := e.svc.Create(e.ctx, ana, officeIn("contract-review", "d-legal"))
	if err != nil {
		t.Fatalf("the responsible creates the skill of its office: %v", err)
	}
	if d.OfficeID != "d-legal" || d.OfficeName != "Legal" {
		t.Fatalf("office = %q %q", d.OfficeID, d.OfficeName)
	}
	if _, err := e.svc.Create(e.ctx, luis, officeIn("nda-checklist", "d-legal")); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a member who is not responsible: %v", err)
	}
	if _, err := e.svc.Create(e.ctx, marta, officeIn("legal-spy", "d-legal")); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("outsider: %v", err)
	}
	if _, err := e.svc.Create(e.ctx, boss, officeIn("sales-script", "d-sales")); err != nil {
		t.Fatalf("an admin creates for an open office: %v", err)
	}
	if _, err := e.svc.Create(e.ctx, ana, officeIn("ghost", "d-nope")); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown office: %v", err)
	}
	// a company skill keeps working as before
	c, err := e.svc.Create(e.ctx, luis, in("company-wide"))
	if err != nil || c.OfficeID != "" {
		t.Fatalf("company skill: %v %q", err, c.OfficeID)
	}
	// "general" is the company
	g, err := e.svc.Create(e.ctx, boss, officeIn("for-general", offices.GeneralID))
	if err != nil || g.OfficeID != "" {
		t.Fatalf("general: %v %q", err, g.OfficeID)
	}
}

func TestOfficeSkillListHidesPrivateOffice(t *testing.T) {
	e := newOfficeEnv(t)
	_, _ = e.svc.Create(e.ctx, ana, officeIn("contract-review", "d-legal"))
	_, _ = e.svc.Create(e.ctx, boss, officeIn("sales-script", "d-sales"))
	_, _ = e.svc.Create(e.ctx, boss, in("company-wide"))

	for _, c := range []struct {
		who  Actor
		want map[string]bool
	}{
		{marta, map[string]bool{"sales-script": true, "company-wide": true, "contract-review": false}},
		{luis, map[string]bool{"sales-script": true, "company-wide": true, "contract-review": true}},
		{boss, map[string]bool{"sales-script": true, "company-wide": true, "contract-review": false}}, // admin, not a member
	} {
		l, err := e.svc.List(e.ctx, c.who)
		if err != nil {
			t.Fatal(err)
		}
		got := names(l)
		for n, want := range c.want {
			if got[n] != want {
				t.Fatalf("%s sees %q = %v, want %v", c.who.ID, n, got[n], want)
			}
		}
	}
}

func TestOfficeSkillHiddenIsNotFound(t *testing.T) {
	e := newOfficeEnv(t)
	d, _ := e.svc.Create(e.ctx, ana, officeIn("contract-review", "d-legal"))
	for _, who := range []Actor{marta, boss} {
		if _, err := e.svc.Get(e.ctx, who, d.ID, 0); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s get: %v", who.ID, err)
		}
		if _, err := e.svc.Versions(e.ctx, who, d.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s versions: %v", who.ID, err)
		}
		if _, _, err := e.svc.Update(e.ctx, who, d.ID, officeIn("contract-review", "d-legal")); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s update: %v", who.ID, err)
		}
		if err := e.svc.Delete(e.ctx, who, d.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s delete: %v", who.ID, err)
		}
	}
	if _, err := e.svc.Get(e.ctx, luis, d.ID, 0); err != nil {
		t.Fatalf("a member reads it: %v", err)
	}
}

func TestOfficeSkillEditAndDelete(t *testing.T) {
	e := newOfficeEnv(t)
	d, _ := e.svc.Create(e.ctx, ana, officeIn("contract-review", "d-legal"))
	edit := officeIn("contract-review", "d-legal")
	edit.Instructions = "# changed\n"
	if _, _, err := e.svc.Update(e.ctx, luis, d.ID, edit); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member edits: %v", err)
	}
	if _, changed, err := e.svc.Update(e.ctx, ana, d.ID, edit); err != nil || !changed {
		t.Fatalf("responsible edits: %v %v", err, changed)
	}
	if err := e.svc.Delete(e.ctx, luis, d.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member deletes: %v", err)
	}
	if err := e.svc.Delete(e.ctx, ana, d.ID); err != nil {
		t.Fatalf("responsible deletes: %v", err)
	}
	// the department of a skill never changes
	o, _ := e.svc.Create(e.ctx, boss, officeIn("sales-script", "d-sales"))
	if _, _, err := e.svc.Update(e.ctx, boss, o.ID, officeIn("sales-script", "d-legal")); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("change of office: %v", err)
	}
	// a company skill is deleted by an admin only
	c, _ := e.svc.Create(e.ctx, luis, in("company-wide"))
	if err := e.svc.Delete(e.ctx, luis, c.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member deletes company skill: %v", err)
	}
	if err := e.svc.Delete(e.ctx, boss, c.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAssignRejectsAgentOfAnotherDepartment(t *testing.T) {
	e := newOfficeEnv(t)
	d, _ := e.svc.Create(e.ctx, ana, officeIn("contract-review", "d-legal"))
	err := e.svc.Assign(e.ctx, ana, "a-valeria", d.ID)
	if !errors.Is(err, domain.ErrInvalid) || CodeOf(err) == "" {
		t.Fatalf("agent of Ventas: %v", err)
	}
	if err := e.svc.Assign(e.ctx, ana, "a-sofia", d.ID); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("agent of General: %v", err)
	}
	if err := e.svc.Assign(e.ctx, ana, "a-elena", d.ID); err != nil {
		t.Fatalf("agent of Legal: %v", err)
	}
	// a company skill goes to anyone
	c, _ := e.svc.Create(e.ctx, boss, in("company-wide"))
	if err := e.svc.Assign(e.ctx, boss, "a-valeria", c.ID); err != nil {
		t.Fatal(err)
	}
}

func TestListWithoutOfficesShowsEverything(t *testing.T) {
	e := newEnv(t, false) // no offices service
	_, _ = e.svc.Create(e.ctx, member, in("plain"))
	l, _ := e.svc.List(e.ctx, member)
	if !names(l)["plain"] {
		t.Fatal("no offices service: all visible")
	}
}

func TestOfficeSkillFilesLiveInTheOfficeFolder(t *testing.T) {
	e := newOfficeEnv(t)
	d, err := e.svc.Create(e.ctx, ana, officeIn("contract-review", "d-legal"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.drv.FileByPath(e.ctx, "/Offices/Legal/Skills/contract-review/v1/SKILL.md"); err != nil {
		t.Fatalf("the files of a department's skill belong in its folder: %v", err)
	}
	if _, err := e.drv.FileByPath(e.ctx, "/Shared/Skills/contract-review/v1/SKILL.md"); err == nil {
		t.Fatal("a department's skill must not leave files in the shared folder")
	}
	// new versions, detail, restore and export read from there
	edit := officeIn("contract-review", "d-legal")
	edit.Instructions = "# v2\n"
	if _, _, err := e.svc.Update(e.ctx, ana, d.ID, edit); err != nil {
		t.Fatal(err)
	}
	if _, err := e.drv.FileByPath(e.ctx, "/Offices/Legal/Skills/contract-review/v2/SKILL.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Restore(e.ctx, ana, d.ID, 1); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, _, err := e.svc.Export(e.ctx, luis, d.ID, 1); err != nil {
		t.Fatalf("export: %v", err)
	}
	if _, err := e.svc.Review(e.ctx, boss, d.ID, true, 3, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an admin outside Legal reviews: %v", err)
	}
	if _, err := e.svc.Review(e.ctx, Actor{ID: "u-ana", Admin: true}, d.ID, true, 3, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Assign(e.ctx, ana, "a-elena", d.ID); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.Load(e.ctx, org, LoadCall{AgentID: "a-elena", Name: "contract-review"})
	if err != nil || !strings.Contains(got.Text, "/Offices/Legal/Skills/contract-review/v3/") {
		t.Fatalf("load: %v %q", err, got.Text)
	}
}

func TestOfficeSkillsFailClosedWhenTheModelFails(t *testing.T) {
	e := newOfficeEnv(t)
	d, _ := e.svc.Create(e.ctx, ana, officeIn("contract-review", "d-legal"))
	c, _ := e.svc.Create(e.ctx, boss, in("company-wide"))
	e.svc.SetOffices(func(context.Context, string) (*offices.Model, error) { return nil, errors.New("boom") })
	for _, who := range []Actor{ana, boss} {
		l, err := e.svc.List(e.ctx, who)
		if err != nil || names(l)["contract-review"] || !names(l)["company-wide"] {
			t.Fatalf("%s list: %v %v", who.ID, err, names(l))
		}
		if _, err := e.svc.Get(e.ctx, who, d.ID, 0); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s get: %v", who.ID, err)
		}
	}
	if err := e.svc.Assign(e.ctx, boss, "a-elena", d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("assign: %v", err)
	}
	if _, err := e.svc.Create(e.ctx, boss, officeIn("another", "d-legal")); err == nil {
		t.Fatal("create in a department while the model fails")
	}
	if err := e.svc.Assign(e.ctx, boss, "a-elena", c.ID); err != nil {
		t.Fatalf("company skills keep working: %v", err)
	}
	// the demo (Open) is not affected
	if l, _ := e.svc.List(e.ctx, Actor{ID: "x", Admin: true, Open: true}); !names(l)["contract-review"] {
		t.Fatal("open demo")
	}
}
