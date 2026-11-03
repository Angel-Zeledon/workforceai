package drive_test

import (
	"context"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
)

type fakeCustomers struct{}

func (fakeCustomers) ProjectCustomer(context.Context, string, string) (string, error) { return "", nil }
func (fakeCustomers) LinkedFolders(context.Context, string) (map[string]string, error) {
	return map[string]string{}, nil
}

func setAccess(t *testing.T, e *agentEnv, agent, path, level string) {
	t.Helper()
	if _, err := e.svc.SetAgentAccess(e.ctx, drive.Human("u"), agent, path, level); err != nil {
		t.Fatalf("set %s %s %s: %v", agent, path, level, err)
	}
}

func TestInScopeHonoursExclusions(t *testing.T) {
	under := []string{"/Shared", "!/Shared/RRHH", "/Agents/sales"}
	for p, want := range map[string]bool{
		"/Shared/a.md": true, "/Shared/RRHH": false, "/Shared/RRHH/n.pdf": false, "/Shared/RRHHx/a": true,
		"/Agents/sales/x": true, "/Agents/legal/x": false,
	} {
		if got := drive.InScope(under, p); got != want {
			t.Fatalf("%s: %v", p, got)
		}
	}
	if !drive.InScope(nil, "/x") || drive.InScope([]string{}, "/x") || drive.InScope([]string{"!/x"}, "/y") {
		t.Fatal("nil = everything, empty = nothing, only exclusions = nothing")
	}
}

func TestAccessNoneHidesSubtreeEverywhere(t *testing.T) {
	e := newAgentEnv(t)
	h := drive.Human("u")
	if _, err := e.svc.Write(e.ctx, h, drive.WriteInput{Path: "/Shared/RRHH/nomina.md", Data: []byte("nomina secreta politica")}); err != nil {
		t.Fatal(err)
	}
	setAccess(t, e, "sales", "/Shared/RRHH", drive.AccessNone)
	if pl := e.plan("sales", "read", map[string]any{"path": "/Shared/RRHH/nomina.md"}); pl.Deny != "not_granted" {
		t.Fatalf("none must refuse reading: %+v", pl)
	}
	if pl := e.plan("sales", "read", map[string]any{"path": "/Shared/politica.md"}); pl.Deny != "" {
		t.Fatalf("the rest of /Shared stays readable: %+v", pl)
	}
	if pl := e.plan("sales", "list", map[string]any{"path": "/Shared/RRHH"}); pl.Deny != "not_granted" {
		t.Fatalf("list: %+v", pl)
	}
	roots, err := e.fs.ReadableRoots(e.ctx, "sales")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(roots, "!/Shared/RRHH") || !contains(roots, "/Shared") {
		t.Fatalf("roots must carry the exclusion: %v", roots)
	}
	r := e.fs.Run(e.ctx, "sales", "search", map[string]any{"query": "politica"}, false)
	if !r.OK || strings.Contains(r.Text, "nomina") || !strings.Contains(r.Text, "politica.md") {
		t.Fatalf("search leaked or missed: %+v", r)
	}
	l := e.fs.Run(e.ctx, "sales", "list", map[string]any{"path": "/Shared"}, false)
	if strings.Contains(l.Text, "RRHH") {
		t.Fatalf("list names a folder the agent cannot see: %s", l.Text)
	}
	// none on the root of a default zone removes the zone
	setAccess(t, e, "sales", "/Shared", drive.AccessNone)
	if pl := e.plan("sales", "read", map[string]any{"path": "/Shared/politica.md"}); pl.Deny != "not_granted" {
		t.Fatalf("none on /Shared: %+v", pl)
	}
	roots, _ = e.fs.ReadableRoots(e.ctx, "sales")
	if contains(roots, "/Shared") {
		t.Fatalf("roots: %v", roots)
	}
	// back to default
	setAccess(t, e, "sales", "/Shared", drive.AccessDefault)
	if pl := e.plan("sales", "read", map[string]any{"path": "/Shared/politica.md"}); pl.Deny != "" {
		t.Fatalf("default restored: %+v", pl)
	}
}

func TestAccessEditWritesFreely(t *testing.T) {
	e := newAgentEnv(t)
	setAccess(t, e, "sales", "/Clients/Acme", drive.AccessEdit)
	if pl := e.plan("sales", "read", map[string]any{"path": "/Clients/Acme/contrato.md"}); pl.Deny != "" {
		t.Fatalf("edit implies read: %+v", pl)
	}
	if pl := e.plan("sales", "write", map[string]any{"path": "/Clients/Acme/sub/nuevo.md", "content": "x"}); pl.Deny != "" || pl.Approval {
		t.Fatalf("edit inherits down and writes freely: %+v", pl)
	}
	setAccess(t, e, "sales", "/Clients/Acme", drive.AccessRead)
	if pl := e.plan("sales", "write", map[string]any{"path": "/Clients/Acme/nuevo.md", "content": "x"}); !pl.Approval {
		t.Fatalf("read asks for approval to write: %+v", pl)
	}
	// a more specific rule wins: read on the project folder of an assigned project
	setAccess(t, e, "sales", "/Projects/Plan/Final", drive.AccessRead)
	if pl := e.plan("sales", "write", map[string]any{"path": "/Projects/Plan/Final/x.md", "content": "x"}); !pl.Approval {
		t.Fatalf("narrower read wins over the project default: %+v", pl)
	}
	if pl := e.plan("sales", "write", map[string]any{"path": "/Projects/Plan/x.md", "content": "x"}); pl.Approval {
		t.Fatalf("the rest of the project stays editable: %+v", pl)
	}
	lv, src, err := e.fs.AccessAt(e.ctx, "sales", "/Projects/Plan/Final/a/b")
	if err != nil || lv != drive.AccessRead || src != drive.SourceManual {
		t.Fatalf("access at: %s %s %v", lv, src, err)
	}
	lv, src, _ = e.fs.AccessAt(e.ctx, "sales", "/Shared/x")
	if lv != drive.AccessRead || src != drive.SourceDefault {
		t.Fatalf("shared default: %s %s", lv, src)
	}
	lv, _, _ = e.fs.AccessAt(e.ctx, "legal", "/Clients/Acme")
	if lv != drive.AccessNone {
		t.Fatalf("other agent: %s", lv)
	}
}

func TestAccessRulesAndAudit(t *testing.T) {
	e := newAgentEnv(t)
	if _, err := e.svc.SetAgentAccess(e.ctx, drive.Human("u"), "sales", "/Agents/sales/x", drive.AccessNone); err == nil {
		t.Fatal("the agent's own desk is always Edit")
	}
	if _, err := e.svc.SetAgentAccess(e.ctx, drive.AgentP("sales"), "sales", "/Clients", drive.AccessEdit); err == nil {
		t.Fatal("agents never change access")
	}
	if _, err := e.svc.SetAgentAccess(e.ctx, drive.Human("u"), "sales", "/Clients", "owner"); err == nil {
		t.Fatal("unknown level")
	}
	setAccess(t, e, "sales", "/Clients/Acme", drive.AccessRead)
	prev, err := e.svc.SetAgentAccess(e.ctx, drive.Human("u"), "sales", "/Clients/Acme", drive.AccessEdit)
	if err != nil || prev != drive.AccessRead {
		t.Fatalf("replace: %q %v", prev, err)
	}
	gs, _ := e.svc.Grants(e.ctx, "sales")
	if len(gs) != 1 || gs[0].Mode != drive.AccessEdit {
		t.Fatalf("one active rule per path: %+v", gs)
	}
	if !e.al.has("drive.access.changed", drive.Human("u")) {
		t.Fatal("audited")
	}
	es, err := e.fs.AgentAccess(e.ctx, "sales")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range es {
		got = append(got, x.Path+"="+x.Level+":"+x.Source)
	}
	want := []string{"/Agents/sales=edit:default", "/Shared=read:default", "/Projects/Plan=edit:default", "/Clients/Acme=edit:manual"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entries: %v", got)
	}
}

func TestAccessDoesNotBreakCustomerIsolation(t *testing.T) {
	e := newAgentEnv(t)
	e.fs.Customers = fakeCustomers{}
	setAccess(t, e, "sales", "/Clients/Acme", drive.AccessEdit)
	ctx := application.WithCustomer(e.ctx, "cus_other")
	if pl := e.fs.Plan(ctx, "sales", "read", map[string]any{"path": "/Clients/Acme/contrato.md"}); pl.Deny == "" {
		t.Fatalf("a grant never opens another customer's folder: %+v", pl)
	}
	setAccess(t, e, "sales", "/Shared", drive.AccessNone)
	if pl := e.fs.Plan(ctx, "sales", "read", map[string]any{"path": "/Shared/politica.md"}); pl.Deny == "" {
		t.Fatalf("none also holds in a customer task: %+v", pl)
	}
}
