package officedoc

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// cell is a resolved value: a string, a number or a date.
type cell struct {
	kind byte // 0 empty | 's' text | 'n' number | 'd' date
	s    string
	f    float64
	t    time.Time
	// calc is true for the cached result of a declared formula.
	calc bool
	// formula is the validated formula text ("SUM(B2:B11)") for calc cells.
	formula string
}

// table is a sheet or a table block with typed columns and computed formulas.
type table struct {
	name  string
	cols  []Column // Type always resolved (text | number | currency | percent | date)
	rows  [][]cell // data rows (len == len(cols)); then the totals row when any
	data  int      // number of data rows (without totals)
	extra map[[2]int]cell
	freeze bool
	// totalsRow is the index in rows of the totals row, or -1.
	totalsRow int
}

var cellRef = regexp.MustCompile(`^([A-Z]{1,2})([1-9][0-9]{0,4})$`)
var formulaRe = regexp.MustCompile(`^(SUM|AVERAGE)\(([A-Z]{1,2}[1-9][0-9]{0,4}):([A-Z]{1,2}[1-9][0-9]{0,4})\)$`)

// colIndex converts "A" -> 0, "AB" -> 27.
func colIndex(s string) int {
	n := 0
	for _, r := range s {
		n = n*26 + int(r-'A') + 1
	}
	return n - 1
}

// colName converts 0 -> "A".
func colName(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}

func parseRef(s string) (col, row int, ok bool) {
	m := cellRef.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(s)))
	if m == nil {
		return 0, 0, false
	}
	r, _ := strconv.Atoi(m[2])
	return colIndex(m[1]), r, true
}

// parseNumber reads "1,234.50", "1.234,50", "$12", " 15 " and "15%" (percent
// reports true when the text ended in %).
func parseNumber(s string) (f float64, pct bool, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false, false
	}
	if strings.HasSuffix(s, "%") {
		pct = true
		s = strings.TrimSpace(strings.TrimSuffix(s, "%"))
	}
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, s[1:len(s)-1]
	}
	s = strings.Map(func(r rune) rune {
		switch r {
		case '$', '€', '£', '¥', ' ', ' ', '_':
			return -1
		}
		return r
	}, s)
	if s == "" {
		return 0, false, false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9') && r != '.' && r != ',' && r != '-' && r != '+' {
			return 0, false, false
		}
	}
	lc, ld := strings.LastIndex(s, ","), strings.LastIndex(s, ".")
	switch {
	case lc >= 0 && ld >= 0:
		if lc > ld { // 1.234,56
			s = strings.ReplaceAll(strings.ReplaceAll(s, ".", ""), ",", ".")
		} else { // 1,234.56
			s = strings.ReplaceAll(s, ",", "")
		}
	case lc >= 0:
		if strings.Count(s, ",") == 1 && len(s)-lc-1 != 3 { // 12,5
			s = strings.Replace(s, ",", ".", 1)
		} else { // 1,234 or 1,234,567
			s = strings.ReplaceAll(s, ",", "")
		}
	case strings.Count(s, ".") > 1: // 1.234.567
		s = strings.ReplaceAll(s, ".", "")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false, false
	}
	if neg {
		f = -f
	}
	return f, pct, true
}

var dateLayouts = []string{"2006-01-02", "2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05", "2006/01/02", "02/01/2006", "02-01-2006", "2 Jan 2006", "Jan 2, 2006"}

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil && t.Year() >= 1900 && t.Year() <= 2200 {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), true
		}
	}
	return time.Time{}, false
}

func fmtPlain(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// resolve types the columns and coerces the cells. Text that cannot be read as
// the column type stays text: nothing is dropped and nothing becomes a formula.
func resolve(name string, cols []Column, rows [][]any) table {
	t := table{name: name, cols: append([]Column(nil), cols...), data: len(rows), totalsRow: -1, freeze: true}
	for ci := range t.cols {
		c := &t.cols[ci]
		if c.Type == "" {
			c.Type = inferType(rows, ci)
		}
	}
	pctPoints := make([]bool, len(t.cols))
	for ci, c := range t.cols {
		if c.Type != TypePercent {
			continue
		}
		mx := 0.0
		for _, r := range rows {
			if ci < len(r) {
				if f, ok := r[ci].(float64); ok && math.Abs(f) > mx {
					mx = math.Abs(f)
				}
			}
		}
		pctPoints[ci] = mx > 2 // 15 means 15%, 0.15 also means 15%
	}
	for _, r := range rows {
		out := make([]cell, len(t.cols))
		for ci := range t.cols {
			var v any
			if ci < len(r) {
				v = r[ci]
			}
			out[ci] = coerce(t.cols[ci].Type, v, pctPoints[ci])
		}
		t.rows = append(t.rows, out)
	}
	return t
}

func inferType(rows [][]any, ci int) string {
	n := 0
	for _, r := range rows {
		if ci >= len(r) || r[ci] == nil {
			continue
		}
		if s, ok := r[ci].(string); ok && strings.TrimSpace(s) == "" {
			continue
		}
		if _, ok := r[ci].(float64); !ok {
			return TypeText
		}
		n++
	}
	if n > 0 {
		return TypeNumber
	}
	return TypeText
}

func coerce(typ string, v any, pctPoints bool) cell {
	switch x := v.(type) {
	case nil:
		return cell{}
	case bool:
		return cell{kind: 's', s: strconv.FormatBool(x)}
	case float64:
		switch typ {
		case TypeNumber, TypeCurrency:
			return cell{kind: 'n', f: x}
		case TypePercent:
			if pctPoints {
				x /= 100
			}
			return cell{kind: 'n', f: x}
		}
		return cell{kind: 's', s: fmtPlain(x)}
	case string:
		if strings.TrimSpace(x) == "" {
			return cell{}
		}
		switch typ {
		case TypeNumber, TypeCurrency:
			if f, pct, ok := parseNumber(x); ok && !pct {
				return cell{kind: 'n', f: f}
			}
		case TypePercent:
			if f, pct, ok := parseNumber(x); ok {
				if pct || pctPoints {
					f /= 100
				}
				return cell{kind: 'n', f: f}
			}
		case TypeDate:
			if d, ok := parseDate(x); ok {
				return cell{kind: 'd', t: d}
			}
		}
		return cell{kind: 's', s: x}
	}
	return cell{}
}

// excelSerial is the Excel date serial of a UTC date (1900 system).
func excelSerial(t time.Time) float64 {
	base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	return math.Floor(t.Sub(base).Hours() / 24)
}

// withFormulas applies the sheet's declared formulas and totals row. Every
// formula is validated (SUM / AVERAGE over one rectangle of the sheet's own
// data) and gets its value computed here, so viewers that do not calculate
// still show it.
func (t *table) withFormulas(totals *Totals, formulas []FormulaCell) error {
	if totals != nil {
		fn := strings.ToLower(strings.TrimSpace(totals.Func))
		if fn == "" || fn == "total" {
			fn = "sum"
		}
		if fn != "sum" && fn != "average" && fn != "avg" {
			return bad("totals.func must be sum or average")
		}
		want := map[string]bool{}
		for _, h := range totals.Columns {
			want[strings.ToLower(strings.TrimSpace(h))] = true
		}
		if len(t.rows) == 0 {
			return bad("totals need at least one data row")
		}
		row := make([]cell, len(t.cols))
		label := oneLine(totals.Label, 60)
		if label == "" {
			label = "Total"
		}
		labelAt := -1
		for ci, c := range t.cols {
			sel := len(want) == 0 && (c.Type == TypeNumber || c.Type == TypeCurrency)
			if len(want) > 0 {
				sel = want[strings.ToLower(c.Header)] && (c.Type == TypeNumber || c.Type == TypeCurrency || c.Type == TypePercent)
			}
			if !sel {
				if labelAt < 0 && c.Type == TypeText {
					labelAt = ci
				}
				continue
			}
			f := "SUM"
			if fn != "sum" {
				f = "AVERAGE"
			}
			rng := fmt.Sprintf("%s2:%s%d", colName(ci), colName(ci), t.data+1)
			row[ci] = t.compute(f, ci, ci, 2, t.data+1)
			row[ci].formula = f + "(" + rng + ")"
		}
		if labelAt < 0 {
			labelAt = 0
			if row[0].kind != 0 { // first column is a total too: no room for a label
				labelAt = -1
			}
		}
		if labelAt >= 0 {
			row[labelAt] = cell{kind: 's', s: label}
		}
		t.rows = append(t.rows, row)
		t.totalsRow = len(t.rows) - 1
	}
	if len(formulas) > 20 {
		return bad("at most 20 formulas per sheet")
	}
	for _, fc := range formulas {
		cc, cr, ok := parseRef(fc.Cell)
		if !ok {
			return bad("formula cell %q is not a cell reference like E12", fc.Cell)
		}
		text := strings.ToUpper(strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(fc.Formula), "="), " ", ""))
		m := formulaRe.FindStringSubmatch(text)
		if m == nil {
			return bad("formula %q is not allowed: only SUM(A2:A10) and AVERAGE(A2:A10) over this sheet's own cells", fc.Formula)
		}
		c1, r1, _ := parseRef(m[2])
		c2, r2, _ := parseRef(m[3])
		if c1 > c2 || r1 > r2 {
			c1, c2 = c2, c1
			r1, r2 = r2, r1
		}
		if r1 < 2 || r2 > t.data+1 || c2 >= len(t.cols) {
			return bad("formula %q must stay inside the data of the sheet (rows 2..%d, %d columns; row 1 is the header)", fc.Formula, t.data+1, len(t.cols))
		}
		if cr < 2 || cc > len(t.cols)+5 || cr > t.data+6 {
			return bad("formula cell %s is outside the area of the sheet", fc.Cell)
		}
		if cc >= c1 && cc <= c2 && cr >= r1 && cr <= r2 {
			return bad("formula %s refers to itself", fc.Cell)
		}
		v := t.compute(m[1], c1, c2, r1, r2)
		v.formula = text
		if t.extra == nil {
			t.extra = map[[2]int]cell{}
		}
		t.extra[[2]int{cr - 1, cc}] = v
	}
	return nil
}

// compute evaluates SUM/AVERAGE over data rows r1..r2 (sheet rows, header = 1).
func (t *table) compute(fn string, c1, c2, r1, r2 int) cell {
	sum, n := 0.0, 0
	for r := r1; r <= r2; r++ {
		for c := c1; c <= c2; c++ {
			if r-2 < t.data && r-2 < len(t.rows) && c < len(t.rows[r-2]) {
				if x := t.rows[r-2][c]; x.kind == 'n' {
					sum += x.f
					n++
				}
			}
		}
	}
	v := sum
	if fn == "AVERAGE" {
		v = 0
		if n > 0 {
			v = sum / float64(n)
		}
	}
	return cell{kind: 'n', f: v, calc: true}
}

// display is the text form of a cell for word, pdf, markdown and csv.
func (t *table) display(ci int, c cell) string {
	switch c.kind {
	case 's':
		return c.s
	case 'd':
		return c.t.Format("2006-01-02")
	case 'n':
		col := t.cols[ci]
		switch col.Type {
		case TypePercent:
			return trimZeros(strconv.FormatFloat(c.f*100, 'f', 2, 64)) + "%"
		case TypeCurrency:
			sym := col.Currency
			if sym == "" {
				sym = "$"
			}
			return sym + groupDigits(c.f, 2)
		}
		if c.f == math.Trunc(c.f) && math.Abs(c.f) < 1e15 {
			return groupDigits(c.f, 0)
		}
		return groupDigits(c.f, 2)
	}
	return ""
}

func trimZeros(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

func groupDigits(f float64, dec int) string {
	s := strconv.FormatFloat(math.Abs(f), 'f', dec, 64)
	ip, fp := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		ip, fp = s[:i], s[i:]
	}
	var b strings.Builder
	for i, r := range ip {
		if i > 0 && (len(ip)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := b.String() + fp
	if f < 0 {
		out = "-" + out
	}
	return out
}

// cellAt returns the cell at data/totals row index ri, column ci, with formula
// overrides applied.
func (t *table) cellAt(ri, ci int) cell {
	if c, ok := t.extra[[2]int{ri + 1, ci}]; ok {
		return c
	}
	return t.rows[ri][ci]
}

// tables returns every table of the document (sheets, or the table blocks; a
// text-only document becomes one single-column sheet).
func (d Doc) tables() ([]table, error) {
	var out []table
	if len(d.Sheets) > 0 {
		seen := map[string]bool{}
		for i, s := range d.Sheets {
			name := sheetName(s.Name, i, seen)
			t := resolve(name, s.Columns, s.Rows)
			if s.FreezeHeader != nil {
				t.freeze = *s.FreezeHeader
			}
			if err := t.withFormulas(s.Totals, s.Formulas); err != nil {
				return nil, err
			}
			out = append(out, t)
		}
		return out, nil
	}
	seen := map[string]bool{}
	heading := ""
	for _, b := range d.Blocks {
		switch b.Type {
		case "heading":
			heading = b.Text
		case "table":
			t := resolve(sheetName(heading, len(out), seen), b.Columns, b.Rows)
			out = append(out, t)
			heading = ""
		}
	}
	if len(out) > 0 {
		if len(out) > MaxSheets {
			out = out[:MaxSheets]
		}
		return out, nil
	}
	// text only: one column, one row per line
	var rows [][]any
	for _, b := range d.Blocks {
		switch b.Type {
		case "heading", "paragraph":
			for _, ln := range strings.Split(b.Text, "\n") {
				if strings.TrimSpace(ln) != "" {
					rows = append(rows, []any{ln})
				}
			}
		case "bullets":
			for _, it := range b.Items {
				rows = append(rows, []any{"- " + it})
			}
		}
	}
	return []table{resolve(sheetName("Document", 0, seen), []Column{{Header: firstNonEmpty(d.Title, "Text"), Type: TypeText}}, rows)}, nil
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

var badSheetChars = strings.NewReplacer("[", "(", "]", ")", ":", "-", "*", "", "?", "", "/", "-", "\\", "-")

// sheetName is a valid, unique worksheet name (max 31 characters, no []:*?/\).
func sheetName(raw string, i int, seen map[string]bool) string {
	n := strings.Trim(badSheetChars.Replace(oneLine(raw, 80)), "' ")
	if n == "" {
		n = fmt.Sprintf("Sheet%d", i+1)
	}
	n = strings.TrimSpace(shorten(n, MaxSheetNameLen))
	base := n
	for k := 2; seen[strings.ToLower(n)]; k++ {
		suffix := fmt.Sprintf(" (%d)", k)
		n = shorten(base, MaxSheetNameLen-len(suffix)) + suffix
	}
	seen[strings.ToLower(n)] = true
	return n
}

// item is one rendered piece of a word, pdf or markdown document.
type item struct {
	kind  string // heading | paragraph | bullets | table
	level int
	text  string
	list  []string
	tbl   *table
}

// items returns the document as render items (sheets become a heading and a table).
func (d Doc) items() ([]item, error) {
	var out []item
	if len(d.Blocks) > 0 {
		for _, b := range d.Blocks {
			switch b.Type {
			case "table":
				t := resolve("", b.Columns, b.Rows)
				out = append(out, item{kind: "table", tbl: &t})
			default:
				out = append(out, item{kind: b.Type, level: b.Level, text: b.Text, list: b.Items})
			}
		}
		return out, nil
	}
	ts, err := d.tables()
	if err != nil {
		return nil, err
	}
	for i := range ts {
		if len(ts) > 1 && ts[i].name != "" {
			out = append(out, item{kind: "heading", level: 2, text: ts[i].name})
		}
		out = append(out, item{kind: "table", tbl: &ts[i]})
	}
	return out, nil
}

// displayRows is the table as text rows (data, formulas and totals applied).
func (t *table) displayRows() [][]any {
	var rows [][]any
	for ri := range t.rows {
		r := make([]any, len(t.cols))
		for ci := range t.cols {
			r[ci] = t.display(ci, t.cellAt(ri, ci))
		}
		rows = append(rows, r)
	}
	return rows
}
