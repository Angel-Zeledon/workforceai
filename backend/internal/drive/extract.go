package drive

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"aiworkforce/backend/internal/artifacts"
)

// Extraction bounds.
const (
	// MaxExtractBytes: files bigger than this are not read for extraction (text
	// files are cut here, xlsx/docx are refused: parsing them needs the whole file).
	MaxExtractBytes = 8 << 20
	// MaxIndexChars bounds the text kept for full-text search per file.
	MaxIndexChars = 100_000

	maxDocxPartBytes = 32 << 20
	maxDocxTotal     = 200 << 20
	maxDocxEntries   = 5000
)

// Extracted is the readable text of a file.
type Extracted struct {
	Text string `json:"text"`
	// Kind is how the text was obtained: text | table | docx | none.
	Kind string `json:"kind"`
	// Extracted is false for binary files whose text cannot be read.
	Extracted bool `json:"extracted"`
	// Truncated: the text was cut at the limit (or the source was cut).
	Truncated bool `json:"truncated"`
	// TotalChars is the length of the full text when known (0 otherwise).
	TotalChars int `json:"total_chars,omitempty"`
	// Note explains limits in plain English (not user-facing copy).
	Note string `json:"note,omitempty"`
	// PDF (additive): declared page count, scanned (empty or very thin text
	// layer), pages without text, and the stable code of a failed extraction
	// (pdf_too_large | pdf_encrypted | pdf_invalid | pdf_timeout | pdf_busy).
	Pages      int    `json:"pages,omitempty"`
	Scanned    bool   `json:"scanned,omitempty"`
	ImagePages []int  `json:"image_pages,omitempty"`
	Code       string `json:"code,omitempty"`
}

// Extract turns file bytes into bounded text for reading and indexing. It
// never evaluates anything: spreadsheets give their cached values, a formula
// is never executed, and the result is DATA (callers delimit it before it can
// reach a model). maxChars <= 0 means MaxIndexChars.
func Extract(name string, data []byte, maxChars int) Extracted {
	if maxChars <= 0 {
		maxChars = MaxIndexChars
	}
	t, ok := TypeOf(name)
	if !ok {
		return Extracted{Kind: "none", Note: "unsupported type: not extracted"}
	}
	switch t.extract {
	case "text":
		return extractText(data, maxChars)
	case "table":
		return extractXLSX(data, maxChars)
	case "docx":
		return extractDOCX(data, maxChars)
	}
	if strings.EqualFold(t.mime, "application/pdf") {
		return extractPDF(data, maxChars, nil)
	}
	return Extracted{Kind: "none", Note: "binary file: not extracted"}
}

func cut(s string, maxChars int) (string, bool, int) {
	n := utf8.RuneCountInString(s)
	if n <= maxChars {
		return s, false, n
	}
	i := 0
	for c := 0; c < maxChars; c++ {
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
	}
	return s[:i], true, n
}

func extractText(data []byte, maxChars int) Extracted {
	srcCut := false
	if len(data) > MaxExtractBytes {
		data, srcCut = data[:MaxExtractBytes], true
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	s := strings.ToValidUTF8(string(data), "�")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\x00", "")
	out, tr, n := cut(s, maxChars)
	e := Extracted{Text: out, Kind: "text", Extracted: true, Truncated: tr || srcCut, TotalChars: n}
	if srcCut {
		e.TotalChars = 0
		e.Note = "file larger than the extraction limit: only the first part was read"
	}
	return e
}

func extractXLSX(data []byte, maxChars int) Extracted {
	if len(data) > artifacts.MaxStatementBytes {
		return Extracted{Kind: "table", Note: "spreadsheet too large to extract"}
	}
	// Same guards as the statement import (Q3): zip-bomb refusal from the
	// headers, bounded parts, bounded rows and columns, cached values only.
	ps, err := artifacts.ParseStatement(data, "en")
	if err != nil {
		return Extracted{Kind: "table", Note: "spreadsheet could not be read safely"}
	}
	var b strings.Builder
	for _, row := range ps.Rows {
		for i, c := range row {
			if i > 0 {
				b.WriteByte('\t')
			}
			switch v := c.(type) {
			case float64:
				b.WriteString(strconv.FormatFloat(v, 'f', -1, 64))
			case string:
				b.WriteString(strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(v))
			}
		}
		b.WriteByte('\n')
		if b.Len() > maxChars*4 {
			break
		}
	}
	out, tr, n := cut(b.String(), maxChars)
	e := Extracted{Text: out, Kind: "table", Extracted: true, Truncated: tr || ps.Truncated, TotalChars: n,
		Note: "first worksheet, cached values (formulas are not evaluated), tab separated"}
	if ps.Truncated {
		e.Note += "; rows, columns or cells were cut at the safety limits"
	}
	return e
}

func extractDOCX(data []byte, maxChars int) Extracted {
	if len(data) > MaxExtractBytes {
		return Extracted{Kind: "docx", Note: "document too large to extract"}
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(zr.File) > maxDocxEntries {
		return Extracted{Kind: "docx", Note: "document could not be read safely"}
	}
	var total uint64
	var doc *zip.File
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if f.Name == "word/document.xml" {
			doc = f
		}
	}
	// Zip bomb: refused from the declared sizes, and the read below is bounded anyway.
	if total > maxDocxTotal || doc == nil || doc.UncompressedSize64 > maxDocxPartBytes {
		return Extracted{Kind: "docx", Note: "document is too large or malformed: not extracted"}
	}
	rc, err := doc.Open()
	if err != nil {
		return Extracted{Kind: "docx", Note: "document could not be read safely"}
	}
	defer rc.Close()
	d := xml.NewDecoder(io.LimitReader(rc, maxDocxPartBytes+1))
	d.Strict = false
	var b strings.Builder
	inT := false
	truncated := false
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inT = true
			case "tab":
				b.WriteByte('\t')
			case "br", "cr":
				b.WriteByte('\n')
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "p":
				b.WriteByte('\n')
			}
		case xml.CharData:
			if inT {
				b.Write(t)
			}
		}
		if b.Len() > maxChars*4 {
			truncated = true
			break
		}
	}
	out, tr, n := cut(strings.TrimSpace(b.String()), maxChars)
	e := Extracted{Text: out, Kind: "docx", Extracted: true, Truncated: tr || truncated, TotalChars: n,
		Note: "body text only (no headers, footers, comments or tracked changes)"}
	if truncated {
		e.TotalChars = 0
	}
	return e
}
