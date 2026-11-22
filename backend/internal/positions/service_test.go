package positions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/offices"
)

const org = "o1"

type fakeFolders struct{}

func (fakeFolders) Bounds(_ context.Context, _, office string) (Bounds, error) {
	switch office {
	case "d-acct":
		return Bounds{Office: "/Offices/Contabilidad", Shared: "/Shared", SharedIn: []string{"/Offices/Legal/Contratos"}}, nil
	case "d-legal":
		return Bounds{Office: "/Offices/Legal", Shared: "/Shared"}, nil
	}
	return Bounds{Shared: "/Shared"}, nil
}

func (fakeFolders) FolderExists(_ context.Context, _, p string) (bool, error) {
	return !strings.Contains(p, "NoExiste"), nil
}

type fakeCatalog []SkillInfo

func (c fakeCatalog) Skills(context.Context, string) ([]SkillInfo, error) { return c, nil }

type auditRec struct {
	mu sync.Mutex
	as []string
}

func (r *auditRec) add(_ context.Context, _, _, action, _ string, _ map[string]any) {
	r.mu.Lock()
	r.as = append(r.as, action)
	r.mu.Unlock()
}

func (r *auditRec) has(a string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.as, a)
}

var (
	owner = Actor{ID: "u-owner", Admin: true}
	lead  = Actor{ID: "u-ana"}   // responsible of Contabilidad (open)
	mem   = Actor{ID: "u-luis"}  // plain member of Contabilidad
	legal = Actor{ID: "u-marta"} // responsible of Legal (private)
	agent = Actor{ID: "agent:valeria"}
)

type env struct {
	svc       *Service
	ctx       context.Context
	audit     *auditRec
	agentDept map[string]string
	move      func(agentID, dept string)
}

func newEnv(t *testing.T) *env {
	t.Helper()
	depts := []offices.Dept{{ID: "d-acct", Name: "Contabilidad", HeadUser: "u-ana"}, {ID: "d-legal", Name: "Legal", HeadUser: "u-marta"}}
	place := map[string]string{"u-ana": "d-acct", "u-luis": "d-acct", "u-marta": "d-legal"}
	agentDept := map[string]string{"valeria": "d-acct", "pedro": "d-acct", "elena": "d-legal"}
	people := []offices.Person{{UserID: "u-owner", Name: "Dueña", Admin: true}, {UserID: "u-ana", Name: "Ana"}, {UserID: "u-luis", Name: "Luis"}, {UserID: "u-marta", Name: "Marta"}}
	agents := []offices.AgentRef{{ID: "valeria", Name: "Valeria"}, {ID: "pedro", Name: "Pedro"}, {ID: "elena", Name: "Elena"}}
	var mu sync.Mutex
	off := offices.New(offices.Config{Store: offices.NewMemStore(), TTL: 1,
		Departments: func(context.Context, string) ([]offices.Dept, error) { return depts, nil },
		Placements:  func(context.Context, string) (map[string]string, error) { return place, nil },
		AgentDepts: func(context.Context, string) (map[string]string, error) {
			mu.Lock()
			defer mu.Unlock()
			return maps(agentDept), nil
		},
		People: func(context.Context, string) ([]offices.Person, error) { return people, nil },
		Agents: func(context.Context, string) ([]offices.AgentRef, error) { return agents, nil }})
	ctx := application.WithOrg(context.Background(), org)
	for dept, vis := range map[string]string{"d-acct": offices.Open, "d-legal": offices.Private} {
		if _, err := off.Enable(ctx, org, offices.Viewer{UserID: "u-owner", Admin: true}, dept, vis); err != nil {
			t.Fatal(err)
		}
	}
	rec := &auditRec{}
	svc := New(Config{Store: NewMemStore(), Model: off.Model, Folders: fakeFolders{}, Audit: rec.add, OrgID: org,
		Skills: fakeCatalog{{ID: "s-exec", Name: "executive-summary"}, {ID: "s-agenda", Name: "meeting-agenda"}, {ID: "s-minutes", Name: "meeting-minutes"},
			{ID: "s-bank", Name: "bank-reconciliation", OfficeID: "d-acct"}, {ID: "s-nda", Name: "nda-review", OfficeID: "d-legal"}}})
	e := &env{svc: svc, ctx: ctx, audit: rec, agentDept: agentDept}
	e.move = func(agentID, dept string) {
		mu.Lock()
		agentDept[agentID] = dept
		mu.Unlock()
		time.Sleep(20 * time.Millisecond) // the offices model is cached for TTL (1 ns; the Windows clock is coarser)
	}
	return e
}

func maps(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func manager() Input {
	return Input{Name: "Gerente de Contabilidad", Template: TemplateManager, Skills: []string{"s-exec", "s-agenda", "s-bank"},
		Library: []Folder{{Path: "/Offices/Contabilidad", Level: LevelEdit}, {Path: "/Shared", Level: LevelRead}}}
}

func TestCreatePermissionsAndSealing(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Create(e.ctx, lead, "d-acct", manager()); err != nil {
		t.Fatalf("the responsible creates: %v", err)
	}
	if _, err := e.svc.Create(e.ctx, mem, "d-acct", Input{Name: "Otro"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a plain member: %v", err)
	}
	if _, err := e.svc.Create(e.ctx, agent, "d-acct", Input{Name: "Robot"}); !errors.Is(err, domain.ErrForbidden) || CodeOf(err) != "humans_only" {
		t.Fatalf("an agent never: %v", err)
	}
	if _, err := e.svc.Create(e.ctx, owner, "d-acct", Input{Name: "Especialista"}); err != nil {
		t.Fatalf("an admin: %v", err)
	}
	// Legal is private: whoever is not a member gets "not found", the admin included
	for _, a := range []Actor{owner, lead} {
		if _, err := e.svc.List(e.ctx, a, "d-legal"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s lists a private department: %v", a.ID, err)
		}
		if _, err := e.svc.Create(e.ctx, a, "d-legal", Input{Name: "x"}); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%s creates in a private department: %v", a.ID, err)
		}
	}
	if _, err := e.svc.Create(e.ctx, legal, "d-legal", Input{Name: "Abogada"}); err != nil {
		t.Fatalf("its responsible: %v", err)
	}
	if _, err := e.svc.List(e.ctx, lead, offices.GeneralID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("General has no positions: %v", err)
	}
	v, err := e.svc.List(e.ctx, mem, "d-acct")
	if err != nil || len(v.Positions) != 2 || v.CanManage {
		t.Fatalf("a member sees, does not manage: %+v %v", v, err)
	}
	if !e.audit.has("position.created") {
		t.Fatal("audited")
	}
}

func TestValidation(t *testing.T) {
	e := newEnv(t)
	cases := map[string]Input{
		"name_required":        {Name: " "},
		"folder_outside":       {Name: "a", Library: []Folder{{Path: "/Offices/Legal/Casos", Level: LevelRead}}},
		"folder_level_invalid": {Name: "b", Library: []Folder{{Path: "/Offices/Legal/Contratos", Level: LevelEdit}}},
		"folder_invalid":       {Name: "c", Library: []Folder{{Path: "/Shared/NoExiste", Level: LevelRead}}},
		"skill_invalid":        {Name: "d", Skills: []string{"s-nda"}}, // another department's skill is not revealed
		"reports_to_invalid":   {Name: "e", ReportsTo: "pos_nope"},
	}
	for code, in := range cases {
		if _, err := e.svc.Create(e.ctx, lead, "d-acct", in); CodeOf(err) != code {
			t.Errorf("%s: got %v", code, err)
		}
	}
	if _, err := e.svc.Create(e.ctx, lead, "d-acct", Input{Name: "Lector", Library: []Folder{{Path: "/Offices/Legal/Contratos", Level: LevelRead}}}); err != nil {
		t.Fatalf("what is shared with the department can be read: %v", err)
	}
	if _, err := e.svc.Create(e.ctx, lead, "d-acct", Input{Name: "lector"}); CodeOf(err) != "name_taken" {
		t.Fatalf("names are unique per department: %v", err)
	}
}

func TestSubordinateNeverExceedsItsManager(t *testing.T) {
	e := newEnv(t)
	boss, err := e.svc.Create(e.ctx, lead, "d-acct", manager())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Create(e.ctx, lead, "d-acct", Input{Name: "Asistente", ReportsTo: boss.ID, Skills: []string{"s-minutes"},
		Library: []Folder{{Path: "/Offices/Contabilidad", Level: LevelEdit}, {Path: "/Shared", Level: LevelEdit}}})
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != "exceeds_superior" {
		t.Fatalf("more than the manager: %v", err)
	}
	ex := pe.Details.(Excess)
	if !slices.Equal(ex.Skills, []string{"s-minutes"}) || len(ex.Folders) != 1 || ex.Folders[0].Path != "/Shared" {
		t.Fatalf("what exceeds: %+v", ex)
	}
	// no library under a manager with a library: the defaults would be more
	if _, err := e.svc.Create(e.ctx, lead, "d-acct", Input{Name: "Sin biblioteca", ReportsTo: boss.ID}); CodeOf(err) != "exceeds_superior" {
		t.Fatalf("no library under a narrower manager: %v", err)
	}
	asst, err := e.svc.Create(e.ctx, lead, "d-acct", Input{Name: "Asistente", ReportsTo: boss.ID, Skills: []string{"s-agenda", "s-bank"},
		Library: []Folder{{Path: "/Offices/Contabilidad", Level: LevelRead}, {Path: "/Shared", Level: LevelRead}}})
	if err != nil {
		t.Fatal(err)
	}
	// the manager loses s-bank and /Shared: the assistant loses them too, after a preview that changes nothing
	cut := manager()
	cut.Skills = []string{"s-exec", "s-agenda"}
	cut.Library = []Folder{{Path: "/Offices/Contabilidad", Level: LevelEdit}}
	_, imp, err := e.svc.Update(e.ctx, lead, "d-acct", boss.ID, cut, true)
	if err != nil || len(imp.Changed) != 1 || !slices.Equal(imp.Changed[0].SkillsRemoved, []string{"s-bank"}) || len(imp.Changed[0].Folders) != 1 {
		t.Fatalf("preview: %+v %v", imp, err)
	}
	if p, _ := e.svc.cfg.Store.GetPosition(e.ctx, org, asst.ID); !slices.Contains(p.Skills, "s-bank") {
		t.Fatal("a preview saves nothing")
	}
	if _, _, err := e.svc.Update(e.ctx, lead, "d-acct", boss.ID, cut, false); err != nil {
		t.Fatal(err)
	}
	p, _ := e.svc.cfg.Store.GetPosition(e.ctx, org, asst.ID)
	if slices.Contains(p.Skills, "s-bank") || LevelAt(p.Library, "/Shared/x") != LevelNone || LevelAt(p.Library, "/Offices/Contabilidad/x") != LevelRead {
		t.Fatalf("the assistant was cut: %+v", p)
	}
	if !Exceeds(cut.packageOf(), p.Package(), Bounds{Office: "/Offices/Contabilidad", Shared: "/Shared"}).Empty() {
		t.Fatal("the assistant still exceeds")
	}
	if !e.audit.has("position.cut") {
		t.Fatal("the cut is audited")
	}
	// a position cannot report to its own subordinate
	up := manager()
	up.ReportsTo = asst.ID
	if _, _, err := e.svc.Update(e.ctx, lead, "d-acct", boss.ID, up, false); CodeOf(err) != "cycle" {
		t.Fatalf("cycle: %v", err)
	}
}

func (in Input) packageOf() Package { return Package{Skills: in.Skills, Library: in.Library} }

func TestTemplatesProposeACutOfTheManager(t *testing.T) {
	e := newEnv(t)
	ts, err := e.svc.Templates(e.ctx, lead, "d-acct")
	if err != nil || len(ts) != 3 || ts[0].Key != TemplateManager {
		t.Fatalf("templates: %+v %v", ts, err)
	}
	if !slices.Contains(ts[0].Proposal.Skills, "s-exec") || LevelAt(ts[0].Proposal.Library, "/Offices/Contabilidad/x") != LevelEdit {
		t.Fatalf("manager proposal: %+v", ts[0])
	}
	boss, err := e.svc.Create(e.ctx, lead, "d-acct", Input{Name: "Gerente", Template: TemplateManager, Skills: []string{"s-agenda"},
		Library: []Folder{{Path: "/Offices/Contabilidad/Reportes", Level: LevelEdit}}})
	if err != nil {
		t.Fatal(err)
	}
	ts, _ = e.svc.Templates(e.ctx, lead, "d-acct")
	for _, x := range ts[1:] {
		if x.ReportsTo != boss.ID || !Exceeds(boss.Package(), x.Proposal, Bounds{Office: "/Offices/Contabilidad", Shared: "/Shared"}).Empty() {
			t.Fatalf("%s proposal must fit the manager: %+v", x.Key, x)
		}
		if _, err := e.svc.Create(e.ctx, lead, "d-acct", Input{Name: x.Key, ReportsTo: x.ReportsTo, Template: x.Key, Skills: x.Proposal.Skills, Library: x.Proposal.Library}); err != nil {
			t.Fatalf("one click creates %s: %v", x.Key, err)
		}
	}
}

func TestAssignMoveAndRuntime(t *testing.T) {
	e := newEnv(t)
	boss, _ := e.svc.Create(e.ctx, lead, "d-acct", manager())
	abogada, _ := e.svc.Create(e.ctx, legal, "d-legal", Input{Name: "Abogada"})
	if err := e.svc.Assign(e.ctx, mem, "valeria", boss.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("a plain member assigns: %v", err)
	}
	if err := e.svc.Assign(e.ctx, legal, "valeria", abogada.ID); CodeOf(err) != "position_other_office" {
		t.Fatalf("a position of another department: %v", err)
	}
	if err := e.svc.Assign(e.ctx, lead, "elena", boss.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an agent of a private department the person is not in: %v", err)
	}
	if err := e.svc.Assign(e.ctx, lead, "valeria", boss.ID); err != nil {
		t.Fatal(err)
	}
	name, skills, err := e.svc.PositionSkills(e.ctx, org, "valeria")
	if err != nil || name != boss.Name || !slices.Equal(skills, boss.Skills) {
		t.Fatalf("runtime skills: %s %v %v", name, skills, err)
	}
	lib, err := e.svc.AgentLibrary(e.ctx, org, "valeria")
	if err != nil || len(lib.Rules) != 2 || lib.PositionID != boss.ID {
		t.Fatalf("runtime library: %+v %v", lib, err)
	}
	v, err := e.svc.Agent(e.ctx, mem, "valeria")
	if err != nil || v.Position == nil || v.Position.ID != boss.ID || v.CanManage || len(v.Positions) != 1 {
		t.Fatalf("agent view: %+v %v", v, err)
	}
	// moving to another department takes the position away at once
	e.move("valeria", "d-legal")
	if lib, err := e.svc.AgentLibrary(e.ctx, org, "valeria"); err != nil || len(lib.Rules) != 0 {
		t.Fatalf("after the move: %+v %v", lib, err)
	}
	if _, err := e.svc.cfg.Store.AssignmentOf(e.ctx, org, "valeria"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("the assignment is removed")
	}
	if !e.audit.has("position.unassigned") {
		t.Fatal("the removal is audited")
	}
	// deleting a position leaves its holders without one and its subordinates under its superior
	e.move("valeria", "d-acct")
	asst, _ := e.svc.Create(e.ctx, lead, "d-acct", Input{Name: "Asistente", ReportsTo: boss.ID})
	mid, _ := e.svc.Create(e.ctx, lead, "d-acct", Input{Name: "Medio", ReportsTo: boss.ID, Library: manager().Library})
	_ = asst
	low, err := e.svc.Create(e.ctx, lead, "d-acct", Input{Name: "Bajo", ReportsTo: mid.ID, Library: []Folder{{Path: "/Shared", Level: LevelRead}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Assign(e.ctx, lead, "pedro", mid.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(e.ctx, lead, "d-acct", mid.ID); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.cfg.Store.GetPosition(e.ctx, org, low.ID); p.ReportsTo != boss.ID {
		t.Fatalf("subordinate re-parented: %+v", p)
	}
	if _, ok, _ := e.svc.PositionOf(e.ctx, org, "pedro"); ok {
		t.Fatal("the holder of a deleted position holds nothing")
	}
}

func TestOrganizationsAreIsolated(t *testing.T) {
	e := newEnv(t)
	boss, _ := e.svc.Create(e.ctx, lead, "d-acct", manager())
	other := application.WithOrg(context.Background(), "o2")
	if _, err := e.svc.cfg.Store.GetPosition(other, "o2", boss.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("another organization reads the position")
	}
	if err := e.svc.cfg.Store.SetAssignment(other, "o2", Assignment{AgentID: "valeria", PositionID: boss.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("another organization assigns the position")
	}
}
