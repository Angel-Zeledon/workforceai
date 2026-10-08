package artifacts

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Export (docs/architecture/agent-workspaces.md sec. 5.5): doc -> docx and
// sheet/table -> xlsx, written as minimal OOXML packages with the standard
// library only. The export is a copy of the canonical content:
//   - formulas are written only if they still pass the formula guard and do not
//     use AIW_REF (an AI Workforce function Excel does not know); otherwise the
//     cached value is exported;
//   - text that a spreadsheet would read as a formula (= + - @, tab, CR) gets
//     the quotePrefix style, so it stays text when opened or re-saved as CSV;
//   - pending suggestions of a document become Word tracked changes (w:ins/w:del).

const (
	FormatDocx = "docx"
	FormatXlsx = "xlsx"

	mimeDocx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	mimeXlsx = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
)

// ExportFile is the result of an export: a download, never stored.
type ExportFile struct {
	Filename string
	MIME     string
	Data     []byte
	Version  int
}

// ExportFormats lists the formats each kind can be exported to.
func ExportFormats(k Kind) []string {
	switch k {
	case KindDoc:
		return []string{FormatDocx}
	case KindSheet, KindTable:
		return []string{FormatXlsx}
	}
	return nil
}

// Export renders an artifact (head or a past version) for download and audits it.
func (s *Service) Export(ctx context.Context, actor Actor, id string, version int, format string) (ExportFile, error) {
	a, err := s.Get(ctx, id, version)
	if err != nil {
		return ExportFile{}, err
	}
	format = strings.ToLower(strings.TrimSpace(format))
	ok := false
	for _, f := range ExportFormats(a.Kind) {
		ok = ok || f == format
	}
	if !ok {
		return ExportFile{}, fmt.Errorf("%w: a %s artifact cannot be exported as %q", domain.ErrInvalid, a.Kind, format)
	}
	root := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(a.Content))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return ExportFile{}, fmt.Errorf("%w: unreadable content", domain.ErrInvalid)
	}
	var data []byte
	mime := mimeXlsx
	switch a.Kind {
	case KindDoc:
		data, err = docxOf(a.Title, root, s.now())
		mime = mimeDocx
	case KindSheet:
		data, err = xlsxOf(sheetsOf(root))
	case KindTable:
		data, err = xlsxOf([]xsheet{tableSheet(a.Title, root)})
	}
	if err != nil {
		return ExportFile{}, err
	}
	s.audit(ctx, actor, "artifact.exported", id, map[string]any{"format": format, "version": a.Version, "kind": a.Kind,
		"bytes": len(data), "content_hash": contentHash(a.Content), "tainted": a.Tainted})
	return ExportFile{Filename: fileName(a.Title, a.Version, format), MIME: mime, Data: data, Version: a.Version}, nil
}

var unsafeName = regexp.MustCompile(`[^\p{L}\p{N} ._-]+`)

func fileName(title string, version int, ext string) string {
	n := strings.TrimSpace(unsafeName.ReplaceAllString(title, ""))
	if n == "" {
		n = "artifact"
	}
	if r := []rune(n); len(r) > 80 {
		n = string(r[:80])
	}
	return fmt.Sprintf("%s v%d.%s", n, version, ext)
}

// ---- package writer ----

type part struct{ name, body string }

func zipOf(parts []part) ([]byte, error) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, p := range parts {
		w, err := z.CreateHeader(&zip.FileHeader{Name: p.name, Method: zip.Deflate, Modified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(p.body)); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// esc escapes text for XML; characters XML cannot carry become U+FFFD.
func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

const xmlHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

// ---- xlsx ----

type xcell struct {
	v       any // string | float64 | bool | nil
	formula string
}

type xsheet struct {
	name  string
	cells map[[2]int]xcell // [row, col], 1-based
}

func sheetsOf(root map[string]any) []xsheet {
	var out []xsheet
	for _, s := range asList(root["sheets"]) {
		sh := asMap(s)
		xs := xsheet{name: asStr(sh["name"]), cells: map[[2]int]xcell{}}
		for key, c := range asMap(sh["cells"]) {
			if !cellKeyRe.MatchString(key) {
				continue
			}
			col, row := splitAddr(key)
			cell := asMap(c)
			xc := xcell{v: scalar(cell["v"])}
			if f := asStr(cell["f"]); f != "" && guardFormula(f) == nil && !strings.Contains(strings.ToUpper(f), "AIW_REF") {
				xc.formula = strings.TrimPrefix(f, "=")
			}
			xs.cells[[2]int{row, col}] = xc
		}
		out = append(out, xs)
	}
	return out
}

func tableSheet(title string, root map[string]any) xsheet {
	xs := xsheet{name: title, cells: map[[2]int]xcell{}}
	var keys []string
	for i, c := range asList(root["columns"]) {
		cm := asMap(c)
		label := asStr(cm["label"])
		if label == "" {
			label = asStr(cm["key"])
		}
		keys = append(keys, asStr(cm["key"]))
		xs.cells[[2]int{1, i + 1}] = xcell{v: label}
	}
	for r, row := range asList(root["rows"]) {
		cells := asMap(asMap(row)["cells"])
		for i, k := range keys {
			xs.cells[[2]int{r + 2, i + 1}] = xcell{v: scalar(cells[k])}
		}
	}
	return xs
}

// scalar reduces a content value to what a cell can hold.
func scalar(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string, bool:
		return t
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	case float64:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, x := range t {
			parts = append(parts, fmt.Sprint(scalar(x)))
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		if l := asStr(t["label"]); l != "" {
			return l
		}
		if l := asStr(t["id"]); l != "" {
			return l
		}
		b, _ := json.Marshal(t)
		return string(b)
	}
	return fmt.Sprint(v)
}

// looksLikeFormula: text a spreadsheet (or a CSV re-import) would evaluate.
func looksLikeFormula(s string) bool {
	return s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0]))
}

var badSheetChars = strings.NewReplacer("[", "(", "]", ")", ":", "-", "*", "-", "?", "", "/", "-", `\`, "-")

func sheetNames(sheets []xsheet) []string {
	seen := map[string]bool{}
	out := make([]string, len(sheets))
	for i, s := range sheets {
		n := strings.Trim(strings.TrimSpace(badSheetChars.Replace(s.name)), "'")
		if n == "" {
			n = "Sheet" + strconv.Itoa(i+1)
		}
		if r := []rune(n); len(r) > 31 {
			n = string(r[:31])
		}
		base, k := n, 2
		for seen[strings.ToLower(n)] {
			suf := " (" + strconv.Itoa(k) + ")"
			r := []rune(base)
			if len(r)+len(suf) > 31 {
				r = r[:31-len(suf)]
			}
			n, k = string(r)+suf, k+1
		}
		seen[strings.ToLower(n)] = true
		out[i] = n
	}
	return out
}

func xlsxOf(sheets []xsheet) ([]byte, error) {
	if len(sheets) == 0 {
		sheets = []xsheet{{name: "Sheet1", cells: map[[2]int]xcell{}}}
	}
	names := sheetNames(sheets)
	var ct, wb, rels strings.Builder
	ct.WriteString(xmlHead + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
		`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	wb.WriteString(xmlHead + `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	rels.WriteString(xmlHead + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	parts := []part{}
	for i, s := range sheets {
		n := strconv.Itoa(i + 1)
		ct.WriteString(`<Override PartName="/xl/worksheets/sheet` + n + `.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`)
		wb.WriteString(`<sheet name="` + esc(names[i]) + `" sheetId="` + n + `" r:id="rId` + n + `"/>`)
		rels.WriteString(`<Relationship Id="rId` + n + `" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet` + n + `.xml"/>`)
		parts = append(parts, part{"xl/worksheets/sheet" + n + ".xml", worksheetXML(s)})
	}
	ct.WriteString(`</Types>`)
	wb.WriteString(`</sheets></workbook>`)
	styles := len(sheets) + 1
	rels.WriteString(`<Relationship Id="rId` + strconv.Itoa(styles) + `" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`)
	parts = append([]part{
		{"[Content_Types].xml", ct.String()},
		{"_rels/.rels", xmlHead + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", wb.String()},
		{"xl/_rels/workbook.xml.rels", rels.String()},
		// Style 1 = quotePrefix: the cell is text even if it starts like a formula.
		{"xl/styles.xml", xmlHead + `<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
			`<fonts count="1"><font><sz val="11"/><name val="Calibri"/></font></fonts>` +
			`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
			`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
			`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
			`<cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0" quotePrefix="1"/></cellXfs>` +
			`</styleSheet>`},
	}, parts...)
	return zipOf(parts)
}

func worksheetXML(s xsheet) string {
	keys := make([][2]int, 0, len(s.cells))
	for k := range s.cells {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	var b strings.Builder
	b.WriteString(xmlHead + `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	row := 0
	for _, k := range keys {
		if k[0] != row {
			if row != 0 {
				b.WriteString(`</row>`)
			}
			row = k[0]
			b.WriteString(`<row r="` + strconv.Itoa(row) + `">`)
		}
		b.WriteString(cellXML(colName(k[1])+strconv.Itoa(k[0]), s.cells[k]))
	}
	if row != 0 {
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.String()
}

func cellXML(ref string, c xcell) string {
	r := `r="` + ref + `"`
	if c.formula != "" {
		v := ""
		switch t := c.v.(type) {
		case float64:
			v = `<v>` + strconv.FormatFloat(t, 'g', -1, 64) + `</v>`
		case string:
			return `<c ` + r + ` t="str"><f>` + esc(c.formula) + `</f><v>` + esc(t) + `</v></c>`
		}
		return `<c ` + r + `><f>` + esc(c.formula) + `</f>` + v + `</c>`
	}
	switch t := c.v.(type) {
	case nil:
		return ""
	case float64:
		return `<c ` + r + `><v>` + strconv.FormatFloat(t, 'g', -1, 64) + `</v></c>`
	case bool:
		v := "0"
		if t {
			v = "1"
		}
		return `<c ` + r + ` t="b"><v>` + v + `</v></c>`
	case string:
		style := ""
		if looksLikeFormula(t) {
			style = ` s="1"`
		}
		return `<c ` + r + style + ` t="inlineStr"><is><t xml:space="preserve">` + esc(t) + `</t></is></c>`
	}
	return ""
}

// ---- docx ----

type docxWriter struct {
	b    strings.Builder
	rev  int
	date string
}

func docxOf(title string, root map[string]any, now time.Time) ([]byte, error) {
	w := &docxWriter{date: now.UTC().Format(time.RFC3339)}
	w.b.WriteString(xmlHead + `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, n := range asList(asMap(root["doc"])["content"]) {
		w.block(asMap(n), 0)
	}
	w.b.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="708" w:footer="708" w:gutter="0"/></w:sectPr></w:body></w:document>`)
	return zipOf([]part{
		{"[Content_Types].xml", xmlHead + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
			`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
			`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/></Types>`},
		{"_rels/.rels", xmlHead + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/></Relationships>`},
		{"docProps/core.xml", xmlHead + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` + esc(title) + `</dc:title></cp:coreProperties>`},
		{"word/_rels/document.xml.rels", xmlHead + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`},
		{"word/styles.xml", docxStyles},
		{"word/document.xml", w.b.String()},
	})
}

var docxStyles = xmlHead + `<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:cs="Calibri"/><w:sz w:val="22"/></w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>` +
	`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>` +
	docxHeading(1, 36) + docxHeading(2, 30) + docxHeading(3, 26) + docxHeading(4, 24) + docxHeading(5, 22) + docxHeading(6, 22) +
	`<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:basedOn w:val="Normal"/><w:pPr><w:ind w:left="720"/></w:pPr><w:rPr><w:i/><w:color w:val="555555"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Code"><w:name w:val="Code"/><w:basedOn w:val="Normal"/><w:rPr><w:rFonts w:ascii="Consolas" w:hAnsi="Consolas"/><w:sz w:val="20"/></w:rPr></w:style>` +
	`<w:style w:type="table" w:styleId="TableGrid"><w:name w:val="Table Grid"/><w:tblPr><w:tblBorders><w:top w:val="single" w:sz="4" w:color="999999"/><w:left w:val="single" w:sz="4" w:color="999999"/><w:bottom w:val="single" w:sz="4" w:color="999999"/><w:right w:val="single" w:sz="4" w:color="999999"/><w:insideH w:val="single" w:sz="4" w:color="999999"/><w:insideV w:val="single" w:sz="4" w:color="999999"/></w:tblBorders></w:tblPr></w:style>` +
	`</w:styles>`

func docxHeading(level, size int) string {
	l := strconv.Itoa(level)
	return `<w:style w:type="paragraph" w:styleId="Heading` + l + `"><w:name w:val="heading ` + l + `"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="240" w:after="120"/><w:outlineLvl w:val="` + strconv.Itoa(level-1) + `"/></w:pPr><w:rPr><w:b/><w:sz w:val="` + strconv.Itoa(size) + `"/></w:rPr></w:style>`
}

func (w *docxWriter) para(style string, indent int, prefix string, inline []any) {
	w.b.WriteString(`<w:p>`)
	if style != "" || indent > 0 {
		w.b.WriteString(`<w:pPr>`)
		if style != "" {
			w.b.WriteString(`<w:pStyle w:val="` + style + `"/>`)
		}
		if indent > 0 {
			w.b.WriteString(`<w:ind w:left="` + strconv.Itoa(360*indent) + `" w:hanging="360"/>`)
		}
		w.b.WriteString(`</w:pPr>`)
	}
	if prefix != "" {
		w.run(prefix, nil)
	}
	for _, c := range inline {
		w.inline(asMap(c))
	}
	w.b.WriteString(`</w:p>`)
}

func (w *docxWriter) block(n map[string]any, depth int) {
	if n == nil || depth > maxDepth {
		return
	}
	content := asList(n["content"])
	attrs := asMap(n["attrs"])
	switch asStr(n["type"]) {
	case "heading":
		lvl, _ := strconv.Atoi(fmt.Sprint(attrs["level"]))
		w.para("Heading"+strconv.Itoa(min(max(lvl, 1), 6)), 0, "", content)
	case "paragraph":
		w.para("", 0, "", content)
	case "blockquote":
		for _, c := range content {
			cm := asMap(c)
			if asStr(cm["type"]) == "paragraph" {
				w.para("Quote", 0, "", asList(cm["content"]))
			} else {
				w.block(cm, depth+1)
			}
		}
	case "codeBlock":
		w.para("Code", 0, "", content)
	case "bulletList", "orderedList":
		ordered := asStr(n["type"]) == "orderedList"
		for i, item := range content {
			marker := "•\t"
			if ordered {
				marker = strconv.Itoa(i+1) + ".\t"
			}
			for j, c := range asList(asMap(item)["content"]) {
				cm := asMap(c)
				if asStr(cm["type"]) == "paragraph" {
					p := ""
					if j == 0 {
						p = marker
					}
					w.para("", depth+1, p, asList(cm["content"]))
				} else {
					w.block(cm, depth+1)
				}
			}
		}
	case "clause":
		title := strings.TrimSpace(strings.TrimSpace(asStr(attrs["number"])+". ") + " " + asStr(attrs["title"]))
		if title != "" && title != "." {
			w.para("Heading3", 0, strings.TrimPrefix(title, ". "), nil)
		}
		for _, c := range content {
			w.block(asMap(c), depth+1)
		}
	case "table":
		w.table(content)
	case "embed":
		w.para("Quote", 0, "[artifact "+asStr(attrs["artifact_id"])+"]", nil)
	case "image":
		w.para("Quote", 0, "[image]", nil)
	default:
		for _, c := range content {
			w.block(asMap(c), depth+1)
		}
	}
}

func (w *docxWriter) table(rows []any) {
	w.b.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/><w:tblW w:w="0" w:type="auto"/></w:tblPr>`)
	for _, r := range rows {
		w.b.WriteString(`<w:tr>`)
		for _, c := range asList(asMap(r)["content"]) {
			cm := asMap(c)
			w.b.WriteString(`<w:tc><w:tcPr><w:tcW w:w="0" w:type="auto"/></w:tcPr>`)
			wrote := false
			for _, p := range asList(cm["content"]) {
				w.block(asMap(p), 1)
				wrote = true
			}
			if !wrote {
				w.b.WriteString(`<w:p/>`) // a cell needs at least one paragraph
			}
			w.b.WriteString(`</w:tc>`)
		}
		w.b.WriteString(`</w:tr>`)
	}
	w.b.WriteString(`</w:tbl>`)
}

func (w *docxWriter) inline(n map[string]any) {
	switch asStr(n["type"]) {
	case "hardBreak":
		w.b.WriteString(`<w:r><w:br/></w:r>`)
	case "text":
		text := asStr(n["text"])
		marks := asList(n["marks"])
		var sug map[string]any
		for _, m := range marks {
			if mm := asMap(m); asStr(mm["type"]) == "suggestion" {
				sug = asMap(mm["attrs"])
			}
			if mm := asMap(m); asStr(mm["type"]) == "link" {
				if href := asStr(asMap(mm["attrs"])["href"]); href != "" && href != text {
					text += " (" + href + ")"
				}
			}
		}
		if sug == nil {
			w.run(text, marks)
			return
		}
		w.rev++
		author := asMap(sug["author"])
		who := esc(strings.Trim(asStr(author["kind"])+":"+asStr(author["id"]), ":"))
		head := ` w:id="` + strconv.Itoa(w.rev) + `" w:author="` + who + `" w:date="` + w.date + `"`
		if asStr(sug["kind"]) == "del" {
			w.b.WriteString(`<w:del` + head + `>`)
			w.runTag(text, marks, "w:delText")
			w.b.WriteString(`</w:del>`)
		} else {
			w.b.WriteString(`<w:ins` + head + `>`)
			w.run(text, marks)
			w.b.WriteString(`</w:ins>`)
		}
	}
}

func (w *docxWriter) run(text string, marks []any) { w.runTag(text, marks, "w:t") }

func (w *docxWriter) runTag(text string, marks []any, tag string) {
	w.b.WriteString(`<w:r>`)
	var props strings.Builder
	for _, m := range marks {
		switch asStr(asMap(m)["type"]) {
		case "bold":
			props.WriteString(`<w:b/>`)
		case "italic":
			props.WriteString(`<w:i/>`)
		case "underline", "link":
			props.WriteString(`<w:u w:val="single"/>`)
		case "strike":
			props.WriteString(`<w:strike/>`)
		case "code":
			props.WriteString(`<w:rFonts w:ascii="Consolas" w:hAnsi="Consolas"/>`)
		}
	}
	if props.Len() > 0 {
		w.b.WriteString(`<w:rPr>` + props.String() + `</w:rPr>`)
	}
	// Tabs inside a run must be <w:tab/> to render as tabs.
	for i, seg := range strings.Split(text, "\t") {
		if i > 0 {
			w.b.WriteString(`<w:tab/>`)
		}
		if seg != "" {
			w.b.WriteString(`<` + tag + ` xml:space="preserve">` + esc(seg) + `</` + tag + `>`)
		}
	}
	w.b.WriteString(`</w:r>`)
}
