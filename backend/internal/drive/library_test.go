package drive_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"aiworkforce/backend/internal/drive"
)

// fakeLibraries maps an agent to the library of its position.
type fakeLibraries map[string][]drive.LibraryRule

func (f fakeLibraries) AgentLibrary(_ context.Context, _, agentID string) (drive.Library, error) {
	if r, ok := f[agentID]; ok {
		return drive.Library{PositionID: "pos_1", PositionName: "Asistente de gerente", Rules: r}, nil
	}
	return drive.Library{}, nil
}

type failingLibraries struct{}

func (failingLibraries) AgentLibrary(context.Context, string, string) (drive.Library, error) {
	return drive.Library{}, errors.New("positions down")
}

// newLibraryEnv: the agent "sales" works in Contabilidad (and on the project Plan); the office has Clientes (a
// balance) and Reportes (a report).
func newLibraryEnv(t *testing.T) *agentEnv {
	t.Helper()
	e := newOfficeEnv(t)
	e.fs.Offices = fakeAgentOffices{"sales": "dep_acct", "acct": "dep_acct", "legal": "dep_legal"}
	h := drive.Human("u")
	for p, c := range map[string]string{
		"/Offices/Contabilidad/Reportes/mensual.md": "reporte mensual confidencial",
		"/Shared/Plantillas/factura.md":             "plantilla de factura confidencial",
	} {
		if _, err := e.svc.Write(e.ctx, h, drive.WriteInput{Path: p, Data: []byte(c)}); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *agentEnv) readable(t *testing.T, agent, path string) bool {
	t.Helper()
	pl := e.plan(agent, "read", map[string]any{"path": path})
	return pl.Deny == ""
}

func TestNoLibraryIsTodaysBehaviour(t *testing.T) {
	e := newLibraryEnv(t)
	before, err := e.fs.ReadableRoots(e.ctx, "sales")
	if err != nil {
		t.Fatal(err)
	}
	e.svc.SetAgentLibraries(fakeLibraries{"acct": {{Path: "/Shared", Level: drive.AccessNone}}}) // someone else's position
	after, err := e.fs.ReadableRoots(e.ctx, "sales")
	if err != nil || !slices.Equal(before, after) {
		t.Fatalf("an agent without a library changed: %v -> %v (%v)", before, after, err)
	}
	for _, p := range []string{"/Offices/Contabilidad/Clientes/balance.md", "/Shared/politica.md", "/Agents/sales/mio.md", "/Projects/Plan/plan.md"} {
		if !e.readable(t, "sales", p) {
			t.Fatalf("without a library %s is readable", p)
		}
	}
}

func TestLibraryNarrowsTheDepartmentAndKeepsTheBase(t *testing.T) {
	e := newLibraryEnv(t)
	e.svc.SetAgentLibraries(fakeLibraries{"sales": {
		{Path: "/Offices/Contabilidad/Reportes", Level: drive.AccessRead},
		{Path: "/Shared/Plantillas", Level: drive.AccessRead},
	}})
	// the library and the base (desk, projects)
	for _, p := range []string{"/Offices/Contabilidad/Reportes/mensual.md", "/Shared/Plantillas/factura.md", "/Agents/sales/mio.md", "/Projects/Plan/plan.md"} {
		if !e.readable(t, "sales", p) {
			t.Fatalf("%s must stay readable", p)
		}
	}
	// the rest of the department and of /Shared: out, answered like a missing file
	for _, p := range []string{"/Offices/Contabilidad/Clientes/balance.md", "/Shared/politica.md"} {
		pl := e.plan("sales", "read", map[string]any{"path": p})
		if pl.Deny != "not_found" || !strings.Contains(pl.DenyMsg, "no such file") {
			t.Fatalf("%s outside the library: %+v", p, pl)
		}
	}
	// Run says not_found to the agent and leaves the real reason in the audit
	r := e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": "/Offices/Contabilidad/Clientes/balance.md"}, false)
	if r.OK || r.Code != "not_found" || strings.Contains(r.Summary, "position") || strings.Contains(r.Summary, "balance") {
		t.Fatalf("the agent learns nothing: %+v", r)
	}
	if !e.al.has("drive.read_outside_position", drive.AgentP("sales")) {
		t.Fatal("the reason is audited")
	}
	// another office is never opened by a library entry pointing at it
	e.svc.SetAgentLibraries(fakeLibraries{"sales": {{Path: "/Offices/Legal/Casos", Level: drive.AccessEdit}, {Path: "/Offices/Contabilidad/Reportes", Level: drive.AccessRead}}})
	if e.readable(t, "sales", "/Offices/Legal/Casos/demanda.md") {
		t.Fatal("a library never opens another department")
	}
}

func TestLibrarySearchAndRoots(t *testing.T) {
	e := newLibraryEnv(t)
	e.svc.SetAgentLibraries(fakeLibraries{"sales": {
		{Path: "/Offices/Contabilidad", Level: drive.AccessEdit},
		{Path: "/Offices/Contabilidad/Clientes", Level: drive.AccessNone},
	}})
	roots, err := e.fs.ReadableRoots(e.ctx, "sales")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(roots, "!/Offices/Contabilidad/Clientes") || slices.Contains(roots, "/Shared") || !slices.Contains(roots, "/Agents/sales") || !slices.Contains(roots, "/Projects/Plan") {
		t.Fatalf("roots: %v", roots)
	}
	r := e.fs.Run(e.ctx, "sales", "search", map[string]any{"query": "confidencial"}, false)
	if !r.OK || strings.Contains(r.Text, "balance") || strings.Contains(r.Text, "politica") || !strings.Contains(r.Text, "mensual") {
		t.Fatalf("search sees only the library: %+v", r)
	}
	// listing the office names only what the library leaves
	l := e.fs.Run(e.ctx, "sales", "list", map[string]any{"path": "/Offices/Contabilidad"}, false)
	if !l.OK || strings.Contains(l.Text, "Clientes") || !strings.Contains(l.Text, "Reportes") {
		t.Fatalf("list: %+v", l)
	}
	// a search scoped to an excluded folder answers like a missing one
	if pl := e.plan("sales", "search", map[string]any{"query": "x", "path": "/Offices/Contabilidad/Clientes"}); pl.Deny != "not_found" {
		t.Fatalf("scoped search: %+v", pl)
	}
}

func TestLibraryLevelsAndHandRules(t *testing.T) {
	e := newLibraryEnv(t)
	e.svc.SetAgentLibraries(fakeLibraries{"sales": {{Path: "/Offices/Contabilidad/Reportes", Level: drive.AccessRead}, {Path: "/Shared/Plantillas", Level: drive.AccessEdit}}})
	// Read in its office: writing there asks a person; Edit in /Shared: free
	if pl := e.plan("sales", "write", map[string]any{"path": "/Offices/Contabilidad/Reportes/n.md", "content": "x"}); !pl.Approval {
		t.Fatalf("read level writes ask: %+v", pl)
	}
	if pl := e.plan("sales", "write", map[string]any{"path": "/Shared/Plantillas/n.md", "content": "x"}); pl.Deny != "" || pl.Approval {
		t.Fatalf("edit level writes freely: %+v", pl)
	}
	if pl := e.plan("sales", "write", map[string]any{"path": "/Offices/Contabilidad/Clientes/n.md", "content": "x"}); pl.Deny == "" && !pl.Approval {
		t.Fatalf("outside the library a write is never free: %+v", pl)
	}
	// a rule a person set by hand, deeper than the library entry it meets, still decides
	if _, err := e.svc.SetAgentAccess(e.ctx, drive.Human("u"), "sales", "/Offices/Contabilidad/Clientes", drive.AccessRead); err != nil {
		t.Fatal(err)
	}
	if !e.readable(t, "sales", "/Offices/Contabilidad/Clientes/balance.md") {
		t.Fatal("a hand rule opens what the library left out")
	}
	es, err := e.fs.AgentAccess(e.ctx, "sales")
	if err != nil {
		t.Fatal(err)
	}
	src := map[string]string{}
	for _, x := range es {
		src[x.Path] = x.Source + ":" + x.Level
	}
	if src["/Offices/Contabilidad/Reportes"] != "position:read" || src["/Shared/Plantillas"] != "position:edit" || src["/Offices/Contabilidad/Clientes"] != "manual:read" ||
		src["/Shared"] != "" || src["/Offices/Contabilidad"] != "" || src["/Agents/sales"] != "default:edit" {
		t.Fatalf("access lines: %v", src)
	}
	if lvl, s, _ := e.fs.AccessAt(e.ctx, "sales", "/Shared/politica.md"); lvl != drive.AccessNone || s != drive.SourcePosition {
		t.Fatalf("AccessAt outside the library = %s %s", lvl, s)
	}
}

func TestLibraryNeverBeatsThePersonLimit(t *testing.T) {
	e := newLibraryEnv(t)
	e.svc.SetAgentLibraries(fakeLibraries{"sales": {{Path: "/Offices/Contabilidad", Level: drive.AccessEdit}}})
	e.svc.SetPeople(officePeopleByID{"dep_acct": drive.AccessNone})
	beto := drive.WithRequester(e.ctx, "beto")
	if pl := e.fs.Plan(beto, "sales", "read", map[string]any{"path": "/Offices/Contabilidad/Reportes/mensual.md"}); pl.Deny != "person_no_access" {
		t.Fatalf("the person's limit still applies: %+v", pl)
	}
}

func TestLibraryFailsClosed(t *testing.T) {
	e := newLibraryEnv(t)
	e.svc.SetAgentLibraries(failingLibraries{})
	if e.readable(t, "sales", "/Shared/politica.md") {
		t.Fatal("an unknown library means nothing readable")
	}
	if _, err := e.fs.ReadableRoots(e.ctx, "sales"); err == nil {
		t.Fatal("roots of an unknown library")
	}
}
