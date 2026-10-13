package drive_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
)

func editArgs(path string, v int, ops ...map[string]any) map[string]any {
	l := make([]any, len(ops))
	for i, o := range ops {
		l[i] = o
	}
	return map[string]any{"path": path, "expected_version": float64(v), "ops": l}
}

func (e *agentEnv) text(t *testing.T, path string) (string, drive.File) {
	t.Helper()
	f, err := e.svc.FileByPath(e.ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	rc, _, err := e.svc.Open(e.ctx, f, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return string(b), f
}

func TestEditInDeskIsFreeVersionedAndReportsDiff(t *testing.T) {
	e := newAgentEnv(t)
	args := editArgs("/Agents/sales/mio.md", 1, map[string]any{"op": "replace", "old": "escritorio", "new": "pupitre"})
	args["message"] = "cambio de palabra"
	pl := e.plan("sales", "edit", args)
	if pl.Deny != "" || pl.Approval {
		t.Fatalf("plan: %+v", pl)
	}
	rr := e.fs.Run(e.ctx, "sales", "edit", args, false)
	if !rr.OK {
		t.Fatalf("run: %+v", rr)
	}
	if rr.Data["version"] != 2 || rr.Data["previous_version"] != 1 || rr.Data["added"] != 1 || rr.Data["removed"] != 1 {
		t.Fatalf("data: %+v", rr.Data)
	}
	if !strings.Contains(rr.Text, "-mi escritorio") || !strings.Contains(rr.Text, "+mi pupitre") {
		t.Fatalf("diff: %q", rr.Text)
	}
	got, f := e.text(t, "/Agents/sales/mio.md")
	if got != "mi pupitre" || f.CurrentVersion != 2 || f.UpdatedBy != drive.AgentP("sales") {
		t.Fatalf("file: %q %+v", got, f)
	}
	vs, _ := e.svc.Versions(e.ctx, f.ID)
	if len(vs) != 2 || vs[0].Message != "cambio de palabra" || vs[0].Author != drive.AgentP("sales") {
		t.Fatalf("versions: %+v", vs)
	}
	// Undo = the existing restore-version: a NEW version with the old bytes
	if _, err := e.svc.RestoreVersion(e.ctx, drive.Human("u"), f.ID, 1, nil); err != nil {
		t.Fatal(err)
	}
	if got, f = e.text(t, "/Agents/sales/mio.md"); got != "mi escritorio" || f.CurrentVersion != 3 {
		t.Fatalf("undo: %q v%d", got, f.CurrentVersion)
	}
}

func TestEditProjectIsFreeAndSharedNeedsApproval(t *testing.T) {
	e := newAgentEnv(t)
	op := map[string]any{"op": "append", "text": "linea"}
	if pl := e.plan("sales", "edit", editArgs("/Projects/Plan/plan.md", 1, op)); pl.Deny != "" || pl.Approval {
		t.Fatalf("project: %+v", pl)
	}
	args := editArgs("/Shared/politica.md", 1, op)
	pl := e.plan("sales", "edit", args)
	if pl.Deny != "" || !pl.Approval || pl.Risk != "medium" || pl.Reversibility != "versioned" {
		t.Fatalf("shared: %+v", pl)
	}
	if r := e.fs.Run(e.ctx, "sales", "edit", args, false); r.Code != "approval_required" {
		t.Fatalf("unapproved ran: %+v", r)
	}
	if got, _ := e.text(t, "/Shared/politica.md"); got != "politica compartida" {
		t.Fatal("file changed without approval")
	}
	if r := e.fs.Run(e.ctx, "sales", "edit", args, true); !r.OK {
		t.Fatalf("approved: %+v", r)
	}
	if got, _ := e.text(t, "/Shared/politica.md"); got != "politica compartida\nlinea" {
		t.Fatalf("got %q", got)
	}
}

func TestEditRefusesWhatTheAgentCannotRead(t *testing.T) {
	e := newAgentEnv(t)
	op := map[string]any{"op": "append", "text": "x"}
	for _, p := range []string{"/Agents/legal/ajeno.md", "/Projects/Otro/otro.md", "/Clients/Acme/contrato.md"} {
		if pl := e.plan("sales", "edit", editArgs(p, 1, op)); pl.Deny != "not_granted" {
			t.Fatalf("%s: %+v", p, pl)
		}
	}
	if pl := e.plan("sales", "edit", editArgs("/Agents/sales/nope.md", 1, op)); pl.Deny != "not_found" {
		t.Fatalf("missing file: %+v", pl)
	}
	// a granted folder opens reading, and an edit there asks for approval
	if _, err := e.svc.AddGrant(e.ctx, drive.Human("u"), "sales", "/Clients/Acme"); err != nil {
		t.Fatal(err)
	}
	if pl := e.plan("sales", "edit", editArgs("/Clients/Acme/contrato.md", 1, op)); pl.Deny != "" || !pl.Approval {
		t.Fatalf("granted: %+v", pl)
	}
}

func TestEditConflictsMissingAndAmbiguousLeaveNoVersion(t *testing.T) {
	e := newAgentEnv(t)
	p := "/Agents/sales/mio.md"
	// stale expected_version
	if _, err := e.svc.Write(e.ctx, drive.Human("u"), drive.WriteInput{Path: p, Data: []byte("mi escritorio mi escritorio")}); err != nil {
		t.Fatal(err)
	}
	r := e.fs.Run(e.ctx, "sales", "edit", editArgs(p, 1, map[string]any{"op": "append", "text": "x"}), false)
	if r.OK || r.Code != "version_conflict" || r.Data["current_version"] != 2 {
		t.Fatalf("conflict: %+v", r)
	}
	// ambiguous: the code and the number of matches come back
	r = e.fs.Run(e.ctx, "sales", "edit", editArgs(p, 2, map[string]any{"op": "replace", "old": "escritorio", "new": "x"}), false)
	if r.OK || r.Code != "edit_ambiguous" || r.Data["matches"] != 2 || r.Data["op"] != 1 || !strings.Contains(r.Summary, "2 times") {
		t.Fatalf("ambiguous: %+v", r)
	}
	// missing
	r = e.fs.Run(e.ctx, "sales", "edit", editArgs(p, 2, map[string]any{"op": "replace", "old": "zzz", "new": "x"}), false)
	if r.OK || r.Code != "edit_not_found" || r.Data["matches"] != 0 {
		t.Fatalf("missing: %+v", r)
	}
	// atomic: op 1 is fine, op 2 fails: no new version
	r = e.fs.Run(e.ctx, "sales", "edit", editArgs(p, 2,
		map[string]any{"op": "replace", "old": "mi", "new": "tu", "count": 2},
		map[string]any{"op": "replace", "old": "zzz", "new": "x"}), false)
	if r.OK || r.Data["op"] != 2 {
		t.Fatalf("atomic: %+v", r)
	}
	got, f := e.text(t, p)
	if f.CurrentVersion != 2 || got != "mi escritorio mi escritorio" {
		t.Fatalf("a failed edit changed the file: v%d %q", f.CurrentVersion, got)
	}
	// expected_version is required
	args := editArgs(p, 2, map[string]any{"op": "append", "text": "x"})
	delete(args, "expected_version")
	if r := e.fs.Run(e.ctx, "sales", "edit", args, false); r.OK || r.Code != "invalid_args" || !strings.Contains(r.Summary, "expected_version") {
		t.Fatalf("no expected_version: %+v", r)
	}
	// malformed and over-cap ops are reported as data, nothing written
	args = editArgs(p, 2)
	args["ops"] = []any{}
	if r := e.fs.Run(e.ctx, "sales", "edit", args, false); r.OK || r.Code != "invalid_args" {
		t.Fatalf("empty ops: %+v", r)
	}
	many := make([]map[string]any, 51)
	for i := range many {
		many[i] = map[string]any{"op": "append", "text": "x"}
	}
	if r := e.fs.Run(e.ctx, "sales", "edit", editArgs(p, 2, many...), false); r.OK || r.Code != "invalid_args" || !strings.Contains(r.Summary, "at most 50") {
		t.Fatalf("too many ops: %+v", r)
	}
	// a no-op edit creates no version
	if r := e.fs.Run(e.ctx, "sales", "edit", editArgs(p, 2, map[string]any{"op": "replace", "old": "mi", "new": "mi", "count": 2}), false); !r.OK || r.Data["unchanged"] != true {
		t.Fatalf("no-op: %+v", r)
	}
	if _, f = e.text(t, p); f.CurrentVersion != 2 {
		t.Fatalf("no-op created v%d", f.CurrentVersion)
	}
}

func TestEditResultSizeCap(t *testing.T) {
	svc, _, _ := newSvc(drive.Limits{MaxFileBytes: 64})
	ctx := application.WithOrg(context.Background(), "o1")
	fs := &drive.AgentFS{Svc: svc}
	if _, err := svc.Write(ctx, drive.Human("u"), drive.WriteInput{Path: "/Agents/a/x.md", Data: []byte("hola")}); err != nil {
		t.Fatal(err)
	}
	r := fs.Run(ctx, "a", "edit", editArgs("/Agents/a/x.md", 1, map[string]any{"op": "append", "text": strings.Repeat("y", 100)}), false)
	if r.OK || r.Code != "file_too_large" {
		t.Fatalf("cap: %+v", r)
	}
}

func TestEditDocxAndXlsxThroughTheDrive(t *testing.T) {
	e := newAgentEnv(t)
	doc := map[string]any{"title": "Informe", "blocks": []any{
		map[string]any{"type": "heading", "level": 1, "text": "Resumen"},
		map[string]any{"type": "paragraph", "text": "El total es 100."}}}
	if r := e.fs.Run(e.ctx, "sales", "write_document", map[string]any{"path": "/Agents/sales/i.docx", "document": doc}, false); !r.OK {
		t.Fatal(r)
	}
	r := e.fs.Run(e.ctx, "sales", "edit", editArgs("/Agents/sales/i.docx", 1,
		map[string]any{"op": "replace", "old": "100", "new": "250"},
		map[string]any{"op": "set_heading", "old": "Resumen", "text": "Resumen ejecutivo"},
		map[string]any{"op": "append_paragraph", "text": "Nota final."}), false)
	if !r.OK || r.Data["version"] != 2 {
		t.Fatalf("docx: %+v", r)
	}
	if !strings.Contains(r.Text, "+El total es 250.") || !strings.Contains(r.Text, "+Nota final.") {
		t.Fatalf("docx diff: %q", r.Text)
	}
	f, _ := e.svc.FileByPath(e.ctx, "/Agents/sales/i.docx")
	tr, err := e.svc.ReadText(e.ctx, f, 0, 0, 0)
	if err != nil || !strings.Contains(tr.Text, "Resumen ejecutivo") || !strings.Contains(tr.Text, "250") {
		t.Fatalf("read back: %q %v", tr.Text, err)
	}
	if r := e.fs.Run(e.ctx, "sales", "write_document", map[string]any{"path": "/Agents/sales/p.xlsx", "document": prodDoc()}, false); !r.OK {
		t.Fatal(r)
	}
	r = e.fs.Run(e.ctx, "sales", "edit", editArgs("/Agents/sales/p.xlsx", 1,
		map[string]any{"op": "set_cell", "cell": "B2", "value": 9},
		map[string]any{"op": "add_row", "values": []any{"Regla", 3, 4}}), false)
	if !r.OK || r.Data["version"] != 2 || r.Data["added"].(int) < 1 {
		t.Fatalf("xlsx: %+v", r)
	}
	// an unsupported formula is refused and nothing is written
	r = e.fs.Run(e.ctx, "sales", "edit", editArgs("/Agents/sales/p.xlsx", 2, map[string]any{"op": "set_cell", "cell": "B2", "formula": "=cmd|'x'!A0"}), false)
	if r.OK || r.Code != "invalid_args" {
		t.Fatalf("formula: %+v", r)
	}
	// types that cannot be edited
	if _, err := e.svc.Write(e.ctx, drive.Human("u"), drive.WriteInput{Path: "/Agents/sales/x.pdf", Data: []byte("%PDF-1.4\n")}); err == nil {
		if pl := e.plan("sales", "edit", editArgs("/Agents/sales/x.pdf", 1, map[string]any{"op": "append", "text": "x"})); pl.Deny != "unsupported_type" {
			t.Fatalf("pdf: %+v", pl)
		}
	}
}

func TestGatewayEditAssessRunAndNotify(t *testing.T) {
	e := newAgentEnv(t)
	type note struct {
		path                     string
		from, to, added, removed int
	}
	var edits []note
	var acts []string
	tool := &drive.GatewayTool{FS: e.fs,
		AgentTools: func(_ context.Context, _, agent string) ([]string, error) {
			if agent == "ro" {
				return []string{"fs_read"}, nil
			}
			return []string{"fs"}, nil
		},
		Notify: func(_ context.Context, _, _, _, action, path string) { acts = append(acts, action+" "+path) },
		NotifyEdit: func(_ context.Context, _, _, _, path string, from, to, added, removed int) {
			edits = append(edits, note{path, from, to, added, removed})
		}}
	if side, ok := tool.SideEffect("edit"); !ok || !side {
		t.Fatal("edit has side effects")
	}
	call := application.GatewayCall{Org: "o1", AgentID: "sales", TaskID: "t1", Tool: "fs", Action: "edit",
		Args: editArgs("/Agents/sales/mio.md", 1, map[string]any{"op": "replace", "old": "mi", "new": "tu"})}
	if as := tool.Assess(e.ctx, call); as.DenyCode != "" || as.NeedsApproval || !strings.Contains(as.Pending.Title, "fs.edit /Agents/sales/mio.md") {
		t.Fatalf("assess: %+v", as)
	}
	res := tool.Run(e.ctx, call, false)
	if res.Status != "succeeded" || !strings.Contains(res.Summary, "version 1 -> 2") {
		t.Fatalf("run: %+v", res)
	}
	if len(edits) != 1 || edits[0] != (note{"/Agents/sales/mio.md", 1, 2, 1, 1}) || len(acts) != 1 || acts[0] != "write /Agents/sales/mio.md" {
		t.Fatalf("notify: %+v %v", edits, acts)
	}
	call.Args = editArgs("/Shared/politica.md", 1, map[string]any{"op": "append", "text": "x"})
	if as := tool.Assess(e.ctx, call); !as.NeedsApproval {
		t.Fatalf("shared: %+v", as)
	}
	call.AgentID = "ro"
	if as := tool.Assess(e.ctx, call); as.DenyCode != "tool_not_granted" {
		t.Fatalf("read-only agent: %+v", as)
	}
	// a failed edit reports the code and the match count in the summary
	call.AgentID = "sales"
	call.Args = editArgs("/Agents/sales/mio.md", 2, map[string]any{"op": "replace", "old": "zzz", "new": "x"})
	res = tool.Run(e.ctx, call, false)
	if res.Status != "failed" || res.Code != "edit_not_found" || !strings.Contains(res.Summary, "0 matches") {
		t.Fatalf("failed: %+v", res)
	}
}
