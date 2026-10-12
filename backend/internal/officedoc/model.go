// Package officedoc builds real office documents (xlsx, docx, pdf, plus md and
// csv renderings) from one small structured model. Agents describe the
// document as data; the backend writes the bytes. Nothing here executes
// anything: formulas are limited to SUM and AVERAGE over the sheet's own data,
// are validated, and carry a computed cached value; every other cell that comes
// from model text is written as a plain string, never as a formula.
package officedoc

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits keep a document bounded no matter what the model asks for.
const (
	MaxSheets       = 8
	MaxRows         = 5000
	MaxColumns      = 30
	MaxCells        = 60000
	MaxCellRunes    = 2000
	MaxBlocks       = 600
	MaxTextRunes    = 20000 // one paragraph
	MaxListItems    = 200
	MaxTotalRunes   = 400000 // all text of a word/pdf document
	MaxTitleRunes   = 200
	MaxSheetNameLen = 31
)

// Formats that Build can produce.
const (
	FormatXLSX = "xlsx"
	FormatDOCX = "docx"
	FormatPDF  = "pdf"
	FormatMD   = "md"
	FormatCSV  = "csv"
)

// Column types.
const (
	TypeText     = "text"
	TypeNumber   = "number"
	TypeCurrency = "currency"
	TypePercent  = "percent"
	TypeDate     = "date"
)

// Error is a refusal with a stable code and a short English message that the
// agent can read to correct the document.
type Error struct{ Code, Msg string }

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

func bad(format string, a ...any) *Error {
	return &Error{Code: "invalid_args", Msg: fmt.Sprintf(format, a...)}
}

// Column describes a table column. In JSON it may also be a bare string (the header).
type Column struct {
	Header string  `json:"header"`
	Type   string  `json:"type,omitempty"`   // text | number | currency | percent | date
	Width  float64 `json:"width,omitempty"`  // characters (xlsx); default: fitted to the content
	Format string  `json:"format,omitempty"` // optional xlsx number format (restricted charset)
	// Currency symbol or code for type currency (default "$").
	Currency string `json:"currency,omitempty"`
}

// UnmarshalJSON accepts {"header":...} or just "Header".
func (c *Column) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*c = Column{Header: s}
		return nil
	}
	type plain Column
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*c = Column(p)
	return nil
}

// FormulaCell is a declared formula: Formula is "SUM(B2:B11)" or "AVERAGE(B2:B11)".
type FormulaCell struct {
	Cell    string `json:"cell"`
	Formula string `json:"formula"`
}

// Totals asks for a totals row under the data.
type Totals struct {
	Label   string   `json:"label,omitempty"`
	Func    string   `json:"func,omitempty"`    // sum (default) | average
	Columns []string `json:"columns,omitempty"` // headers; default: every number and currency column
}

// Sheet is one worksheet (or one table of a word/pdf document).
type Sheet struct {
	Name         string        `json:"name"`
	Columns      []Column      `json:"columns"`
	Rows         [][]any       `json:"rows"`
	Formulas     []FormulaCell `json:"formulas,omitempty"`
	FreezeHeader *bool         `json:"freeze_header,omitempty"`
	Totals       *Totals       `json:"totals,omitempty"`
}

// Block is one piece of a word/pdf document.
type Block struct {
	Type    string   `json:"type"` // heading | paragraph | bullets | table
	Level   int      `json:"level,omitempty"`
	Text    string   `json:"text,omitempty"`
	Items   []string `json:"items,omitempty"`
	Columns []Column `json:"columns,omitempty"`
	Rows    [][]any  `json:"rows,omitempty"`
}

// Doc is the document model shared by every format.
type Doc struct {
	Title  string  `json:"title,omitempty"`
	Sheets []Sheet `json:"sheets,omitempty"`
	Blocks []Block `json:"blocks,omitempty"`
}

// Parse reads the model from the JSON-decoded value of the tool argument.
func Parse(raw any) (Doc, error) {
	var d Doc
	switch v := raw.(type) {
	case nil:
		return d, bad("document is required")
	case string:
		if err := json.Unmarshal([]byte(v), &d); err != nil {
			return d, bad("document must be an object")
		}
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return d, bad("document must be an object")
		}
		if err := json.Unmarshal(b, &d); err != nil {
			return d, bad("document is malformed: %s", shorten(err.Error(), 160))
		}
	}
	if err := d.normalize(); err != nil {
		return d, err
	}
	return d, nil
}

func shorten(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// cleanText removes control characters and invalid UTF-8 (NUL, escape codes)
// that would corrupt an XML part; it keeps \n and \t.
func cleanText(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == '\r':
			return -1
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == 0xFFFE, r == 0xFFFF, r == 0x2028, r == 0x2029:
			return -1
		}
		return r
	}, s)
}

func oneLine(s string, max int) string {
	return shorten(strings.Join(strings.Fields(cleanText(s)), " "), max)
}

// normalize cleans text, fills defaults and enforces every limit.
func (d *Doc) normalize() error {
	d.Title = oneLine(d.Title, MaxTitleRunes)
	if len(d.Sheets) == 0 && len(d.Blocks) == 0 {
		return bad("the document needs sheets (spreadsheet) or blocks (text document)")
	}
	if len(d.Sheets) > MaxSheets {
		return bad("at most %d sheets", MaxSheets)
	}
	if len(d.Blocks) > MaxBlocks {
		return bad("at most %d blocks", MaxBlocks)
	}
	cells := 0
	for i := range d.Sheets {
		s := &d.Sheets[i]
		n, err := normalizeTable(&s.Columns, &s.Rows, fmt.Sprintf("sheet %d", i+1))
		if err != nil {
			return err
		}
		cells += n
		s.Name = oneLine(s.Name, 80)
	}
	total := utf8.RuneCountInString(d.Title)
	for i := range d.Blocks {
		b := &d.Blocks[i]
		b.Type = strings.ToLower(strings.TrimSpace(b.Type))
		switch b.Type {
		case "heading":
			b.Text = oneLine(b.Text, 300)
			if b.Level < 1 {
				b.Level = 1
			}
			if b.Level > 3 {
				b.Level = 3
			}
		case "paragraph", "text", "p":
			b.Type = "paragraph"
			b.Text = shorten(cleanText(b.Text), MaxTextRunes)
		case "bullets", "list", "bullet":
			b.Type = "bullets"
			if len(b.Items) > MaxListItems {
				return bad("at most %d items in a list", MaxListItems)
			}
			for j := range b.Items {
				b.Items[j] = oneLine(b.Items[j], MaxTextRunes)
				total += utf8.RuneCountInString(b.Items[j])
			}
		case "table":
			n, err := normalizeTable(&b.Columns, &b.Rows, fmt.Sprintf("table in block %d", i+1))
			if err != nil {
				return err
			}
			cells += n
		default:
			return bad("block %d: type must be heading, paragraph, bullets or table", i+1)
		}
		total += utf8.RuneCountInString(b.Text)
	}
	if cells > MaxCells {
		return bad("at most %d cells in total", MaxCells)
	}
	if total > MaxTotalRunes {
		return bad("the text is too long")
	}
	return nil
}

func normalizeTable(cols *[]Column, rows *[][]any, what string) (int, error) {
	if len(*rows) > MaxRows {
		return 0, bad("%s: at most %d rows", what, MaxRows)
	}
	width := len(*cols)
	for _, r := range *rows {
		if len(r) > width {
			width = len(r)
		}
	}
	if width > MaxColumns {
		return 0, bad("%s: at most %d columns", what, MaxColumns)
	}
	for len(*cols) < width {
		*cols = append(*cols, Column{Header: fmt.Sprintf("Column %d", len(*cols)+1)})
	}
	for i := range *cols {
		c := &(*cols)[i]
		c.Header = oneLine(c.Header, 200)
		c.Type = strings.ToLower(strings.TrimSpace(c.Type))
		switch c.Type {
		case "", TypeText, TypeNumber, TypeCurrency, TypePercent, TypeDate:
		case "string":
			c.Type = TypeText
		case "int", "integer", "float", "decimal":
			c.Type = TypeNumber
		case "money":
			c.Type = TypeCurrency
		case "pct":
			c.Type = TypePercent
		default:
			c.Type = "" // unknown: inferred from the values
		}
		if c.Width < 0 || c.Width != c.Width {
			c.Width = 0
		}
		if c.Width > 80 {
			c.Width = 80
		}
		c.Currency = oneLine(c.Currency, 3)
		if !safeNumFmt(c.Format) {
			c.Format = ""
		}
	}
	n := 0
	for ri, r := range *rows {
		for ci, v := range r {
			switch x := v.(type) {
			case nil, bool, float64:
			case string:
				x = cleanText(x)
				if utf8.RuneCountInString(x) > MaxCellRunes {
					return 0, bad("%s: a cell in row %d is longer than %d characters", what, ri+1, MaxCellRunes)
				}
				(*rows)[ri][ci] = x
			default:
				// nested values are not cells: keep a short text form
				b, _ := json.Marshal(x)
				(*rows)[ri][ci] = shorten(string(b), 200)
			}
		}
		n += len(r)
	}
	return n, nil
}

// safeNumFmt keeps custom number formats to a harmless charset.
func safeNumFmt(f string) bool {
	if f == "" || len(f) > 40 {
		return false
	}
	for _, r := range f {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case strings.ContainsRune("#,.$%€£¥ \"/-:_()", r):
		default:
			return false
		}
	}
	return true
}

// Build renders the document in the given format.
func Build(format string, d Doc) ([]byte, error) {
	switch strings.ToLower(format) {
	case FormatXLSX:
		return buildXLSX(d)
	case FormatDOCX:
		return buildDOCX(d)
	case FormatPDF:
		return buildPDF(d)
	case FormatMD:
		return []byte(renderMarkdown(d)), nil
	case FormatCSV:
		return renderCSV(d)
	}
	return nil, bad("unsupported format %q", format)
}

// FormatOfName returns the format a file name asks for ("" when it is none of ours).
func FormatOfName(name string) string {
	n := strings.ToLower(name)
	for _, f := range []string{FormatXLSX, FormatDOCX, FormatPDF, FormatMD, FormatCSV} {
		if strings.HasSuffix(n, "."+f) {
			return f
		}
	}
	return ""
}
