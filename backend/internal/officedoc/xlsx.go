package officedoc

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// buildXLSX writes a real workbook: typed cells, number formats, a styled and
// frozen header row, fitted column widths, an optional totals row and the
// declared formulas (each with its computed value cached).
//
// Cells that come from model text are always written with SetCellStr (a string
// cell). A text that starts with = + - or @ is therefore shown as that text and
// never evaluated; only the validated formulas of withFormulas are formulas.
func buildXLSX(d Doc) ([]byte, error) {
	tables, err := d.tables()
	if err != nil {
		return nil, err
	}
	f := excelize.NewFile()
	defer f.Close()
	if d.Title != "" {
		_ = f.SetDocProps(&excelize.DocProperties{Title: d.Title, Creator: "AI Workforce OS"})
	} else {
		_ = f.SetDocProps(&excelize.DocProperties{Creator: "AI Workforce OS"})
	}
	styles := &styleCache{f: f, byKey: map[string]int{}}
	for i, t := range tables {
		sh := t.name
		if i == 0 {
			if err := f.SetSheetName("Sheet1", sh); err != nil {
				return nil, bad("sheet name: %v", err)
			}
		} else if _, err := f.NewSheet(sh); err != nil {
			return nil, bad("sheet name: %v", err)
		}
		if err := writeSheet(f, styles, sh, t); err != nil {
			return nil, err
		}
	}
	f.SetActiveSheet(0)
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, bad("could not write the workbook")
	}
	return fixFormulaTypes(buf.Bytes())
}

// fixFormulaTypes drops the t="str" excelize puts on a formula cell that has a
// cached number, so the cached value is a number (viewers that do not calculate
// show it formatted, and Excel recalculates anyway). Only <c ... t="str"><f>
// is touched: text cells are shared strings and never carry an <f> element.
func fixFormulaTypes(in []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(in), int64(len(in)))
	if err != nil {
		return nil, bad("could not write the workbook")
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			return nil, bad("could not write the workbook")
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, bad("could not write the workbook")
		}
		if strings.HasPrefix(zf.Name, "xl/worksheets/") {
			data = bytes.ReplaceAll(data, []byte(`t="str"><f>`), []byte(`><f>`))
		}
		w, err := zw.Create(zf.Name)
		if err != nil {
			return nil, bad("could not write the workbook")
		}
		if _, err := w.Write(data); err != nil {
			return nil, bad("could not write the workbook")
		}
	}
	if err := zw.Close(); err != nil {
		return nil, bad("could not write the workbook")
	}
	return out.Bytes(), nil
}

type styleCache struct {
	f     *excelize.File
	byKey map[string]int
}

func (s *styleCache) get(key string, st excelize.Style) int {
	if id, ok := s.byKey[key]; ok {
		return id
	}
	id, err := s.f.NewStyle(&st)
	if err != nil {
		return 0
	}
	s.byKey[key] = id
	return id
}

func numFmtOf(c Column, integers bool) string {
	if c.Format != "" {
		return c.Format
	}
	switch c.Type {
	case TypeCurrency:
		sym := c.Currency
		if sym == "" {
			sym = "$"
		}
		sym = strings.ReplaceAll(sym, `"`, "")
		return `"` + sym + `"#,##0.00`
	case TypePercent:
		return "0.0%"
	case TypeDate:
		return "yyyy-mm-dd"
	case TypeNumber:
		if integers {
			return "#,##0"
		}
		return "#,##0.00"
	}
	return ""
}

func (s *styleCache) body(c Column, integers bool, bold bool) int {
	nf := numFmtOf(c, integers)
	key := fmt.Sprintf("b|%s|%t", nf, bold)
	st := excelize.Style{CustomNumFmt: nil}
	if nf != "" {
		st.CustomNumFmt = &nf
	}
	if c.Type == TypeText || c.Type == "" {
		st.Alignment = &excelize.Alignment{Vertical: "top", WrapText: true}
	} else {
		st.Alignment = &excelize.Alignment{Vertical: "top", Horizontal: "right"}
	}
	if bold {
		st.Font = &excelize.Font{Bold: true}
		st.Border = []excelize.Border{{Type: "top", Color: "444444", Style: 1}}
	}
	return s.get(key, st)
}

func (s *styleCache) header() int {
	return s.get("h", excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"2F5496"}, Pattern: 1},
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
		Border:    []excelize.Border{{Type: "bottom", Color: "1F3864", Style: 1}},
	})
}

func writeSheet(f *excelize.File, st *styleCache, sh string, t table) error {
	ncols := len(t.cols)
	if ncols == 0 {
		return nil
	}
	name := func(c, r int) string { n, _ := excelize.CoordinatesToCellName(c+1, r); return n }
	widths := make([]float64, ncols)
	// header
	for ci, c := range t.cols {
		h := c.Header
		if h == "" {
			h = colName(ci)
		}
		_ = f.SetCellStr(sh, name(ci, 1), h)
		widths[ci] = float64(utf8.RuneCountInString(h)) + 4
	}
	_ = f.SetCellStyle(sh, name(0, 1), name(ncols-1, 1), st.header())
	_ = f.SetRowHeight(sh, 1, 22)
	// integers: a number column whose values are all whole shows no decimals
	ints := make([]bool, ncols)
	for ci := range ints {
		ints[ci] = true
		for ri := 0; ri < t.data; ri++ {
			if c := t.rows[ri][ci]; c.kind == 'n' && c.f != math.Trunc(c.f) {
				ints[ci] = false
			}
		}
	}
	setCell := func(ci, row int, c cell, bold bool) {
		ref := name(ci, row)
		switch c.kind {
		case 's':
			_ = f.SetCellStr(sh, ref, c.s)
			if w := longestLine(c.s) + 2; w > widths[ci] {
				widths[ci] = w
			}
		case 'n':
			_ = f.SetCellFloat(sh, ref, c.f, -1, 64)
			if c.formula != "" { // after the value, so the cached result stays
				_ = f.SetCellFormula(sh, ref, c.formula)
			}
			if w := float64(len(t.display(ci, c))) + 2; w > widths[ci] {
				widths[ci] = w
			}
		case 'd':
			_ = f.SetCellValue(sh, ref, c.t)
			if widths[ci] < 12 {
				widths[ci] = 12
			}
		}
		_ = f.SetCellStyle(sh, ref, ref, st.body(t.cols[ci], ints[ci], bold))
	}
	for ri := range t.rows {
		bold := ri == t.totalsRow
		for ci := 0; ci < ncols; ci++ {
			c := t.rows[ri][ci]
			if c.kind == 0 && !bold {
				continue
			}
			setCell(ci, ri+2, c, bold)
		}
	}
	for k, c := range t.extra {
		if k[1] < ncols {
			setCell(k[1], k[0]+1, c, false)
		} else { // beyond the table: still a validated formula, plain number format
			ref := name(k[1], k[0]+1)
			_ = f.SetCellFloat(sh, ref, c.f, -1, 64)
			_ = f.SetCellFormula(sh, ref, c.formula)
		}
	}
	// widths
	for ci, c := range t.cols {
		w := c.Width
		if w == 0 {
			w = math.Min(math.Max(widths[ci], 8), 60)
		}
		col, _ := excelize.ColumnNumberToName(ci + 1)
		_ = f.SetColWidth(sh, col, col, w)
	}
	last := t.data + 1
	if t.totalsRow >= 0 {
		last++
	}
	lastCol := ncols - 1
	for k := range t.extra { // formulas placed below or beside the table
		last = max(last, k[0]+1)
		lastCol = max(lastCol, k[1])
	}
	_ = f.SetSheetDimension(sh, fmt.Sprintf("A1:%s", name(lastCol, last)))
	if t.freeze {
		_ = f.SetPanes(sh, &excelize.Panes{Freeze: true, Split: false, XSplit: 0, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft",
			Selection: []excelize.Selection{{SQRef: "A2", ActiveCell: "A2", Pane: "bottomLeft"}}})
	}
	if t.data > 0 {
		_ = f.AutoFilter(sh, fmt.Sprintf("A1:%s", name(ncols-1, t.data+1)), nil)
	}
	return nil
}

func longestLine(s string) float64 {
	best := 0
	for _, ln := range strings.Split(s, "\n") {
		if n := utf8.RuneCountInString(ln); n > best {
			best = n
		}
	}
	return float64(best)
}

