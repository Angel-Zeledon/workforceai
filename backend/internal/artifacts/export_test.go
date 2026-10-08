package artifacts_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/domain"
)

func unzip(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func TestExportXlsxNeutralizesFormulaInjection(t *testing.T) {
	e := newEnv(t, nil)
	a := e.sheetArt(`{"A1":{"v":"Ventas"},"A2":{"v":"=HYPERLINK(\"http://evil\",\"x\")"},"A3":{"v":"+1+1"},"A4":{"v":"-2"},"A5":{"v":"@SUM(A1)"},"A6":{"v":"\tcmd"},"A7":{"v":"\rcmd"},"B1":{"v":42},"B2":{"v":7},"B3":{"v":0,"f":"=B1+B2"}}`)
	f, err := e.svc.Export(e.ctx, e.me, a.ID, 0, "XLSX")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(f.Filename, ".xlsx") || f.MIME == "" {
		t.Fatalf("file: %+v", f)
	}
	parts := unzip(t, f.Data)
	sh := parts["xl/worksheets/sheet1.xml"]
	for _, want := range []string{"Ventas", "<v>42</v>", "<f>B1+B2</f>"} {
		if !strings.Contains(sh, want) {
			t.Errorf("missing %q in %s", want, sh)
		}
	}
	if !strings.Contains(parts["xl/workbook.xml"], `name="Margen"`) {
		t.Errorf("workbook: %s", parts["xl/workbook.xml"])
	}
	// Every dangerous text cell is an inline string with the quotePrefix style and no <f>.
	for _, ref := range []string{"A2", "A3", "A4", "A5", "A6", "A7"} {
		i := strings.Index(sh, `r="`+ref+`"`)
		if i < 0 {
			t.Fatalf("no cell %s", ref)
		}
		cell := sh[i : i+strings.Index(sh[i:], "</c>")]
		if !strings.Contains(cell, `s="1"`) || !strings.Contains(cell, `t="inlineStr"`) || strings.Contains(cell, "<f>") {
			t.Errorf("%s not neutralized: %s", ref, cell)
		}
	}
	if strings.Count(sh, "<f>") != 1 {
		t.Errorf("only the legitimate formula may be exported: %s", sh)
	}
	if !strings.Contains(parts["xl/styles.xml"], `quotePrefix="1"`) {
		t.Error("styles lack quotePrefix")
	}
	found := false
	for _, l := range e.store.Audit() {
		found = found || l.Action == "artifact.exported"
	}
	if !found {
		t.Error("no artifact.exported audit entry")
	}
}

func TestExportTableAndDoc(t *testing.T) {
	e := newEnv(t, nil)
	tb := e.create(e.me, artifacts.CreateInput{Kind: artifacts.KindTable, Title: "Clientes", Content: raw(
		`{"schema":"aiw.table/1","columns":[{"key":"n","label":"Nombre","type":"text"}],"rows":[{"id":"r1","cells":{"n":"=cmd|' /C calc'!A0"}},{"id":"r2","cells":{"n":"Ana"}}]}`)})
	f, err := e.svc.Export(e.ctx, e.me, tb.ID, 0, "xlsx")
	if err != nil {
		t.Fatal(err)
	}
	sh := unzip(t, f.Data)["xl/worksheets/sheet1.xml"]
	if !strings.Contains(sh, "Nombre") || !strings.Contains(sh, "Ana") || !strings.Contains(sh, `s="1"`) {
		t.Errorf("table: %s", sh)
	}
	doc := e.create(e.me, artifacts.CreateInput{Kind: artifacts.KindDoc, Title: "Contrato <1>", Content: raw(
		`{"schema":"aiw.doc/1","doc":{"type":"doc","content":[{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"Titulo & co"}]},{"type":"paragraph","content":[{"type":"text","text":"Hola "},{"type":"text","text":"mundo","marks":[{"type":"bold"}]}]}]}}`)})
	f, err = e.svc.Export(e.ctx, e.me, doc.ID, 0, "docx")
	if err != nil {
		t.Fatal(err)
	}
	parts := unzip(t, f.Data)
	d := parts["word/document.xml"]
	if !strings.Contains(d, "Titulo &amp; co") || !strings.Contains(d, "mundo") || !strings.Contains(d, `Heading1`) {
		t.Errorf("docx: %s", d)
	}
	if strings.ContainsAny(f.Filename, "<>/\\") {
		t.Errorf("filename %q", f.Filename)
	}
	// Wrong format for the kind.
	if _, err := e.svc.Export(e.ctx, e.me, doc.ID, 0, "xlsx"); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("doc as xlsx: %v", err)
	}
	if _, err := e.svc.Export(e.ctx, e.me, tb.ID, 0, "pdf"); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("table as pdf: %v", err)
	}
}

func TestExportOtherTenantIsNotFound(t *testing.T) {
	e := newEnv(t, nil)
	a := e.sheetArt(`{"A1":{"v":1}}`)
	other := application.WithOrg(e.ctx, "another-org")
	if _, err := e.svc.Export(other, e.me, a.ID, 0, "xlsx"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other tenant: %v", err)
	}
}
