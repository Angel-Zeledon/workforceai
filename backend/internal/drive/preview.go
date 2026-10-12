package drive

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"path"
	"strings"
	"unicode"

	"github.com/xuri/excelize/v2"
)

// Preview is a bounded, display-ready JSON form of an office file for the
// viewer: worksheets as rows of text (formatted like the file shows them), or a
// word document as blocks. The server reads the file (with the same zip-bomb
// guards as text extraction) so the browser never parses an untrusted office
// package, and nothing is evaluated: formulas show their cached values.
type Preview struct {
	// Kind is "sheets" | "doc" | "none".
	Kind   string         `json:"kind"`
	Sheets []PreviewSheet `json:"sheets,omitempty"`
	Blocks []PreviewBlock `json:"blocks,omitempty"`
	// Truncated: rows, columns, sheets or blocks were cut at the safety limits.
	Truncated bool   `json:"truncated"`
	Note      string `json:"note,omitempty"`
}

// PreviewSheet is one worksheet; Rows[0] is the first row of the sheet.
type PreviewSheet struct {
	Name      string     `json:"name"`
	Rows      [][]string `json:"rows"`
	Truncated bool       `json:"truncated,omitempty"`
}

// PreviewBlock is one piece of a word document.
type PreviewBlock struct {
	Type  string     `json:"type"` // heading | paragraph | bullets | table
	Level int        `json:"level,omitempty"`
	Text  string     `json:"text,omitempty"`
	Items []string   `json:"items,omitempty"`
	Rows  [][]string `json:"rows,omitempty"`
}

// Preview bounds.
const (
	previewMaxSheets    = 12
	previewMaxRows      = 500
	previewMaxCols      = 40
	previewMaxCellRunes = 400
	previewMaxBlocks    = 1500
	previewMaxTableRows = 300
)

func previewText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// BuildPreview reads the bytes of a file named name. It never fails: a file it
// cannot read safely previews as Kind "none" with a note.
func BuildPreview(name string, data []byte) Preview {
	switch strings.ToLower(path.Ext(name)) {
	case ".xlsx":
		return previewXLSX(data)
	case ".docx":
		return previewDOCX(data)
	}
	return Preview{Kind: "none", Note: "no preview for this type"}
}

func zipOK(data []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(zr.File) > maxDocxEntries {
		return false
	}
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if f.UncompressedSize64 > maxDocxPartBytes {
			return false
		}
	}
	return total <= maxDocxTotal
}

func previewXLSX(data []byte) Preview {
	if len(data) > MaxExtractBytes || !zipOK(data) {
		return Preview{Kind: "none", Note: "spreadsheet too large or malformed to preview"}
	}
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{UnzipSizeLimit: maxDocxTotal, UnzipXMLSizeLimit: maxDocxPartBytes})
	if err != nil {
		return Preview{Kind: "none", Note: "spreadsheet could not be read safely"}
	}
	defer f.Close()
	p := Preview{Kind: "sheets"}
	for i, name := range f.GetSheetList() {
		if i >= previewMaxSheets {
			p.Truncated = true
			break
		}
		sh := PreviewSheet{Name: previewText(name, 60), Rows: [][]string{}}
		it, err := f.Rows(name)
		if err != nil {
			p.Sheets = append(p.Sheets, sh)
			continue
		}
		for it.Next() {
			if len(sh.Rows) >= previewMaxRows {
				sh.Truncated = true
				break
			}
			cols, err := it.Columns()
			if err != nil {
				break
			}
			if len(cols) > previewMaxCols {
				cols, sh.Truncated = cols[:previewMaxCols], true
			}
			row := make([]string, len(cols))
			for ci, c := range cols {
				row[ci] = previewText(c, previewMaxCellRunes)
			}
			sh.Rows = append(sh.Rows, row)
		}
		_ = it.Close()
		for len(sh.Rows) > 0 && len(sh.Rows[len(sh.Rows)-1]) == 0 { // trailing blank rows
			sh.Rows = sh.Rows[:len(sh.Rows)-1]
		}
		p.Truncated = p.Truncated || sh.Truncated
		p.Sheets = append(p.Sheets, sh)
	}
	if len(p.Sheets) == 0 {
		return Preview{Kind: "none", Note: "the workbook has no sheets"}
	}
	return p
}

func attr(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

func previewDOCX(data []byte) Preview {
	if len(data) > MaxExtractBytes || !zipOK(data) {
		return Preview{Kind: "none", Note: "document too large or malformed to preview"}
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Preview{Kind: "none", Note: "document could not be read safely"}
	}
	var doc *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			doc = f
		}
	}
	if doc == nil {
		return Preview{Kind: "none", Note: "document could not be read safely"}
	}
	rc, err := doc.Open()
	if err != nil {
		return Preview{Kind: "none", Note: "document could not be read safely"}
	}
	defer rc.Close()
	d := xml.NewDecoder(io.LimitReader(rc, maxDocxPartBytes+1))
	d.Strict = false

	p := Preview{Kind: "doc"}
	var (
		para     strings.Builder
		style    string
		listItem bool
		inT      bool
		tblDepth int
		rows     [][]string
		row      []string
		cell     strings.Builder
		pending  []string // consecutive list items
	)
	flushList := func() {
		if len(pending) > 0 {
			p.Blocks = append(p.Blocks, PreviewBlock{Type: "bullets", Items: pending})
			pending = nil
		}
	}
	add := func(b PreviewBlock) {
		if len(p.Blocks) >= previewMaxBlocks {
			p.Truncated = true
			return
		}
		p.Blocks = append(p.Blocks, b)
	}
	endParagraph := func() {
		txt := strings.TrimSpace(para.String())
		para.Reset()
		defer func() { style, listItem = "", false }()
		if tblDepth > 0 {
			if cell.Len() > 0 {
				cell.WriteByte('\n')
			}
			cell.WriteString(txt)
			return
		}
		if txt == "" {
			return
		}
		s := strings.ToLower(style)
		switch {
		case listItem || strings.HasPrefix(s, "list"):
			if len(pending) < 400 {
				pending = append(pending, previewText(txt, 1000))
			}
			return
		}
		flushList()
		switch {
		case s == "title":
			add(PreviewBlock{Type: "heading", Level: 1, Text: previewText(txt, 300)})
		case strings.HasPrefix(s, "heading"):
			lvl := 1
			if len(s) > 7 && s[7] >= '1' && s[7] <= '3' {
				lvl = int(s[7] - '0')
			} else if len(s) > 7 && s[7] > '3' && s[7] <= '9' {
				lvl = 3
			}
			add(PreviewBlock{Type: "heading", Level: lvl, Text: previewText(txt, 300)})
		default:
			add(PreviewBlock{Type: "paragraph", Text: previewText(txt, 4000)})
		}
	}
loop:
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				para.Reset()
			case "pStyle":
				style = attr(t, "val")
			case "numPr":
				listItem = true
			case "t":
				inT = true
			case "tab":
				para.WriteByte(' ')
			case "br", "cr":
				para.WriteByte('\n')
			case "tbl":
				if tblDepth == 0 {
					flushList()
					rows = nil
				}
				tblDepth++
			case "tr":
				row = nil
			case "tc":
				cell.Reset()
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "p":
				endParagraph()
			case "tc":
				if tblDepth == 1 && len(row) < previewMaxCols {
					row = append(row, previewText(strings.TrimSpace(cell.String()), previewMaxCellRunes))
				}
			case "tr":
				if tblDepth == 1 {
					if len(rows) < previewMaxTableRows {
						rows = append(rows, row)
					} else {
						p.Truncated = true
					}
				}
			case "tbl":
				tblDepth--
				if tblDepth == 0 && len(rows) > 0 {
					add(PreviewBlock{Type: "table", Rows: rows})
					rows = nil
				}
			}
		case xml.CharData:
			if inT {
				para.Write(t)
			}
		}
		if len(p.Blocks) >= previewMaxBlocks {
			p.Truncated = true
			break loop
		}
	}
	flushList()
	if len(p.Blocks) == 0 {
		return Preview{Kind: "none", Note: "the document has no readable text"}
	}
	return p
}

// PreviewOf previews a version (0 = current) of an office file.
func (s *Service) PreviewOf(ctx context.Context, f File, version int) (Preview, Version, error) {
	rc, v, err := s.Open(ctx, f, version)
	if err != nil {
		return Preview{}, Version{}, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, MaxExtractBytes+1))
	if err != nil {
		return Preview{}, Version{}, err
	}
	return BuildPreview(f.Name, data), v, nil
}
