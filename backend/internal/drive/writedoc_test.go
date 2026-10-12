package drive_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"aiworkforce/backend/internal/drive"
)

func prodDoc() map[string]any {
	return map[string]any{"title": "Productos", "sheets": []any{map[string]any{
		"name":    "Productos",
		"columns": []any{map[string]any{"header": "Producto"}, map[string]any{"header": "Precio", "type": "currency"}, map[string]any{"header": "Stock", "type": "number"}},
		"rows":    []any{[]any{"Lapiz", 1.5, 10.0}, []any{"=cmd", 2.0, 5.0}},
		"totals":  map[string]any{},
	}}}
}

func TestWriteDocumentXLSXInDeskAndProject(t *testing.T) {
	e := newAgentEnv(t)
	for _, p := range []string{"/Agents/sales/productos.xlsx", "/Projects/Plan/productos.xlsx"} {
		args := map[string]any{"path": p, "document": prodDoc()}
		pl := e.plan("sales", "write_document", args)
		if pl.Deny != "" || pl.Approval {
			t.Fatalf("%s should be free: %+v", p, pl)
		}
		rr := e.fs.Run(e.ctx, "sales", "write_document", args, false)
		if !rr.OK {
			t.Fatalf("run: %+v", rr)
		}
		f, err := e.svc.FileByPath(e.ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if f.Mime != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" || f.CreatedBy != drive.AgentP("sales") {
			t.Fatalf("file = %+v", f)
		}
		rc, _, err := e.svc.Open(e.ctx, f, 0)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		x, err := excelize.OpenReader(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("the stored file is not a workbook: %v", err)
		}
		rows, _ := x.GetRows("Productos")
		if len(rows) != 4 || rows[0][0] != "Producto" || rows[2][0] != "=cmd" {
			t.Fatalf("rows = %v", rows)
		}
		if fm, _ := x.GetCellFormula("Productos", "A3"); fm != "" {
			t.Fatalf("a text cell became a formula: %q", fm)
		}
		x.Close()
		// the text of the workbook is searchable (cached values, nothing executed)
		if f.Extraction == "" || !strings.Contains(f.SearchText, "Lapiz") {
			t.Fatalf("extraction = %q search = %q", f.Extraction, f.SearchText)
		}
	}
}

func TestWriteDocumentOutsideZonesAsksAndSharesRules(t *testing.T) {
	e := newAgentEnv(t)
	for _, p := range []string{"/Shared/p.xlsx", "/Agents/legal/p.docx", "/Projects/Otro/p.pdf", "/Clients/Acme/p.xlsx"} {
		pl := e.plan("sales", "write_document", map[string]any{"path": p, "document": map[string]any{"blocks": []any{map[string]any{"type": "paragraph", "text": "hola"}}}})
		if pl.Deny != "" || !pl.Approval || !contains(pl.Flags, "outside_scope") {
			t.Fatalf("%s must ask: %+v", p, pl)
		}
		if rr := e.fs.Run(e.ctx, "sales", "write_document", map[string]any{"path": p, "document": prodDoc()}, false); rr.OK || rr.Code != "approval_required" {
			t.Fatalf("%s without approval: %+v", p, rr)
		}
	}
	// approved: it is written
	if rr := e.fs.Run(e.ctx, "sales", "write_document", map[string]any{"path": "/Shared/p.xlsx", "document": prodDoc()}, true); !rr.OK {
		t.Fatalf("approved: %+v", rr)
	}
}

func TestWriteDocumentRefusals(t *testing.T) {
	e := newAgentEnv(t)
	base := func(p string, doc any) map[string]any { return map[string]any{"path": p, "document": doc} }
	if pl := e.plan("sales", "write_document", base("/Agents/sales/x.exe", prodDoc())); pl.Deny != "unsupported_type" {
		t.Fatalf("exe: %+v", pl)
	}
	if pl := e.plan("sales", "write_document", base("/Agents/sales/x.html", prodDoc())); pl.Deny != "unsupported_type" {
		t.Fatalf("html: %+v", pl)
	}
	if pl := e.plan("sales", "write_document", base("../x.xlsx", prodDoc())); pl.Deny == "" {
		t.Fatalf("traversal: %+v", pl)
	}
	// a document that cannot be built is not a policy denial: the agent reads why
	args := base("/Agents/sales/mal.xlsx", map[string]any{"sheets": []any{map[string]any{"name": "a", "columns": []any{"A"}, "rows": []any{[]any{"x"}},
		"formulas": []any{map[string]any{"cell": "C2", "formula": "HYPERLINK(\"http://x\",\"y\")"}}}}})
	pl := e.plan("sales", "write_document", args)
	if pl.Deny != "" || pl.Approval {
		t.Fatalf("bad document: %+v", pl)
	}
	rr := e.fs.Run(e.ctx, "sales", "write_document", args, false)
	if rr.OK || rr.Code != "invalid_args" || !strings.Contains(rr.Summary, "not allowed") {
		t.Fatalf("bad document run: %+v", rr)
	}
	if _, err := e.svc.FileByPath(e.ctx, "/Agents/sales/mal.xlsx"); err == nil {
		t.Fatal("nothing must be written for a bad document")
	}
	// the plain write still refuses binary types and says where to go
	pl = e.plan("sales", "write", map[string]any{"path": "/Agents/sales/a.xlsx", "content": "x"})
	if pl.Deny != "unsupported_type" || !strings.Contains(pl.DenyMsg, "write_document") {
		t.Fatalf("plain write of xlsx: %+v", pl)
	}
	// size limit
	small, _, _ := newSvc(drive.Limits{MaxFileBytes: 3000})
	sfs := &drive.AgentFS{Svc: small}
	huge := make([]any, 0, 200)
	for i := 0; i < 200; i++ {
		huge = append(huge, []any{strings.Repeat("x", 100), strings.Repeat("y", 100)})
	}
	pl = sfs.Plan(e.ctx, "sales", "write_document", base("/Agents/sales/huge.csv", map[string]any{"sheets": []any{map[string]any{"name": "h", "columns": []any{"A", "B"}, "rows": huge}}}))
	if pl.Deny != "file_too_large" {
		t.Fatalf("huge: deny=%q", pl.Deny)
	}
}

func TestFsWriteWithDocumentArgIsWriteDocument(t *testing.T) {
	e := newAgentEnv(t)
	args := map[string]any{"path": "/Agents/sales/informe.docx", "document": map[string]any{"title": "Informe", "blocks": []any{
		map[string]any{"type": "heading", "level": 1, "text": "Resumen"}, map[string]any{"type": "bullets", "items": []any{"a", "b"}}}}}
	if rr := e.fs.Run(e.ctx, "sales", "write", args, false); !rr.OK {
		t.Fatalf("%+v", rr)
	}
	f, err := e.svc.FileByPath(e.ctx, "/Agents/sales/informe.docx")
	if err != nil || !strings.Contains(f.SearchText, "Resumen") {
		t.Fatalf("docx = %+v err=%v", f, err)
	}
	if !drive.IsWriteAction(drive.ActWriteDocument) {
		t.Fatal("write_document is a write action")
	}
}

func TestWriteDocumentPDFAndRerunMakesNewVersion(t *testing.T) {
	e := newAgentEnv(t)
	args := map[string]any{"path": "/Agents/sales/r.pdf", "document": map[string]any{"title": "R", "blocks": []any{map[string]any{"type": "paragraph", "text": "uno"}}}}
	if rr := e.fs.Run(e.ctx, "sales", "write_document", args, false); !rr.OK {
		t.Fatalf("%+v", rr)
	}
	args["document"] = map[string]any{"title": "R", "blocks": []any{map[string]any{"type": "paragraph", "text": "dos"}}}
	rr := e.fs.Run(e.ctx, "sales", "write_document", args, false)
	if !rr.OK || rr.Data["version"] != 2 {
		t.Fatalf("second write: %+v", rr)
	}
}
