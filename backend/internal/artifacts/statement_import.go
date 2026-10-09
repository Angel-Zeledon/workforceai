package artifacts

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"aiworkforce/backend/internal/domain"
)

// Q3: bank statement import (finance_treasury, catalog 4.5). A person uploads
// a CSV or xlsx statement; the BACKEND parses it, deterministically and with
// hard limits, into a `sheet` artifact. That artifact is then plain read-only
// context for agents (a task description can reference it as artifact:<id>,
// W3) and the runtime still sees it as untrusted delimited data.
//
// Safety:
//   - no formula is ever kept: xlsx `<f>` elements are ignored (only the cached
//     value is read) and CSV cells are text; nothing is written to `f`;
//   - text that a spreadsheet would evaluate (= + - @ tab CR) and is not a
//     number is stored with a leading apostrophe, the same rule the xlsx export
//     applies (looksLikeFormula), so a later export or CSV re-import cannot run it;
//   - agents cannot import (human-only, like the PDF upload), the organization
//     read-only mode is honoured, size, rows, columns and cell length are capped
//     and a zip bomb is refused before it is inflated;
//   - the content of a cell is data: it is never interpreted as instructions.

const (
	MaxStatementBytes = 5 << 20
	maxStatementRows  = 5000
	maxStatementCols  = 30
	maxStatementCell  = 500 // runes
	maxXLSXPartBytes  = 32 << 20
)

// StatementInput is the upload of a statement.
type StatementInput struct {
	Filename string
	Title    string
	Locale   string // es|en: decimal convention of ambiguous numbers (1,234)
	Data     []byte
	// CustomerID links the sheet to a client of an accounting firm (optional).
	CustomerID string
}

// ParsedStatement is the pure result of parsing a statement file.
type ParsedStatement struct {
	Rows      [][]any // numbers are float64, everything else string
	Truncated bool    // rows, columns or cell text were cut at a limit
	Format    string  // csv|xlsx
	SHA256    string
}

var (
	ErrStatementFormat = fmt.Errorf("%w: unsupported_statement_format: only CSV and xlsx files are accepted", domain.ErrInvalid)
	zipMagic           = []byte("PK\x03\x04")
)

// ParseStatement reads a CSV or xlsx file (decided by magic bytes, not by the
// file name) into rows of scalars.
func ParseStatement(data []byte, locale string) (ParsedStatement, error) {
	if len(data) == 0 {
		return ParsedStatement{}, invalid("empty_file: the file is empty")
	}
	if len(data) > MaxStatementBytes {
		return ParsedStatement{}, fmt.Errorf("%w: the statement exceeds %d MB", ErrTooLarge, MaxStatementBytes>>20)
	}
	sum := sha256.Sum256(data)
	var (
		raw       [][]string
		truncated bool
		format    string
		err       error
	)
	if bytes.HasPrefix(data, zipMagic) {
		format = "xlsx"
		raw, truncated, err = readXLSX(data)
	} else {
		format = "csv"
		raw, truncated, err = readCSV(data)
	}
	if err != nil {
		return ParsedStatement{}, err
	}
	if len(raw) == 0 {
		return ParsedStatement{}, invalid("empty_statement: the statement has no rows")
	}
	out := ParsedStatement{Format: format, Truncated: truncated, SHA256: hex.EncodeToString(sum[:])}
	for _, r := range raw {
		row := make([]any, len(r))
		for i, c := range r {
			v, cut := cellValue(c, locale)
			row[i] = v
			out.Truncated = out.Truncated || cut
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

func readCSV(data []byte) ([][]string, bool, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, false, ErrStatementFormat // binary data
	}
	text := strings.ToValidUTF8(string(data), "�")
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = detectDelimiter(text)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	var rows [][]string
	truncated := false
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false, invalid("invalid_csv: %v", err)
		}
		if len(rows) >= maxStatementRows {
			truncated = true
			break
		}
		if len(rec) > maxStatementCols {
			rec, truncated = rec[:maxStatementCols], true
		}
		rows = append(rows, rec)
	}
	return rows, truncated, nil
}

func detectDelimiter(text string) rune {
	line := text
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		line = text[:i]
	}
	best, bestN := ',', strings.Count(line, ",")
	for _, d := range []rune{';', '\t'} {
		if n := strings.Count(line, string(d)); n > bestN {
			best, bestN = d, n
		}
	}
	return best
}

// ---- xlsx (read only, first worksheet, cached values) ----

func readXLSX(data []byte) ([][]string, bool, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, false, ErrStatementFormat
	}
	files := map[string]*zip.File{}
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
		files[f.Name] = f
	}
	// A zip bomb is refused from the headers, before anything is inflated.
	if total > 4*maxXLSXPartBytes || len(zr.File) > 2000 {
		return nil, false, invalid("invalid_xlsx: the workbook is too large")
	}
	sheetFile := firstWorksheet(files)
	if sheetFile == nil {
		return nil, false, ErrStatementFormat
	}
	var shared []string
	if f := files["xl/sharedStrings.xml"]; f != nil {
		b, err := readPart(f)
		if err != nil {
			return nil, false, err
		}
		shared = parseSharedStrings(b)
	}
	b, err := readPart(sheetFile)
	if err != nil {
		return nil, false, err
	}
	return parseSheetXML(b, shared)
}

func firstWorksheet(files map[string]*zip.File) *zip.File {
	var names []string
	for n := range files {
		if strings.HasPrefix(n, "xl/worksheets/") && strings.HasSuffix(n, ".xml") && !strings.Contains(strings.TrimPrefix(n, "xl/worksheets/"), "/") {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Slice(names, func(i, j int) bool { // sheet2 before sheet10
		a, b := numSuffix(names[i]), numSuffix(names[j])
		if a != b {
			return a < b
		}
		return names[i] < names[j]
	})
	return files[names[0]]
}

var digitsRe = regexp.MustCompile(`[0-9]+`)

func numSuffix(n string) int {
	m := digitsRe.FindString(path.Base(n))
	v, _ := strconv.Atoi(m)
	return v
}

func readPart(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > maxXLSXPartBytes {
		return nil, invalid("invalid_xlsx: a part of the workbook is too large")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, invalid("invalid_xlsx: %v", err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxXLSXPartBytes+1))
	if err != nil || len(b) > maxXLSXPartBytes {
		return nil, invalid("invalid_xlsx: unreadable or oversized part")
	}
	return b, nil
}

func parseSharedStrings(b []byte) []string {
	var out []string
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	var cur strings.Builder
	inSI, inT := false, false
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "t":
				inT = inSI
			}
		case xml.CharData:
			if inT {
				cur.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "si":
				inSI = false
				out = append(out, cur.String())
				if len(out) > maxStatementRows*maxStatementCols {
					return out
				}
			}
		}
	}
	return out
}

func colIndex(ref string) int {
	n := 0
	for _, c := range ref {
		if c < 'A' || c > 'Z' {
			break
		}
		n = n*26 + int(c-'A'+1)
	}
	return n - 1
}

func parseSheetXML(b []byte, shared []string) ([][]string, bool, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	var rows [][]string
	truncated := false
	var (
		cur      []string
		rowNo    int
		cellType string
		col      int
		inV, inT bool
		val      strings.Builder
		inCell   bool
	)
	flushCell := func() {
		s := val.String()
		if cellType == "s" {
			idx, err := strconv.Atoi(strings.TrimSpace(s))
			if err == nil && idx >= 0 && idx < len(shared) {
				s = shared[idx]
			} else {
				s = ""
			}
		}
		if col >= maxStatementCols {
			truncated = true
			return
		}
		for len(cur) <= col {
			cur = append(cur, "")
		}
		cur[col] = s
	}
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				cur = nil
				rowNo++
			case "c":
				inCell, cellType, val = true, "", strings.Builder{}
				col = len(cur)
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "t":
						cellType = a.Value
					case "r":
						if i := colIndex(a.Value); i >= 0 {
							col = i
						}
					}
				}
			case "v":
				inV = inCell
			case "t":
				inT = inCell && cellType == "inlineStr"
			case "f":
				// formulas are never read: only the cached value counts
			}
		case xml.CharData:
			if inV || inT {
				val.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "t":
				inT = false
			case "c":
				if inCell {
					flushCell()
				}
				inCell = false
			case "row":
				if len(rows) >= maxStatementRows {
					truncated = true
					return rows, truncated, nil
				}
				rows = append(rows, cur)
			}
		}
	}
	// trailing empty rows carry no data
	for len(rows) > 0 && isBlankRow(rows[len(rows)-1]) {
		rows = rows[:len(rows)-1]
	}
	return rows, truncated, nil
}

func isBlankRow(r []string) bool {
	for _, c := range r {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// ---- cells ----

var (
	plainNumRe = regexp.MustCompile(`^[+-]?[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)
	groupedRe  = regexp.MustCompile(`^[+-]?[0-9]{1,3}([.,' ][0-9]{3})+([.,][0-9]+)?$`)
	decimalRe  = regexp.MustCompile(`^[+-]?[0-9]+[.,][0-9]+$`)
)

// cellValue turns one cell into a number or a safe text. cut reports a truncated text.
func cellValue(s, locale string) (any, bool) {
	s = strings.TrimSpace(strings.ToValidUTF8(s, "�"))
	if s == "" {
		return "", false
	}
	if f, ok := parseAmount(s, locale); ok {
		return f, false
	}
	cut := false
	if utf8.RuneCountInString(s) > maxStatementCell {
		s, cut = string([]rune(s)[:maxStatementCell]), true
	}
	if looksLikeFormula(s) {
		s = "'" + s
	}
	return s, cut
}

// parseAmount recognizes amounts like 1234.50, -1.234,56, (1,234.50) and
// $ 1 234,50. Text with leading zeros ("00123", an account number) stays text.
func parseAmount(s, locale string) (float64, bool) {
	neg := false
	t := s
	if strings.HasPrefix(t, "(") && strings.HasSuffix(t, ")") {
		neg, t = true, strings.TrimSpace(t[1:len(t)-1])
	}
	t = strings.TrimSpace(strings.TrimLeft(t, "$€£"))
	if !strings.ContainsAny(t, "0123456789") {
		return 0, false
	}
	// A '-' glued to a currency symbol ("-$5") is not worth the ambiguity: only plain forms.
	body := strings.TrimLeft(t, "+-")
	if len(body) > 1 && body[0] == '0' && body[1] >= '0' && body[1] <= '9' {
		return 0, false // leading zeros: an identifier
	}
	esLocale := locale != "en"
	switch {
	case plainNumRe.MatchString(t):
		// ok as is
	case groupedRe.MatchString(t):
		t = normalizeGrouped(t, esLocale)
	case decimalRe.MatchString(t):
		t = strings.Replace(t, ",", ".", 1)
	default:
		return 0, false
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	if neg {
		f = -f
	}
	return f, true
}

// normalizeGrouped removes thousands separators; when both '.' and ',' appear
// the last one is the decimal mark, otherwise the locale decides.
func normalizeGrouped(t string, es bool) string {
	t = strings.NewReplacer(" ", "", "'", "").Replace(t)
	lastDot, lastComma := strings.LastIndex(t, "."), strings.LastIndex(t, ",")
	dec := byte(0)
	switch {
	case lastDot >= 0 && lastComma >= 0:
		if lastDot > lastComma {
			dec = '.'
		} else {
			dec = ','
		}
	case lastDot >= 0 && strings.Count(t, ".") == 1 && !es:
		dec = '.'
	case lastComma >= 0 && strings.Count(t, ",") == 1 && es:
		dec = ','
	}
	var b strings.Builder
	for i := 0; i < len(t); i++ {
		c := t[i]
		switch {
		case c == '.' || c == ',':
			if c == dec {
				b.WriteByte('.')
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// ---- artifact ----

// statementContent builds the aiw.sheet/1 content of a parsed statement.
func statementContent(p ParsedStatement, filename, name string) map[string]any {
	cells := map[string]any{}
	cols := 0
	for r, row := range p.Rows {
		for c, v := range row {
			if s, ok := v.(string); ok && s == "" {
				continue
			}
			cells[colName(c+1)+strconv.Itoa(r+1)] = map[string]any{"v": v}
			if c+1 > cols {
				cols = c + 1
			}
		}
	}
	return map[string]any{
		"schema": "aiw.sheet/1",
		"sheets": []any{map[string]any{"id": "s1", "name": name, "rows": len(p.Rows) + 10, "cols": max(cols, 8), "cells": cells}},
		"import": map[string]any{"kind": "bank_statement", "source": statementFilename(filename), "format": p.Format, "sha256": p.SHA256,
			"rows": len(p.Rows), "truncated": p.Truncated, "formulas_ignored": true},
	}
}

// ImportStatement parses an uploaded statement and stores it as a new sheet
// artifact. Humans only; the organization read-only mode applies.
func (s *Service) ImportStatement(ctx context.Context, actor Actor, in StatementInput) (Artifact, error) {
	if actor.IsAgent() {
		return Artifact{}, forbidden("agents cannot import files")
	}
	if s.cfg.WriteGate != nil {
		if err := s.cfg.WriteGate(ctx, s.org(ctx)); err != nil {
			return Artifact{}, err
		}
	}
	p, err := ParseStatement(in.Data, in.Locale)
	if err != nil {
		return Artifact{}, err
	}
	locale := in.Locale
	if locale != "en" {
		locale = "es"
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = L(locale, "Extracto bancario", "Bank statement") + " - " + statementFilename(in.Filename)
	}
	raw, err := json.Marshal(statementContent(p, in.Filename, L(locale, "Extracto", "Statement")))
	if err != nil {
		return Artifact{}, err
	}
	a, err := s.Create(ctx, actor, CreateInput{Kind: KindSheet, Title: title, Content: raw, Locale: locale, CustomerID: in.CustomerID})
	if err != nil {
		return Artifact{}, err
	}
	s.audit(ctx, actor, "artifact.statement_imported", a.Meta.ID, map[string]any{"format": p.Format, "sha256": p.SHA256, "rows": len(p.Rows),
		"truncated": p.Truncated, "filename": statementFilename(in.Filename)})
	return a, nil
}

// statementFilename is the base name of the upload, without the ".pdf" that
// cleanFilename appends for PDF blobs.
func statementFilename(s string) string {
	n := cleanFilename(s)
	if !strings.HasSuffix(strings.ToLower(strings.TrimSpace(s)), ".pdf") {
		n = strings.TrimSuffix(n, ".pdf")
	}
	return n
}
