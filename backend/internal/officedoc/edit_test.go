package officedoc

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func ops(t *testing.T, raw ...map[string]any) []Op {
	t.Helper()
	l := make([]any, len(raw))
	for i, r := range raw {
		l[i] = r
	}
	o, err := ParseOps(l)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func editStr(t *testing.T, s string, raw ...map[string]any) (string, *EditError) {
	t.Helper()
	out, err := Edit("a.md", []byte(s), ops(t, raw...))
	return string(out), err
}

func TestTextReplaceInsertDeleteAppend(t *testing.T) {
	src := "# Title\n\nFirst para.\nSecond para.\n"
	out, err := editStr(t, src,
		map[string]any{"op": "replace", "old": "# Title", "new": "# New title"},
		map[string]any{"op": "insert_after", "anchor": "First para.\n", "text": "Inserted.\n"},
		map[string]any{"op": "insert_before", "anchor": "Second", "text": ">> "},
		map[string]any{"op": "delete", "text": "Inserted.\n"},
		map[string]any{"op": "append", "text": "Last."},
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := "# New title\n\nFirst para.\n>> Second para.\nLast."; out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestTextMissingAmbiguousAndCount(t *testing.T) {
	src := "a b a b a\n"
	_, err := editStr(t, src, map[string]any{"op": "replace", "old": "zzz", "new": "x"})
	if err == nil || err.Code != CodeNotFound || err.Matches != 0 || err.Op != 1 {
		t.Fatalf("missing: %+v", err)
	}
	_, err = editStr(t, src, map[string]any{"op": "replace", "old": "a", "new": "x"})
	if err == nil || err.Code != CodeAmbiguous || err.Matches != 3 {
		t.Fatalf("ambiguous: %+v", err)
	}
	_, err = editStr(t, src, map[string]any{"op": "replace", "old": "a", "new": "x", "count": 5})
	if err == nil || err.Code != CodeCountMismatch || err.Matches != 3 {
		t.Fatalf("mismatch: %+v", err)
	}
	out, err := editStr(t, src, map[string]any{"op": "replace", "old": "a", "new": "x", "count": 3})
	if err != nil || out != "x b x b x\n" {
		t.Fatalf("count 3: %q %+v", out, err)
	}
	out, err = editStr(t, src, map[string]any{"op": "replace", "old": "b", "new": "y", "count": "all"})
	if err != nil || out != "a y a y a\n" {
		t.Fatalf("all: %q %+v", out, err)
	}
}

func TestTextEditIsAtomicAndSequential(t *testing.T) {
	src := "one two\n"
	// the second op sees the result of the first
	out, err := editStr(t, src,
		map[string]any{"op": "replace", "old": "one", "new": "uno"},
		map[string]any{"op": "replace", "old": "uno two", "new": "dos"})
	if err != nil || out != "dos\n" {
		t.Fatalf("%q %+v", out, err)
	}
	// the third op fails: nothing is returned
	out, err = editStr(t, src,
		map[string]any{"op": "replace", "old": "one", "new": "uno"},
		map[string]any{"op": "replace", "old": "two", "new": "dos"},
		map[string]any{"op": "replace", "old": "missing", "new": "x"})
	if err == nil || err.Op != 3 || out != "" {
		t.Fatalf("atomic: %q %+v", out, err)
	}
}

func TestTextRegexIsOptInAndSafe(t *testing.T) {
	src := "v1.2 and v3.4\n"
	// without regex:true the text is literal
	if _, err := editStr(t, src, map[string]any{"op": "replace", "old": `v\d\.\d`, "new": "x"}); err == nil || err.Code != CodeNotFound {
		t.Fatalf("literal: %+v", err)
	}
	out, err := editStr(t, src, map[string]any{"op": "replace", "old": `v(\d)\.\d`, "new": "ver$1", "regex": true, "count": 2})
	if err != nil || out != "ver1 and ver3\n" {
		t.Fatalf("regex: %q %+v", out, err)
	}
	if _, err := editStr(t, src, map[string]any{"op": "replace", "old": `x*`, "new": "y", "regex": true}); err == nil || err.Code != CodeInvalidArgs {
		t.Fatalf("empty match: %+v", err)
	}
	if _, err := editStr(t, src, map[string]any{"op": "replace", "old": `(`, "new": "y", "regex": true}); err == nil || err.Code != CodeInvalidArgs {
		t.Fatalf("bad regex: %+v", err)
	}
}

func TestTextKeepsCRLFAndChecksJSON(t *testing.T) {
	out, err := Edit("a.txt", []byte("a\r\nb\r\n"), ops(t, map[string]any{"op": "replace", "old": "a\nb", "new": "a\nc"}))
	if err != nil || string(out) != "a\r\nc\r\n" {
		t.Fatalf("crlf: %q %+v", out, err)
	}
	if _, err := Edit("a.json", []byte(`{"a":1}`), ops(t, map[string]any{"op": "replace", "old": `1}`, "new": `1`})); err == nil || err.Code != CodeInvalidResult {
		t.Fatalf("json: %+v", err)
	}
	out, err = Edit("a.json", []byte(`{"a":1}`), ops(t, map[string]any{"op": "replace", "old": `"a":1`, "new": `"a":2`}))
	if err != nil || string(out) != `{"a":2}` {
		t.Fatalf("json ok: %q %+v", out, err)
	}
	out, err = Edit("t.csv", []byte("n,v\nx,1\n"), ops(t, map[string]any{"op": "append", "text": "y,2\n"}))
	if err != nil || string(out) != "n,v\nx,1\ny,2\n" {
		t.Fatalf("csv: %q %+v", out, err)
	}
}

func TestParseOpsCaps(t *testing.T) {
	if _, err := ParseOps(nil); err == nil {
		t.Fatal("nil ops")
	}
	if _, err := ParseOps([]any{}); err == nil {
		t.Fatal("empty ops")
	}
	many := make([]any, MaxEditOps+1)
	for i := range many {
		many[i] = map[string]any{"op": "append", "text": "x"}
	}
	if _, err := ParseOps(many); err == nil || !strings.Contains(err.Msg, "at most") {
		t.Fatalf("too many: %+v", err)
	}
	if _, err := ParseOps([]any{map[string]any{"op": "append", "text": strings.Repeat("x", MaxEditOneRunes+1)}}); err == nil {
		t.Fatal("one string too long")
	}
	big := strings.Repeat("y", MaxEditOneRunes)
	var l []any
	for i := 0; i < 11; i++ {
		l = append(l, map[string]any{"op": "append", "text": big})
	}
	if _, err := ParseOps(l); err == nil {
		t.Fatal("total too long")
	}
	if o, err := ParseOps(`[{"op":"append","text":"x"}]`); err != nil || len(o) != 1 {
		t.Fatalf("json string: %+v", err)
	}
	if _, err := Edit("a.md", bytes.Repeat([]byte("a"), MaxEditTextBytes+1), ops(t, map[string]any{"op": "append", "text": "x"})); err == nil || err.Code != CodeTooLarge {
		t.Fatalf("large file: %+v", err)
	}
	if _, err := Edit("a.pdf", []byte("x"), ops(t, map[string]any{"op": "append", "text": "x"})); err == nil || err.Code != CodeUnsupported {
		t.Fatalf("pdf: %+v", err)
	}
}

// ---- docx ----

func docxFile(t *testing.T) []byte {
	t.Helper()
	d, err := Parse(map[string]any{"title": "Informe", "blocks": []any{
		map[string]any{"type": "heading", "level": 1, "text": "Resumen"},
		map[string]any{"type": "paragraph", "text": "El total es 100 y el total final es 200."},
		map[string]any{"type": "heading", "level": 2, "text": "Detalle"},
		map[string]any{"type": "paragraph", "text": "Segundo parrafo."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(FormatDOCX, d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func docxXML(t *testing.T, data []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			return string(b)
		}
	}
	t.Fatal("no document.xml")
	return ""
}

func TestDocxReplaceSetHeadingAppend(t *testing.T) {
	src := docxFile(t)
	out, err := Edit("r.docx", src, ops(t,
		map[string]any{"op": "replace", "old": "Segundo", "new": "Tercer"},
		map[string]any{"op": "replace", "old": "total", "new": "monto", "count": 2},
		map[string]any{"op": "set_heading", "old": "Resumen", "text": "Resumen ejecutivo"},
		map[string]any{"op": "set_heading", "index": 2, "text": "Detalles <b>&"},
		map[string]any{"op": "append_paragraph", "text": "Fin del informe."},
	))
	if err != nil {
		t.Fatal(err)
	}
	x := docxXML(t, out)
	for _, want := range []string{"Tercer parrafo.", "El monto es 100 y el monto final es 200.", "Resumen ejecutivo", "Detalles &lt;b&gt;&amp;", "Fin del informe."} {
		if !strings.Contains(x, want) {
			t.Fatalf("missing %q in %s", want, x)
		}
	}
	if !strings.Contains(x, `<w:pStyle w:val="Heading1"/>`) || strings.Index(x, "Fin del informe.") > strings.Index(x, "<w:sectPr") {
		t.Fatalf("styles lost or paragraph after sectPr: %s", x)
	}
	// the package is still a document the drive accepts and reads
	if len(out) == 0 || !bytes.HasPrefix(out, []byte("PK")) {
		t.Fatal("not a zip")
	}
	// the other parts are kept
	zr, _ := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if len(zr.File) != 7 {
		t.Fatalf("parts = %d", len(zr.File))
	}
}

func TestDocxReplaceAcrossRunsKeepsFirstRunFormat(t *testing.T) {
	src := docxFile(t)
	// split a paragraph into two runs, the first bold
	x := docxXML(t, src)
	x2 := strings.Replace(x, "Segundo parrafo.", "Segundo </w:t></w:r><w:r><w:t xml:space=\"preserve\">parrafo.", 1)
	x2 = strings.Replace(x2, `<w:r><w:t xml:space="preserve">Segundo`, `<w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve">Segundo`, 1)
	zr, _ := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		w, _ := zw.Create(f.Name)
		if f.Name == "word/document.xml" {
			w.Write([]byte(x2))
			continue
		}
		rc, _ := f.Open()
		io.Copy(w, rc)
		rc.Close()
	}
	zw.Close()
	out, err := Edit("r.docx", buf.Bytes(), ops(t, map[string]any{"op": "replace", "old": "do parr", "new": "X"}))
	if err != nil {
		t.Fatal(err)
	}
	got := docxXML(t, out)
	if !strings.Contains(got, `<w:b/></w:rPr><w:t xml:space="preserve">SegunXafo.`) && !strings.Contains(got, "SegunX") {
		t.Fatalf("got %s", got)
	}
	if !strings.Contains(got, "<w:b/>") {
		t.Fatal("formatting of the first run lost")
	}
}

func TestDocxErrorsAndAtomic(t *testing.T) {
	src := docxFile(t)
	_, err := Edit("r.docx", src, ops(t, map[string]any{"op": "replace", "old": "total", "new": "x"}))
	if err == nil || err.Code != CodeAmbiguous || err.Matches != 2 {
		t.Fatalf("ambiguous: %+v", err)
	}
	_, err = Edit("r.docx", src, ops(t, map[string]any{"op": "replace", "old": "nope", "new": "x"}))
	if err == nil || err.Code != CodeNotFound {
		t.Fatalf("missing: %+v", err)
	}
	_, err = Edit("r.docx", src, ops(t, map[string]any{"op": "replace", "old": "Segundo", "new": "a\nb"}))
	if err == nil || err.Code != CodeInvalidArgs {
		t.Fatalf("newline: %+v", err)
	}
	_, err = Edit("r.docx", src, ops(t, map[string]any{"op": "set_heading", "old": "Segundo parrafo.", "text": "x"}))
	if err == nil || err.Code != CodeNotFound {
		t.Fatalf("not a heading: %+v", err)
	}
	_, err = Edit("r.docx", src, ops(t,
		map[string]any{"op": "append_paragraph", "text": "ok"},
		map[string]any{"op": "bogus"}))
	if err == nil || err.Op != 2 {
		t.Fatalf("unknown op: %+v", err)
	}
	if _, err := Edit("r.docx", []byte("not a zip"), ops(t, map[string]any{"op": "append_paragraph", "text": "x"})); err == nil || err.Code != CodeUnsupported {
		t.Fatalf("garbage: %+v", err)
	}
}

// ---- xlsx ----

func xlsxFile(t *testing.T) []byte {
	t.Helper()
	d, err := Parse(map[string]any{"sheets": []any{map[string]any{
		"name":    "Productos",
		"columns": []any{map[string]any{"header": "Producto"}, map[string]any{"header": "Precio", "type": "currency"}},
		"rows":    []any{[]any{"Lapiz", 1.5}, []any{"Goma", 2.0}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(FormatXLSX, d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestXlsxSetCellAddRowRename(t *testing.T) {
	out, err := Edit("p.xlsx", xlsxFile(t), ops(t,
		map[string]any{"op": "set_cell", "cell": "b2", "value": 9.25},
		map[string]any{"op": "set_cell", "cell": "A3", "value": "=cmd|' /C calc'!A0"},
		map[string]any{"op": "add_row", "values": []any{"Regla", 3}},
		map[string]any{"op": "set_cell", "cell": "B6", "formula": "=SUM(B2:B5)"},
		map[string]any{"op": "rename_sheet", "sheet": "Productos", "name": "Catalogo"},
	))
	if err != nil {
		t.Fatal(err)
	}
	x, e := excelize.OpenReader(bytes.NewReader(out))
	if e != nil {
		t.Fatal(e)
	}
	defer x.Close()
	if x.GetSheetList()[0] != "Catalogo" {
		t.Fatalf("sheets %v", x.GetSheetList())
	}
	v, _ := x.GetCellValue("Catalogo", "B2")
	if v == "" || !strings.Contains(v, "9") {
		t.Fatalf("B2 = %q", v)
	}
	a3, _ := x.GetCellValue("Catalogo", "A3")
	if a3 != "=cmd|' /C calc'!A0" {
		t.Fatalf("A3 = %q", a3)
	}
	if fm, _ := x.GetCellFormula("Catalogo", "A3"); fm != "" {
		t.Fatalf("text became a formula: %q", fm)
	}
	a4, _ := x.GetCellValue("Catalogo", "A4")
	if a4 != "Regla" {
		t.Fatalf("A4 = %q", a4)
	}
	if fm, _ := x.GetCellFormula("Catalogo", "B6"); fm != "SUM(B2:B5)" {
		t.Fatalf("formula = %q", fm)
	}
}

func TestXlsxRefusals(t *testing.T) {
	src := xlsxFile(t)
	cases := []map[string]any{
		{"op": "set_cell", "cell": "B2", "formula": "=cmd|'x'!A0"},
		{"op": "set_cell", "cell": "B2", "formula": "=HYPERLINK(\"http://x\")"},
		{"op": "set_cell", "cell": "ZZZ99999", "value": 1},
		{"op": "set_cell", "cell": "B2"},
		{"op": "set_cell", "cell": "B2", "value": 1, "sheet": "Nope"},
		{"op": "add_row", "values": []any{}},
		{"op": "rename_sheet", "name": "a/b"},
		{"op": "replace", "old": "a", "new": "b"},
	}
	for i, c := range cases {
		if _, err := Edit("p.xlsx", src, ops(t, c)); err == nil {
			t.Fatalf("case %d accepted: %v", i, c)
		}
	}
	if _, err := Edit("p.xlsx", []byte("junk"), ops(t, map[string]any{"op": "set_cell", "cell": "A1", "value": 1})); err == nil || err.Code != CodeUnsupported {
		t.Fatalf("junk: %+v", err)
	}
}
