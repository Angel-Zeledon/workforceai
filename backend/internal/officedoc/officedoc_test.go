package officedoc

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func mustParse(t *testing.T, raw any) Doc {
	t.Helper()
	d, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return d
}

func products() map[string]any {
	return map[string]any{
		"title": "Productos",
		"sheets": []any{map[string]any{
			"name": "Productos",
			"columns": []any{
				map[string]any{"header": "Producto", "type": "text"},
				map[string]any{"header": "Precio", "type": "currency"},
				map[string]any{"header": "Stock", "type": "number"},
				map[string]any{"header": "Margen", "type": "percent"},
				map[string]any{"header": "Alta", "type": "date"},
			},
			"rows": []any{
				[]any{"Lapiz", 1.5, 100.0, 0.25, "2026-01-31"},
				[]any{"Cuaderno", "$3,25", 40.0, "30%", "2026-02-01"},
				[]any{"=HYPERLINK(\"http://evil\",\"x\")", 10.0, 5.0, 0.1, "no es fecha"},
			},
			"totals": map[string]any{"label": "Total", "columns": []any{"Precio", "Stock"}},
		}},
	}
}

func openX(t *testing.T, b []byte) *excelize.File {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("excelize cannot open the file: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestXLSXSheetsRowsAndTypes(t *testing.T) {
	b, err := Build("xlsx", mustParse(t, products()))
	if err != nil {
		t.Fatal(err)
	}
	f := openX(t, b)
	if got := f.GetSheetList(); len(got) != 1 || got[0] != "Productos" {
		t.Fatalf("sheets = %v", got)
	}
	rows, err := f.GetRows("Productos", excelize.Options{RawCellValue: true})
	if err != nil || len(rows) != 5 { // header + 3 + totals
		t.Fatalf("rows = %d err=%v", len(rows), err)
	}
	if rows[0][0] != "Producto" || rows[0][4] != "Alta" {
		t.Fatalf("header = %v", rows[0])
	}
	// numbers are real numbers
	for _, ref := range []string{"B2", "C2", "D2", "B3", "D3"} {
		ty, _ := f.GetCellType("Productos", ref)
		if ty != excelize.CellTypeNumber && ty != excelize.CellTypeUnset {
			t.Errorf("%s type = %v, want numeric", ref, ty)
		}
	}
	if v, _ := f.GetCellValue("Productos", "B3", excelize.Options{RawCellValue: true}); v != "3.25" {
		t.Errorf("B3 = %q (the currency text must be parsed to 3.25)", v)
	}
	if v, _ := f.GetCellValue("Productos", "D3", excelize.Options{RawCellValue: true}); v != "0.3" {
		t.Errorf("D3 = %q (30%% must be 0.3)", v)
	}
	// a date column holds a date serial; text that is not a date stays text
	if v, _ := f.GetCellValue("Productos", "E2", excelize.Options{RawCellValue: true}); v != "46053" {
		t.Errorf("E2 = %q, want the serial of 2026-01-31", v)
	}
	if v, _ := f.GetCellValue("Productos", "E4"); v != "no es fecha" {
		t.Errorf("E4 = %q", v)
	}
	// formatted value of the price: currency
	if v, _ := f.GetCellValue("Productos", "B2"); !strings.Contains(v, "1.50") || !strings.Contains(v, "$") {
		t.Errorf("B2 formatted = %q", v)
	}
	// totals row: a SUM formula with its cached value
	if fm, _ := f.GetCellFormula("Productos", "B5"); fm != "SUM(B2:B4)" {
		t.Errorf("B5 formula = %q", fm)
	}
	if v, _ := f.GetCellValue("Productos", "B5", excelize.Options{RawCellValue: true}); v != "14.75" {
		t.Errorf("B5 cached = %q, want 14.75", v)
	}
	if ty, _ := f.GetCellType("Productos", "B5"); ty != excelize.CellTypeUnset && ty != excelize.CellTypeNumber {
		t.Errorf("B5 cached result must be numeric, type = %v", ty) // not t="str"
	}
	if v, _ := f.GetCellValue("Productos", "B5"); v != "$14.75" {
		t.Errorf("B5 formatted = %q (a cached formula result keeps the number format)", v)
	}
	if v, _ := f.GetCellValue("Productos", "C5", excelize.Options{RawCellValue: true}); v != "145" {
		t.Errorf("C5 cached = %q, want 145", v)
	}
	if v, _ := f.GetCellValue("Productos", "A5"); v != "Total" {
		t.Errorf("A5 = %q", v)
	}
	// frozen header
	if p, _ := f.GetPanes("Productos"); !p.Freeze || p.TopLeftCell != "A2" {
		t.Errorf("panes = %+v", p)
	}
}

func TestXLSXFormulaInjectionIsText(t *testing.T) {
	evil := []string{`=HYPERLINK("http://evil","x")`, `+1+1`, `-2+3`, `@SUM(A1:A2)`, "=cmd|' /C calc'!A0", "\t=1+1"}
	var rows []any
	for _, e := range evil {
		rows = append(rows, []any{e})
	}
	d := mustParse(t, map[string]any{"sheets": []any{map[string]any{"name": "x", "columns": []any{map[string]any{"header": "=1+1", "type": "text"}}, "rows": rows}}})
	b, err := Build("xlsx", d)
	if err != nil {
		t.Fatal(err)
	}
	f := openX(t, b)
	if fm, _ := f.GetCellFormula("x", "A1"); fm != "" {
		t.Errorf("header became a formula: %q", fm)
	}
	for i, e := range evil {
		ref, _ := excelize.CoordinatesToCellName(1, i+2)
		if fm, _ := f.GetCellFormula("x", ref); fm != "" {
			t.Errorf("%s became a formula %q", ref, fm)
		}
		ty, _ := f.GetCellType("x", ref)
		if ty == excelize.CellTypeFormula || ty == excelize.CellTypeNumber {
			t.Errorf("%s has type %v", ref, ty)
		}
		if v, _ := f.GetCellValue("x", ref); v != strings.TrimRight(cleanText(e), "") {
			t.Errorf("%s = %q, want the text %q", ref, v, e)
		}
	}
	// raw XML: no <f> element at all
	zr, _ := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	for _, zf := range zr.File {
		if strings.HasPrefix(zf.Name, "xl/worksheets/") {
			rc, _ := zf.Open()
			data, _ := io.ReadAll(rc)
			rc.Close()
			if bytes.Contains(data, []byte("<f>")) || bytes.Contains(data, []byte("<f ")) {
				t.Errorf("%s contains a formula element", zf.Name)
			}
		}
	}
}

func TestDeclaredFormulasAreValidated(t *testing.T) {
	mk := func(fs ...any) map[string]any {
		return map[string]any{"sheets": []any{map[string]any{"name": "s",
			"columns":  []any{"A", map[string]any{"header": "N", "type": "number"}},
			"rows":     []any{[]any{"a", 1.0}, []any{"b", 2.0}, []any{"c", 6.0}},
			"formulas": fs}}}
	}
	okd := mustParse(t, mk(map[string]any{"cell": "D2", "formula": "=AVERAGE(B2:B4)"}, map[string]any{"cell": "D3", "formula": "SUM(B2:B3)"}))
	b, err := Build("xlsx", okd)
	if err != nil {
		t.Fatal(err)
	}
	f := openX(t, b)
	if fm, _ := f.GetCellFormula("s", "D2"); fm != "AVERAGE(B2:B4)" {
		t.Errorf("D2 = %q", fm)
	}
	if v, _ := f.GetCellValue("s", "D2", excelize.Options{RawCellValue: true}); v != "3" {
		t.Errorf("D2 cached = %q", v)
	}
	if v, _ := f.GetCellValue("s", "D3", excelize.Options{RawCellValue: true}); v != "3" {
		t.Errorf("D3 cached = %q", v)
	}
	for _, bad := range []string{
		"HYPERLINK(\"http://x\",\"y\")", "SUM(B2:B4)+1", "SUM(Sheet2!B2:B4)", "SUM(B2:B99)", "SUM(B1:B4)", "IMPORTXML(B2)", "cmd|' /C calc'!A0",
		"SUM(B2:C4)", "SUM(B2:B4,B2)", "SUM(D2:D3)",
	} {
		d := mustParse(t, mk(map[string]any{"cell": "E2", "formula": bad}))
		if _, err := Build("xlsx", d); err == nil {
			t.Errorf("formula %q must be refused", bad)
		}
	}
	// the cell it writes to must be a plain reference inside the sheet's area
	for _, cell := range []string{"A1", "Z999", "B3", "not a cell", "E1"} {
		d := mustParse(t, mk(map[string]any{"cell": cell, "formula": "SUM(B2:B4)"}))
		if _, err := Build("xlsx", d); err == nil {
			t.Errorf("formula target %q must be refused", cell)
		}
	}
}

func TestLimits(t *testing.T) {
	many := make([]any, MaxRows+1)
	for i := range many {
		many[i] = []any{"x"}
	}
	for name, raw := range map[string]any{
		"empty":      map[string]any{},
		"rows":       map[string]any{"sheets": []any{map[string]any{"name": "a", "columns": []any{"A"}, "rows": many}}},
		"cols":       map[string]any{"sheets": []any{map[string]any{"name": "a", "rows": []any{make([]any, MaxColumns+1)}}}},
		"sheets":     map[string]any{"sheets": []any{map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}}},
		"cell":       map[string]any{"sheets": []any{map[string]any{"name": "a", "columns": []any{"A"}, "rows": []any{[]any{strings.Repeat("x", MaxCellRunes+1)}}}}},
		"block type": map[string]any{"blocks": []any{map[string]any{"type": "script", "text": "x"}}},
	} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	if _, err := Parse("not json"); err == nil {
		t.Error("a non-object document must be refused")
	}
}

func TestSheetNamesAreSanitized(t *testing.T) {
	d := mustParse(t, map[string]any{"sheets": []any{
		map[string]any{"name": "Ventas/2026: [Q1]?*", "columns": []any{"A"}, "rows": []any{[]any{"1"}}},
		map[string]any{"name": "Ventas/2026: [Q1]?*", "columns": []any{"A"}, "rows": []any{[]any{"1"}}},
		map[string]any{"name": strings.Repeat("n", 60), "columns": []any{"A"}, "rows": []any{[]any{"1"}}},
		map[string]any{"columns": []any{"A"}, "rows": []any{[]any{"1"}}},
	}})
	b, err := Build("xlsx", d)
	if err != nil {
		t.Fatal(err)
	}
	f := openX(t, b)
	got := f.GetSheetList()
	if len(got) != 4 {
		t.Fatalf("sheets = %v", got)
	}
	seen := map[string]bool{}
	for _, n := range got {
		if len(n) > 31 || strings.ContainsAny(n, `[]:*?/\`) || seen[strings.ToLower(n)] {
			t.Errorf("bad sheet name %q in %v", n, got)
		}
		seen[strings.ToLower(n)] = true
	}
}

func TestDocxIsWellFormedAndHasContent(t *testing.T) {
	d := mustParse(t, map[string]any{"title": "Informe <Q1> & más", "blocks": []any{
		map[string]any{"type": "heading", "level": 1, "text": "Resumen"},
		map[string]any{"type": "paragraph", "text": "Línea uno\nLínea dos con \x00 control"},
		map[string]any{"type": "bullets", "items": []any{"uno", "dos & tres"}},
		map[string]any{"type": "table", "columns": []any{"Nombre", map[string]any{"header": "Monto", "type": "currency"}}, "rows": []any{[]any{"a", 1234.5}, []any{"b", "7"}}},
	}})
	b, err := Build("docx", d)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var doc string
	for _, zf := range zr.File {
		rc, _ := zf.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		dec := xml.NewDecoder(bytes.NewReader(data))
		for {
			if _, err := dec.Token(); err != nil {
				if err != io.EOF {
					t.Errorf("%s is not well-formed XML: %v", zf.Name, err)
				}
				break
			}
		}
		if zf.Name == "word/document.xml" {
			doc = string(data)
		}
	}
	for _, want := range []string{"Informe &lt;Q1&gt; &amp; más", "Resumen", "Línea dos con  control", "dos &amp; tres", "$1,234.50", "Heading1", "<w:tbl>"} {
		if !strings.Contains(doc, want) {
			t.Errorf("document.xml misses %q", want)
		}
	}
	if strings.ContainsRune(doc, 0) {
		t.Error("NUL in the document")
	}
}

func TestPDFAndMarkdownAndCSV(t *testing.T) {
	d := mustParse(t, products())
	p, err := Build("pdf", d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(p, []byte("%PDF-")) || !bytes.Contains(p, []byte("%%EOF")) || len(p) < 800 {
		t.Fatalf("not a pdf (%d bytes)", len(p))
	}
	// a long text document paginates
	var blocks []any
	for i := 0; i < 80; i++ {
		blocks = append(blocks, map[string]any{"type": "paragraph", "text": strings.Repeat("Texto con acentos áéíóú ñ. ", 30)})
	}
	long, err := Build("pdf", mustParse(t, map[string]any{"title": "Largo", "blocks": blocks}))
	if err != nil || bytes.Count(long, []byte("/Type /Page\n")) < 3 && bytes.Count(long, []byte("/Type /Page")) < 4 {
		t.Fatalf("long pdf: err=%v pages? len=%d", err, len(long))
	}
	md, _ := Build("md", d)
	if !strings.Contains(string(md), "| Producto | Precio | Stock | Margen | Alta |") || !strings.Contains(string(md), "$1.50") {
		t.Errorf("markdown:\n%s", md)
	}
	cs, err := Build("csv", d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cs), "'=HYPERLINK") {
		t.Errorf("csv must neutralize a formula-like text:\n%s", cs)
	}
	// sheets render as word, text-only renders as a one-column sheet
	if _, err := Build("docx", d); err != nil {
		t.Error(err)
	}
	x, err := Build("xlsx", mustParse(t, map[string]any{"blocks": []any{map[string]any{"type": "paragraph", "text": "a\nb"}}}))
	if err != nil {
		t.Fatal(err)
	}
	if rows, _ := openX(t, x).GetRows("Document"); len(rows) != 3 {
		t.Errorf("text-only xlsx rows = %v", rows)
	}
}

func TestParseNumber(t *testing.T) {
	for in, want := range map[string]float64{"1,234.50": 1234.5, "1.234,50": 1234.5, "12,5": 12.5, "$3,25": 3.25, "1,234": 1234, "1.234.567": 1234567, "(5)": -5, " 7 ": 7, "-3.5": -3.5} {
		got, _, ok := parseNumber(in)
		if !ok || got != want {
			t.Errorf("parseNumber(%q) = %v %v, want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "abc", "1e5x", "=1+1", "NaN"} {
		if _, _, ok := parseNumber(in); ok {
			t.Errorf("parseNumber(%q) must fail", in)
		}
	}
}

func TestFormatOfName(t *testing.T) {
	for in, want := range map[string]string{"a.XLSX": "xlsx", "a.docx": "docx", "a.pdf": "pdf", "a.md": "md", "a.csv": "csv", "a.txt": ""} {
		if FormatOfName(in) != want {
			t.Errorf("FormatOfName(%q)", in)
		}
	}
}
