package officedoc

import (
	"bytes"
	"encoding/csv"
	"strings"
)

// renderMarkdown is the text form of the model.
func renderMarkdown(d Doc) string {
	items, err := d.items()
	if err != nil {
		return ""
	}
	var b strings.Builder
	if d.Title != "" {
		b.WriteString("# " + d.Title + "\n\n")
	}
	for _, it := range items {
		switch it.kind {
		case "heading":
			b.WriteString(strings.Repeat("#", min(it.level+1, 4)) + " " + it.text + "\n\n")
		case "paragraph":
			b.WriteString(it.text + "\n\n")
		case "bullets":
			for _, x := range it.list {
				b.WriteString("- " + x + "\n")
			}
			b.WriteString("\n")
		case "table":
			t := it.tbl
			if len(t.cols) == 0 {
				continue
			}
			esc := strings.NewReplacer("|", "\\|", "\n", " ")
			hs, ds := make([]string, len(t.cols)), make([]string, len(t.cols))
			for i, c := range t.cols {
				hs[i] = esc.Replace(firstNonEmpty(c.Header, colName(i)))
				ds[i] = "---"
			}
			b.WriteString("| " + strings.Join(hs, " | ") + " |\n| " + strings.Join(ds, " | ") + " |\n")
			for ri := range t.rows {
				cs := make([]string, len(t.cols))
				for ci := range cs {
					cs[ci] = esc.Replace(t.display(ci, t.cellAt(ri, ci)))
				}
				b.WriteString("| " + strings.Join(cs, " | ") + " |\n")
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// csvSafe neutralizes spreadsheet formula injection in text for csv: a cell that
// starts with = + - @ tab or CR gets a leading apostrophe.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// renderCSV writes the first table of the document.
func renderCSV(d Doc) ([]byte, error) {
	ts, err := d.tables()
	if err != nil {
		return nil, err
	}
	t := ts[0]
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	hs := make([]string, len(t.cols))
	for i, c := range t.cols {
		hs[i] = csvSafe(firstNonEmpty(c.Header, colName(i)))
	}
	_ = w.Write(hs)
	for ri := range t.rows {
		cs := make([]string, len(t.cols))
		for ci := range cs {
			c := t.cellAt(ri, ci)
			if c.kind == 'n' { // numbers are written raw (a negative number is not an injection)
				cs[ci] = fmtPlain(c.f)
			} else {
				cs[ci] = csvSafe(t.display(ci, c))
			}
		}
		_ = w.Write(cs)
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}
