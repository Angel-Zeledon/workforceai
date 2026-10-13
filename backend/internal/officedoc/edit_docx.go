package officedoc

import (
	"archive/zip"
	"bytes"
	"html"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// DOCX edits are made IN PLACE in word/document.xml: every other part of the
// package (styles, numbering, images, headers, footers, settings) is copied
// untouched, and so is all the markup around the text that changes. Only the
// text of w:t elements is rewritten. A replacement takes the formatting of the
// first run it touches. Text in headers, footers, footnotes and text boxes is
// not reached.

const (
	maxDocxEditBytes   = 8 << 20
	maxDocxEditPart    = 32 << 20
	maxDocxEditTotal   = 64 << 20
	maxDocxEditEntries = 5000
	docxMainPart       = "word/document.xml"
)

var (
	reParaX     = regexp.MustCompile(`(?s)<w:p(?:\s[^>/]*)?>.*?</w:p>`)
	reRunTextX  = regexp.MustCompile(`<w:t(?:\s[^>]*)?>([^<]*)</w:t>`)
	reHeadingX  = regexp.MustCompile(`<w:pStyle w:val="(?:Heading[1-9]|Title)"`)
	reNumberedX = regexp.MustCompile(`<w:pStyle w:val="Heading[1-9]"`)
	docxEmptyRu = `<w:r><w:t xml:space="preserve">`
)

func editDOCX(data []byte, ops []Op) ([]byte, *EditError) {
	if len(data) > maxDocxEditBytes {
		return nil, &EditError{Code: CodeTooLarge, Msg: "the document is too large to edit"}
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(zr.File) > maxDocxEditEntries {
		return nil, &EditError{Code: CodeUnsupported, Msg: "the document could not be read safely"}
	}
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
	}
	if total > maxDocxEditTotal {
		return nil, &EditError{Code: CodeTooLarge, Msg: "the document is too large to edit"}
	}
	parts := make([][]byte, len(zr.File))
	mainIdx := -1
	for i, f := range zr.File {
		if f.UncompressedSize64 > maxDocxEditPart {
			return nil, &EditError{Code: CodeTooLarge, Msg: "the document is too large to edit"}
		}
		rc, err := f.Open()
		if err != nil {
			return nil, &EditError{Code: CodeUnsupported, Msg: "the document could not be read safely"}
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxDocxEditPart+1))
		rc.Close()
		if err != nil || len(b) > maxDocxEditPart {
			return nil, &EditError{Code: CodeUnsupported, Msg: "the document could not be read safely"}
		}
		parts[i] = b
		if f.Name == docxMainPart {
			mainIdx = i
		}
	}
	if mainIdx < 0 {
		return nil, &EditError{Code: CodeUnsupported, Msg: "this is not a Word document (no word/document.xml)"}
	}
	doc := string(parts[mainIdx])
	for n, op := range ops {
		i := n + 1
		kind, _ := op.str("op")
		var e *EditError
		switch kind {
		case "replace":
			doc, e = docxReplace(doc, op, i)
		case "set_heading":
			doc, e = docxSetHeading(doc, op, i)
		case "append_paragraph":
			doc, e = docxAppend(doc, op, i)
		default:
			return nil, editBad(i, "unknown op %q for a docx file (replace, set_heading, append_paragraph)", kind)
		}
		if e != nil {
			return nil, e
		}
	}
	parts[mainIdx] = []byte(doc)
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for i, f := range zr.File {
		h := &zip.FileHeader{Name: f.Name, Method: zip.Deflate, Modified: f.Modified}
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, &EditError{Code: CodeInvalidResult, Msg: "could not write the document"}
		}
		if _, err := w.Write(parts[i]); err != nil {
			return nil, &EditError{Code: CodeInvalidResult, Msg: "could not write the document"}
		}
	}
	if err := zw.Close(); err != nil {
		return nil, &EditError{Code: CodeInvalidResult, Msg: "could not write the document"}
	}
	return out.Bytes(), nil
}

// ---- paragraph model ----

type xrun struct {
	start, end int    // the whole <w:t ...>...</w:t> element, absolute in the document
	text       []rune // unescaped text
	changed    bool
}

type xpara struct {
	start, end int  // absolute
	heading    bool // Heading1-9 or Title (found by its text)
	numbered   bool // Heading1-9 (counted by index)
	runs       []xrun
}

func (p xpara) text() string {
	var b strings.Builder
	for _, r := range p.runs {
		b.WriteString(string(r.text))
	}
	return b.String()
}

func parseParas(doc string) []xpara {
	var out []xpara
	for _, loc := range reParaX.FindAllStringIndex(doc, -1) {
		s := doc[loc[0]:loc[1]]
		p := xpara{start: loc[0], end: loc[1], heading: reHeadingX.MatchString(s), numbered: reNumberedX.MatchString(s)}
		for _, m := range reRunTextX.FindAllStringSubmatchIndex(s, -1) {
			p.runs = append(p.runs, xrun{start: loc[0] + m[0], end: loc[0] + m[1], text: []rune(html.UnescapeString(s[m[2]:m[3]]))})
		}
		out = append(out, p)
	}
	return out
}

// spliceRuns rewrites the changed w:t elements of a paragraph (last to first).
func spliceRuns(doc string, p xpara) string {
	for k := len(p.runs) - 1; k >= 0; k-- {
		r := p.runs[k]
		if !r.changed {
			continue
		}
		doc = doc[:r.start] + `<w:t xml:space="preserve">` + esc(string(r.text)) + `</w:t>` + doc[r.end:]
	}
	return doc
}

// replaceSpan replaces runes [s,e) of the paragraph text with nw, inside the
// runs: the text goes into the first run touched, the rest of the span is removed.
func replaceSpan(runs []xrun, s, e int, nw []rune) {
	pos := 0
	first := true
	for k := range runs {
		rs, re := pos, pos+len(runs[k].text)
		pos = re
		if re <= s || rs >= e {
			continue
		}
		lo, hi := max(s, rs)-rs, min(e, re)-rs
		t := runs[k].text
		var nt []rune
		nt = append(nt, t[:lo]...)
		if first {
			nt = append(nt, nw...)
			first = false
		}
		nt = append(nt, t[hi:]...)
		runs[k].text, runs[k].changed = nt, true
	}
}

func oneLineErr(i int, what string, s string) *EditError {
	if strings.ContainsAny(s, "\r\n") {
		return editBad(i, "%s must be a single line (use append_paragraph for new paragraphs)", what)
	}
	return nil
}

func docxReplace(doc string, op Op, i int) (string, *EditError) {
	old, ok := op.str("old")
	if !ok || old == "" {
		return "", editBad(i, "replace needs a non-empty old text")
	}
	nw, ok := op.str("new")
	if !ok {
		return "", editBad(i, "replace needs new (use \"\" to remove the text)")
	}
	if e := oneLineErr(i, "old", old); e != nil {
		return "", e
	}
	if e := oneLineErr(i, "new", nw); e != nil {
		return "", e
	}
	nw = cleanText(nw)
	want, e := op.expected(i)
	if e != nil {
		return "", e
	}
	paras := parseParas(doc)
	found := 0
	for _, p := range paras {
		found += strings.Count(p.text(), old)
	}
	if e := checkCount(i, found, want, "old"); e != nil {
		e.Msg += " (a docx match must lie inside one paragraph)"
		return "", e
	}
	for k := len(paras) - 1; k >= 0; k-- {
		p := paras[k]
		t := p.text()
		if !strings.Contains(t, old) {
			continue
		}
		// occurrences as rune offsets, applied last to first
		var offs []int
		for from := 0; ; {
			j := strings.Index(t[from:], old)
			if j < 0 {
				break
			}
			offs = append(offs, utf8.RuneCountInString(t[:from+j]))
			from += j + len(old)
		}
		oldLen := utf8.RuneCountInString(old)
		for x := len(offs) - 1; x >= 0; x-- {
			replaceSpan(p.runs, offs[x], offs[x]+oldLen, []rune(nw))
		}
		doc = spliceRuns(doc, p)
	}
	return doc, nil
}

func docxSetHeading(doc string, op Op, i int) (string, *EditError) {
	nw, ok := op.str("text")
	if !ok || strings.TrimSpace(nw) == "" {
		return "", editBad(i, "set_heading needs text")
	}
	if e := oneLineErr(i, "text", nw); e != nil {
		return "", e
	}
	nw = cleanText(nw)
	paras := parseParas(doc)
	var heads, numbered []int
	for k, p := range paras {
		if p.heading {
			heads = append(heads, k)
		}
		if p.numbered {
			numbered = append(numbered, k)
		}
	}
	target := -1
	if old, ok := op.str("old"); ok && old != "" {
		var hit []int
		for _, k := range heads {
			if strings.TrimSpace(paras[k].text()) == strings.TrimSpace(old) {
				hit = append(hit, k)
			}
		}
		if e := checkCount(i, len(hit), 1, "heading"); e != nil {
			return "", e
		}
		target = hit[0]
	} else if n, ok := argIndex(op["index"]); ok {
		if n < 1 || n > len(numbered) {
			return "", &EditError{Code: CodeNotFound, Op: i, Matches: 0, Msg: "op " + itoa(i) + ": the document has " + itoa(len(numbered)) + " heading(s) (the title not counted); index is 1-based"}
		}
		target = numbered[n-1]
	} else {
		return "", editBad(i, "set_heading needs old (the current heading text) or index (1-based, in document order)")
	}
	p := paras[target]
	if len(p.runs) == 0 {
		at := p.end - len("</w:p>")
		return doc[:at] + docxEmptyRu + esc(nw) + `</w:t></w:r>` + doc[at:], nil
	}
	replaceSpan(p.runs, 0, len(p.text()), nil) // empties every run
	p.runs[0].text = []rune(nw)
	p.runs[0].changed = true
	return spliceRuns(doc, p), nil
}

func argIndex(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), n == float64(int(n))
	case int:
		return n, true
	}
	return 0, false
}

func docxAppend(doc string, op Op, i int) (string, *EditError) {
	t, ok := op.str("text")
	if !ok || strings.TrimSpace(t) == "" {
		return "", editBad(i, "append_paragraph needs text")
	}
	if utf8.RuneCountInString(t) > MaxTextRunes {
		return "", editBad(i, "the paragraph is longer than %d characters", MaxTextRunes)
	}
	var xml strings.Builder
	for _, ln := range strings.Split(cleanText(t), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		xml.WriteString(para("", esc(ln), false))
	}
	at := strings.LastIndex(doc, "<w:sectPr")
	if end := strings.LastIndex(doc, "</w:body>"); at < 0 || (end >= 0 && at > end) {
		at = end
	}
	if at < 0 {
		return "", &EditError{Code: CodeUnsupported, Op: i, Msg: "the document body was not found"}
	}
	return doc[:at] + xml.String() + doc[at:], nil
}
