package drive_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
)

type fakeProjects map[string][]drive.ProjectRef

func (f fakeProjects) AgentProjects(_ context.Context, _, agentID string) ([]drive.ProjectRef, error) {
	return f[agentID], nil
}

type agentEnv struct {
	svc  *drive.Service
	fs   *drive.AgentFS
	al   *auditLog
	ctx  context.Context
	proj drive.Folder
	oth  drive.Folder
}

func newAgentEnv(t *testing.T) *agentEnv {
	t.Helper()
	svc, al, _ := newSvc(drive.Limits{})
	ctx := application.WithOrg(context.Background(), "o1")
	e := &agentEnv{svc: svc, al: al, ctx: ctx,
		fs: &drive.AgentFS{Svc: svc, Projects: fakeProjects{"sales": {{ID: "prj_1", Name: "Plan"}}}}}
	var err error
	if e.proj, err = svc.EnsureOwnedFolder(ctx, drive.RootProjects, "prj_1", "Plan"); err != nil {
		t.Fatal(err)
	}
	if e.oth, err = svc.EnsureOwnedFolder(ctx, drive.RootProjects, "prj_2", "Otro"); err != nil {
		t.Fatal(err)
	}
	h := drive.Human("u")
	for p, c := range map[string]string{
		"/Agents/sales/mio.md": "mi escritorio", "/Agents/legal/ajeno.md": "escritorio de legal",
		"/Projects/Plan/plan.md": "plan del proyecto", "/Projects/Otro/otro.md": "otro proyecto",
		"/Shared/politica.md": "politica compartida", "/Clients/Acme/contrato.md": "contrato secreto de Acme",
	} {
		if _, err := svc.Write(ctx, h, drive.WriteInput{Path: p, Data: []byte(c)}); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *agentEnv) plan(agent, action string, args map[string]any) drive.Plan {
	return e.fs.Plan(e.ctx, agent, action, args)
}

func TestAgentReadScope(t *testing.T) {
	e := newAgentEnv(t)
	for _, p := range []string{"/Agents/sales/mio.md", "/Projects/Plan/plan.md", "/Shared/politica.md"} {
		if pl := e.plan("sales", "read", map[string]any{"path": p}); pl.Deny != "" || pl.Approval {
			t.Fatalf("read %s: %+v", p, pl)
		}
	}
	for _, p := range []string{"/Agents/legal/ajeno.md", "/Projects/Otro/otro.md", "/Clients/Acme/contrato.md"} {
		if pl := e.plan("sales", "read", map[string]any{"path": p}); pl.Deny != "not_granted" {
			t.Fatalf("read %s must be refused: %+v", p, pl)
		}
	}
	// a human grant opens reading (only reading)
	if _, err := e.svc.AddGrant(e.ctx, drive.Human("u"), "sales", "/Clients/Acme"); err != nil {
		t.Fatal(err)
	}
	if pl := e.plan("sales", "read", map[string]any{"path": "/Clients/Acme/contrato.md"}); pl.Deny != "" {
		t.Fatalf("granted read: %+v", pl)
	}
	if pl := e.plan("sales", "read", map[string]any{"path": "/Clients/Other/x.md"}); pl.Deny == "" {
		// not an existing file: not_found, never leaked as readable
		t.Fatalf("ungranted: %+v", pl)
	}
	w := e.plan("sales", "write", map[string]any{"path": "/Clients/Acme/nuevo.md", "content": "x"})
	if !w.Approval || w.Deny != "" {
		t.Fatalf("a read grant must not allow free writes: %+v", w)
	}
	// the other agent has no such grant
	if pl := e.plan("legal", "read", map[string]any{"path": "/Clients/Acme/contrato.md"}); pl.Deny != "not_granted" {
		t.Fatalf("grants are per agent: %+v", pl)
	}
}

func TestAgentWriteRules(t *testing.T) {
	e := newAgentEnv(t)
	free := []string{"/Agents/sales/nuevo.md", "/Agents/sales/sub/dir/notas.txt", "/Projects/Plan/informe.md"}
	for _, p := range free {
		pl := e.plan("sales", "write", map[string]any{"path": p, "content": "hola"})
		if pl.Deny != "" || pl.Approval {
			t.Fatalf("write %s should be free: %+v", p, pl)
		}
	}
	ask := []string{"/Shared/nuevo.md", "/Agents/legal/x.md", "/Projects/Otro/x.md", "/Clients/Acme/x.md", "/Projects/NoExiste/x.md"}
	for _, p := range ask {
		pl := e.plan("sales", "write", map[string]any{"path": p, "content": "hola"})
		if pl.Deny != "" || !pl.Approval || pl.Risk != "medium" || !contains(pl.Flags, "outside_scope") {
			t.Fatalf("write %s must ask: %+v", p, pl)
		}
	}
	// an agent that is NOT assigned to the project must ask there
	if pl := e.plan("legal", "write", map[string]any{"path": "/Projects/Plan/x.md", "content": "x"}); !pl.Approval {
		t.Fatalf("unassigned agent: %+v", pl)
	}
	// hard refusals
	for name, args := range map[string]map[string]any{
		"traversal":  {"path": "/Agents/sales/../legal/x.md", "content": "x"},
		"relative":   {"path": "Agents/sales/x.md", "content": "x"},
		"binary":     {"path": "/Agents/sales/x.pdf", "content": "%PDF-1"},
		"html":       {"path": "/Agents/sales/x.html", "content": "<b>"},
		"no content": {"path": "/Agents/sales/x.md"},
		"nul":        {"path": "/Agents/sales/x.md", "content": "a\x00b"},
		"root file":  {"path": "/x.md", "content": "x"},
		"bad tags":   {"path": "/Agents/sales/x.md", "content": "x", "tags": []any{strings.Repeat("t", 80)}},
	} {
		if pl := e.plan("sales", "write", args); pl.Deny == "" {
			t.Fatalf("%s must be refused: %+v", name, pl)
		}
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestAgentDeleteAlwaysNeedsApproval(t *testing.T) {
	e := newAgentEnv(t)
	for _, p := range []string{"/Agents/sales/mio.md", "/Projects/Plan/plan.md", "/Shared/politica.md"} {
		pl := e.plan("sales", "delete", map[string]any{"path": p})
		if !pl.Approval || pl.Risk != "high" || pl.Reversibility != "trash" {
			t.Fatalf("delete %s: %+v", p, pl)
		}
	}
	// not even visible: refused (no approval card for something the agent cannot see)
	if pl := e.plan("sales", "delete", map[string]any{"path": "/Agents/legal/ajeno.md"}); pl.Deny != "not_granted" {
		t.Fatalf("%+v", pl)
	}
	args := map[string]any{"path": "/Agents/sales/mio.md"}
	if r := e.fs.Run(e.ctx, "sales", "delete", args, false); r.OK || r.Code != "approval_required" {
		t.Fatalf("unapproved delete ran: %+v", r)
	}
	if _, err := e.svc.FileByPath(e.ctx, "/Agents/sales/mio.md"); err != nil {
		t.Fatal("file vanished without approval")
	}
	if r := e.fs.Run(e.ctx, "sales", "delete", args, true); !r.OK {
		t.Fatalf("approved delete: %+v", r)
	}
	if _, err := e.svc.FileByPath(e.ctx, "/Agents/sales/mio.md"); err == nil {
		t.Fatal("approved delete did not delete")
	}
	if !e.al.has("drive.file.deleted", drive.AgentP("sales")) {
		t.Fatal("delete must be audited as the agent")
	}
	// reversible: it is in the trash and can be restored by a person
	tr, _ := e.svc.Trash(e.ctx)
	if len(tr.Files) != 1 {
		t.Fatalf("trash: %+v", tr)
	}
	if _, err := e.svc.RestoreFile(e.ctx, drive.Human("u"), tr.Files[0].ID); err != nil {
		t.Fatal(err)
	}
	// an approval never lifts a refusal
	if r := e.fs.Run(e.ctx, "sales", "delete", map[string]any{"path": "/Agents/legal/ajeno.md"}, true); r.OK {
		t.Fatalf("approved call on an unreadable file ran: %+v", r)
	}
}

func TestAgentMoveRules(t *testing.T) {
	e := newAgentEnv(t)
	in := e.plan("sales", "move", map[string]any{"path": "/Agents/sales/mio.md", "to": "/Projects/Plan/mio.md"})
	if in.Approval || in.Deny != "" {
		t.Fatalf("desk -> own project is free: %+v", in)
	}
	out := e.plan("sales", "move", map[string]any{"path": "/Agents/sales/mio.md", "to": "/Shared/mio.md"})
	if !out.Approval || !contains(out.Flags, "moves_out_of_scope") {
		t.Fatalf("moving out must ask: %+v", out)
	}
	from := e.plan("sales", "move", map[string]any{"path": "/Shared/politica.md", "to": "/Agents/sales/politica.md"})
	if !from.Approval {
		t.Fatalf("moving a shared file away must ask: %+v", from)
	}
	if pl := e.plan("sales", "move", map[string]any{"path": "/Agents/legal/ajeno.md", "to": "/Agents/sales/a.md"}); pl.Deny != "not_granted" {
		t.Fatalf("stealing from another desk: %+v", pl)
	}
	if pl := e.plan("sales", "move", map[string]any{"path": "/Agents/sales/mio.md", "to": "/Agents/sales/mio.pdf"}); pl.Deny != "extension_change" {
		t.Fatalf("%+v", pl)
	}
	if r := e.fs.Run(e.ctx, "sales", "move", map[string]any{"path": "/Agents/sales/mio.md", "to": "/Agents/sales/archivo/mio.md"}, false); !r.OK {
		t.Fatalf("free move: %+v", r)
	}
	if r := e.fs.Run(e.ctx, "sales", "move", map[string]any{"path": "/Agents/sales/archivo/mio.md", "to": "/Shared/mio.md"}, false); r.OK || r.Code != "approval_required" {
		t.Fatalf("unapproved move out: %+v", r)
	}
}

func TestAgentListAndSearchRespectScope(t *testing.T) {
	e := newAgentEnv(t)
	r := e.fs.Run(e.ctx, "sales", "list", map[string]any{"path": "/"}, false)
	if !r.OK || !strings.Contains(r.Text, "/Agents/sales") || !strings.Contains(r.Text, "/Projects/Plan") || !strings.Contains(r.Text, "/Shared") {
		t.Fatalf("root list: %+v", r)
	}
	for _, hidden := range []string{"legal", "Otro", "Clients", "Acme"} {
		if strings.Contains(r.Text, hidden) {
			t.Fatalf("root list leaks %q: %s", hidden, r.Text)
		}
	}
	if r = e.fs.Run(e.ctx, "sales", "list", map[string]any{"path": "/Agents"}, false); strings.Contains(r.Text, "legal") {
		t.Fatalf("/Agents leaks other desks: %s", r.Text)
	}
	if r = e.fs.Run(e.ctx, "sales", "list", map[string]any{"path": "/Projects"}, false); strings.Contains(r.Text, "Otro") || !strings.Contains(r.Text, "Plan") {
		t.Fatalf("/Projects: %s", r.Text)
	}
	if r = e.fs.Run(e.ctx, "sales", "list", map[string]any{"path": "/Agents/legal"}, false); r.OK || r.Code != "not_granted" {
		t.Fatalf("list another desk: %+v", r)
	}
	if r = e.fs.Run(e.ctx, "sales", "list", map[string]any{"path": "/Shared"}, false); !r.OK || !strings.Contains(r.Text, "politica.md") {
		t.Fatalf("list shared: %+v", r)
	}
	// search sees only what the agent can read
	for q, want := range map[string]bool{"escritorio": true, "proyecto": true, "politica": true, "secreto": false, "legal": false, "otro": false} {
		r := e.fs.Run(e.ctx, "sales", "search", map[string]any{"query": q}, false)
		got := strings.Contains(r.Text, "/")
		if r.Code != "" || got != want && !(q == "escritorio" || q == "proyecto") {
			t.Fatalf("search %q: %+v", q, r)
		}
		if strings.Contains(r.Text, "/Clients/") || strings.Contains(r.Text, "/Agents/legal") || strings.Contains(r.Text, "/Projects/Otro") {
			t.Fatalf("search %q leaked: %s", q, r.Text)
		}
	}
	if r := e.fs.Run(e.ctx, "sales", "search", map[string]any{"query": "secreto", "path": "/Clients"}, false); r.OK || r.Code != "not_granted" {
		t.Fatalf("search in an unreadable folder: %+v", r)
	}
}

func TestAgentReadIsBoundedAndPaged(t *testing.T) {
	e := newAgentEnv(t)
	big := strings.Repeat("linea de prueba número uno\n", 3000) // ~81k chars
	if _, err := e.svc.Write(e.ctx, drive.Human("u"), drive.WriteInput{Path: "/Shared/grande.md", Data: []byte(big)}); err != nil {
		t.Fatal(err)
	}
	r := e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": "/Shared/grande.md"}, false)
	if !r.OK || len(r.Text) > 9500 || !strings.Contains(r.Text, "[truncated:") || !strings.Contains(r.Text, "offset=8000") {
		t.Fatalf("default window: ok=%v len=%d tail=%q", r.OK, len(r.Text), tail(r.Text))
	}
	r2 := e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": "/Shared/grande.md", "offset": 8000.0, "max_chars": 100000.0}, false)
	// the cap counts characters, not bytes ("número" is 7 bytes, 6 characters)
	if n := utf8.RuneCountInString(r2.Text); !r2.OK || n > drive.MaxReadChars+600 {
		t.Fatalf("hard cap: %d characters", n)
	}
	last := e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": "/Shared/grande.md", "offset": 80000.0}, false)
	if strings.Contains(last.Text, "[truncated") {
		t.Fatalf("last window must not claim more: %q", tail(last.Text))
	}
	// a file the agent cannot read says nothing about its content
	if r := e.fs.Run(e.ctx, "sales", "read", map[string]any{"path": "/Clients/Acme/contrato.md"}, false); r.OK || strings.Contains(r.Summary+r.Text, "secreto") {
		t.Fatalf("%+v", r)
	}
}

func tail(s string) string {
	if len(s) > 120 {
		return s[len(s)-120:]
	}
	return s
}

func TestAgentWritesAreAttributedVersionedAndLocked(t *testing.T) {
	e := newAgentEnv(t)
	args := map[string]any{"path": "/Agents/sales/informe.md", "content": "v1", "message": "primer informe"}
	r := e.fs.Run(e.ctx, "sales", "write", args, false)
	if !r.OK || r.Data["version"] != 1 {
		t.Fatalf("%+v", r)
	}
	f, _ := e.svc.FileByPath(e.ctx, "/Agents/sales/informe.md")
	if f.CreatedBy != drive.AgentP("sales") || f.UpdatedBy != drive.AgentP("sales") {
		t.Fatalf("not attributed to the agent: %+v", f)
	}
	if !e.al.has("drive.file.created", drive.AgentP("sales")) {
		t.Fatal("agent write not audited")
	}
	// the human edits meanwhile; the agent's stale write is refused with the current version
	if _, err := e.svc.NewVersion(e.ctx, drive.Human("u"), f.ID, []byte("editado por la persona"), "", nil, "ui"); err != nil {
		t.Fatal(err)
	}
	stale := e.fs.Run(e.ctx, "sales", "write", map[string]any{"path": "/Agents/sales/informe.md", "content": "v-agent", "expected_version": 1.0}, false)
	if stale.OK || stale.Code != "version_conflict" || stale.Data["current_version"] != 2 {
		t.Fatalf("stale write: %+v", stale)
	}
	ok := e.fs.Run(e.ctx, "sales", "write", map[string]any{"path": "/Agents/sales/informe.md", "content": "v-agent", "expected_version": 2.0}, false)
	if !ok.OK || ok.Data["version"] != 3 {
		t.Fatalf("%+v", ok)
	}
	// every state stays reachable
	vs, _ := e.svc.Versions(e.ctx, f.ID)
	if len(vs) != 3 || vs[1].Author != drive.Human("u") {
		t.Fatalf("history: %+v", vs)
	}
	if bad := e.fs.Run(e.ctx, "sales", "write", map[string]any{"path": "/Agents/sales/informe.md", "content": "x", "expected_version": "abc"}, false); bad.OK || bad.Code != "invalid_args" {
		t.Fatalf("%+v", bad)
	}
}

func TestAgentCannotWriteWhenReadOnlyMode(t *testing.T) {
	ro := true
	svc := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: "o1",
		WriteGate: func(context.Context, string) error {
			if ro {
				return drive.ErrReadOnly
			}
			return nil
		}})
	fs := &drive.AgentFS{Svc: svc}
	ctx := application.WithOrg(context.Background(), "o1")
	r := fs.Run(ctx, "sales", "write", map[string]any{"path": "/Agents/sales/x.md", "content": "x"}, false)
	if r.OK || r.Code != "read_only_mode" {
		t.Fatalf("%+v", r)
	}
	ro = false
	if r := fs.Run(ctx, "sales", "write", map[string]any{"path": "/Agents/sales/x.md", "content": "x"}, false); !r.OK {
		t.Fatalf("%+v", r)
	}
	ro = true
	if r := fs.Run(ctx, "sales", "read", map[string]any{"path": "/Agents/sales/x.md"}, false); !r.OK {
		t.Fatalf("reads must keep working in read-only mode: %+v", r)
	}
}

func TestAgentFolderAccessFailsClosed(t *testing.T) {
	svc, _, _ := newSvc(drive.Limits{})
	fs := &drive.AgentFS{Svc: svc, Projects: failingProjects{}}
	ctx := application.WithOrg(context.Background(), "o1")
	if pl := fs.Plan(ctx, "sales", "read", map[string]any{"path": "/Shared/x.md"}); pl.Deny != "drive_unavailable" {
		t.Fatalf("%+v", pl)
	}
}

type failingProjects struct{}

func (failingProjects) AgentProjects(context.Context, string, string) ([]drive.ProjectRef, error) {
	return nil, errors.New("down")
}
