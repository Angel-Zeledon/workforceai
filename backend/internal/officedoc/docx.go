package officedoc

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
)

// buildDOCX writes a small, valid WordprocessingML package by hand (title,
// headings, paragraphs, bullet lists and tables with a styled header row).
// Text is XML-escaped; nothing else from the model reaches the markup.
func buildDOCX(d Doc) ([]byte, error) {
	items, err := d.items()
	if err != nil {
		return nil, err
	}
	var body strings.Builder
	if d.Title != "" {
		body.WriteString(para("Title", esc(d.Title), false))
	}
	for _, it := range items {
		switch it.kind {
		case "heading":
			body.WriteString(para(fmt.Sprintf("Heading%d", it.level), esc(it.text), false))
		case "paragraph":
			for _, ln := range strings.Split(it.text, "\n") {
				if strings.TrimSpace(ln) == "" {
					continue
				}
				body.WriteString(para("", esc(ln), false))
			}
		case "bullets":
			for _, x := range it.list {
				body.WriteString(para("ListParagraph", esc(x), true))
			}
		case "table":
			body.WriteString(docxTable(it.tbl))
			body.WriteString(`<w:p/>`)
		}
	}
	doc := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body>` +
		body.String() +
		`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1134" w:right="1134" w:bottom="1134" w:left="1134" w:header="708" w:footer="708" w:gutter="0"/></w:sectPr>` +
		`</w:body></w:document>`
	title := esc(d.Title)
	parts := []struct{ name, data string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
			`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
			`<Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/>` +
			`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>` +
			`</Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
			`</Relationships>`},
		{"docProps/core.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
			`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" ` +
			`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>` + title + `</dc:title><dc:creator>AI Workforce OS</dc:creator></cp:coreProperties>`},
		{"word/_rels/document.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/>` +
			`</Relationships>`},
		{"word/document.xml", doc},
		{"word/styles.xml", docxStyles},
		{"word/numbering.xml", docxNumbering},
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range parts {
		w, err := zw.Create(p.name)
		if err != nil {
			return nil, bad("could not write the document")
		}
		if _, err := w.Write([]byte(p.data)); err != nil {
			return nil, bad("could not write the document")
		}
	}
	if err := zw.Close(); err != nil {
		return nil, bad("could not write the document")
	}
	return buf.Bytes(), nil
}

// esc escapes text for an XML text node and removes characters XML 1.0 forbids.
func esc(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r >= 0x20 && r != 0xFFFE && r != 0xFFFF && !(r >= 0xD800 && r <= 0xDFFF) {
			return r
		}
		return -1
	}, s)
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func run(text string, bold bool) string {
	rpr := ""
	if bold {
		rpr = `<w:rPr><w:b/></w:rPr>`
	}
	return `<w:r>` + rpr + `<w:t xml:space="preserve">` + text + `</w:t></w:r>`
}

func para(style, text string, bullet bool) string {
	ppr := ""
	switch {
	case bullet:
		ppr = `<w:pPr><w:pStyle w:val="ListParagraph"/><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr>`
	case style != "":
		ppr = `<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
	}
	return `<w:p>` + ppr + run(text, false) + `</w:p>`
}

func docxTable(t *table) string {
	n := len(t.cols)
	if n == 0 {
		return ""
	}
	w := 9638 / n // text width in twips (A4 minus margins)
	var b strings.Builder
	b.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="5000" w:type="pct"/><w:tblBorders>`)
	for _, side := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
		b.WriteString(`<w:` + side + ` w:val="single" w:sz="4" w:space="0" w:color="999999"/>`)
	}
	b.WriteString(`</w:tblBorders><w:tblLayout w:type="autofit"/><w:tblCellMar><w:left w:w="80" w:type="dxa"/><w:right w:w="80" w:type="dxa"/></w:tblCellMar></w:tblPr><w:tblGrid>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<w:gridCol w:w="%d"/>`, w)
	}
	b.WriteString(`</w:tblGrid>`)
	cellXML := func(text string, header, right, bold bool) string {
		shade := ""
		if header {
			shade = `<w:shd w:val="clear" w:color="auto" w:fill="D9E2F3"/>`
		}
		jc := ""
		if right {
			jc = `<w:pPr><w:jc w:val="right"/></w:pPr>`
		}
		return fmt.Sprintf(`<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/>%s</w:tcPr><w:p>%s%s</w:p></w:tc>`, w, shade, jc, run(esc(text), header || bold))
	}
	b.WriteString(`<w:tr><w:trPr><w:tblHeader/></w:trPr>`)
	for i, c := range t.cols {
		h := c.Header
		if h == "" {
			h = colName(i)
		}
		b.WriteString(cellXML(h, true, false, false))
	}
	b.WriteString(`</w:tr>`)
	for ri := range t.rows {
		b.WriteString(`<w:tr>`)
		for ci := 0; ci < n; ci++ {
			ty := t.cols[ci].Type
			b.WriteString(cellXML(t.display(ci, t.cellAt(ri, ci)), false, ty == TypeNumber || ty == TypeCurrency || ty == TypePercent, ri == t.totalsRow))
		}
		b.WriteString(`</w:tr>`)
	}
	b.WriteString(`</w:tbl>`)
	return b.String()
}

const docxStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:eastAsia="Calibri" w:cs="Calibri"/><w:sz w:val="22"/><w:szCs w:val="22"/><w:lang w:val="en-US"/></w:rPr></w:rPrDefault>` +
	`<w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>` +
	`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:spacing w:after="240"/></w:pPr><w:rPr><w:b/><w:sz w:val="48"/><w:szCs w:val="48"/><w:color w:val="1F3864"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="320" w:after="120"/><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:sz w:val="34"/><w:szCs w:val="34"/><w:color w:val="1F3864"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="240" w:after="100"/><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:b/><w:sz w:val="28"/><w:szCs w:val="28"/><w:color w:val="2F5496"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="200" w:after="80"/><w:outlineLvl w:val="2"/></w:pPr><w:rPr><w:b/><w:sz w:val="24"/><w:szCs w:val="24"/><w:color w:val="2F5496"/></w:rPr></w:style>` +
	`<w:style w:type="paragraph" w:styleId="ListParagraph"><w:name w:val="List Paragraph"/><w:basedOn w:val="Normal"/><w:qFormat/><w:pPr><w:spacing w:after="60"/><w:ind w:left="720"/></w:pPr></w:style>` +
	`</w:styles>`

const docxNumbering = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:abstractNum w:abstractNumId="0"><w:multiLevelType w:val="hybridMultilevel"/>` +
	`<w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="&#8226;"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl>` +
	`</w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num></w:numbering>`
