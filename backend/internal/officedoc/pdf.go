package officedoc

import (
	"bytes"
	"math"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

// buildPDF renders the document model with the built-in PDF fonts (Latin text:
// Spanish and English accents are fine; characters outside Latin-1/cp1252 are
// replaced). It is pure Go: no browser, no external tool.
func buildPDF(d Doc) ([]byte, error) {
	items, err := d.items()
	if err != nil {
		return nil, err
	}
	landscape := false
	for _, it := range items {
		if it.kind == "table" && len(it.tbl.cols) > 6 {
			landscape = true
		}
	}
	orient := "P"
	if landscape {
		orient = "L"
	}
	pdf := fpdf.New(orient, "mm", "A4", "")
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 18)
	pdf.SetCompression(true)
	pdf.SetCreationDate(time.Now().UTC())
	pdf.SetProducer("AI Workforce OS", true)
	pdf.SetCreator("AI Workforce OS", true)
	if d.Title != "" {
		pdf.SetTitle(d.Title, true)
	}
	pdf.AliasNbPages("")
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("Helvetica", "", 8)
		pdf.SetTextColor(120, 120, 120)
		pdf.CellFormat(0, 6, tr("Page ")+itoa(pdf.PageNo())+" / {nb}", "", 0, "C", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
	})
	pdf.AddPage()
	pw, _ := pdf.GetPageSize()
	width := pw - 30

	if d.Title != "" {
		pdf.SetFont("Helvetica", "B", 20)
		pdf.SetTextColor(31, 56, 100)
		pdf.MultiCell(width, 9, tr(d.Title), "", "L", false)
		pdf.SetTextColor(0, 0, 0)
		pdf.Ln(3)
	}
	for _, it := range items {
		switch it.kind {
		case "heading":
			size := map[int]float64{1: 16, 2: 13.5, 3: 11.5}[it.level]
			if size == 0 {
				size = 13.5
			}
			pdf.Ln(2)
			pdf.SetFont("Helvetica", "B", size)
			pdf.SetTextColor(47, 84, 150)
			pdf.MultiCell(width, size*0.5, tr(it.text), "", "L", false)
			pdf.SetTextColor(0, 0, 0)
			pdf.Ln(1.5)
		case "paragraph":
			pdf.SetFont("Helvetica", "", 10.5)
			for _, ln := range strings.Split(it.text, "\n") {
				if strings.TrimSpace(ln) == "" {
					continue
				}
				pdf.MultiCell(width, 5.2, tr(ln), "", "L", false)
				pdf.Ln(1.2)
			}
		case "bullets":
			pdf.SetFont("Helvetica", "", 10.5)
			for _, x := range it.list {
				x0 := pdf.GetX()
				pdf.CellFormat(6, 5.2, tr("•"), "", 0, "R", false, 0, "")
				pdf.SetX(x0 + 8)
				pdf.MultiCell(width-8, 5.2, tr(x), "", "L", false)
			}
			pdf.Ln(1.2)
		case "table":
			pdfTable(pdf, tr, it.tbl, width)
			pdf.Ln(3)
		}
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, bad("could not write the pdf")
	}
	return buf.Bytes(), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

func pdfTable(pdf *fpdf.Fpdf, tr func(string) string, t *table, width float64) {
	n := len(t.cols)
	if n == 0 {
		return
	}
	const lh = 5.0
	// column widths proportional to the longest content, within bounds
	need := make([]float64, n)
	pdf.SetFont("Helvetica", "B", 9)
	for i, c := range t.cols {
		h := c.Header
		if h == "" {
			h = colName(i)
		}
		need[i] = pdf.GetStringWidth(tr(h)) + 4
	}
	pdf.SetFont("Helvetica", "", 9)
	for ri := range t.rows {
		for ci := 0; ci < n; ci++ {
			s := t.display(ci, t.cellAt(ri, ci))
			w := 0.0
			for _, ln := range strings.Split(s, "\n") {
				w = math.Max(w, pdf.GetStringWidth(tr(ln)))
			}
			need[ci] = math.Max(need[ci], math.Min(w+4, width*0.6))
		}
	}
	sum := 0.0
	for _, w := range need {
		sum += w
	}
	cw := make([]float64, n)
	for i := range cw {
		if sum <= width {
			cw[i] = need[i] + (width-sum)/float64(n)
		} else {
			cw[i] = math.Max(need[i]*width/sum, 10)
		}
	}
	row := func(cells []string, header, bold bool, rights []bool) {
		// height of the row = tallest cell
		lines := 1
		split := make([][]string, n)
		pdf.SetFont("Helvetica", map[bool]string{true: "B", false: ""}[header || bold], 9)
		for i := 0; i < n; i++ {
			parts := pdf.SplitText(tr(cells[i]), cw[i]-3)
			if len(parts) == 0 {
				parts = []string{""}
			}
			split[i] = make([]string, len(parts))
			for k, p := range parts {
				split[i][k] = string(p)
			}
			if len(parts) > lines {
				lines = len(parts)
			}
		}
		h := float64(lines)*lh + 2
		_, ph := pdf.GetPageSize()
		if pdf.GetY()+h > ph-18 {
			pdf.AddPage()
		}
		x0, y0 := pdf.GetX(), pdf.GetY()
		for i := 0; i < n; i++ {
			if header {
				pdf.SetFillColor(217, 226, 243)
			} else if bold {
				pdf.SetFillColor(242, 242, 242)
			}
			pdf.Rect(x0, y0, cw[i], h, map[bool]string{true: "FD", false: "D"}[header || bold])
			align := "L"
			if rights != nil && rights[i] {
				align = "R"
			}
			pdf.SetXY(x0, y0+1)
			for _, ln := range split[i] {
				pdf.SetX(x0 + 1.5)
				pdf.CellFormat(cw[i]-3, lh, ln, "", 2, align, false, 0, "")
			}
			x0 += cw[i]
		}
		pdf.SetXY(15, y0+h)
	}
	pdf.SetDrawColor(153, 153, 153)
	pdf.SetLineWidth(0.2)
	pdf.SetX(15)
	heads := make([]string, n)
	rights := make([]bool, n)
	for i, c := range t.cols {
		heads[i] = c.Header
		if heads[i] == "" {
			heads[i] = colName(i)
		}
		rights[i] = c.Type == TypeNumber || c.Type == TypeCurrency || c.Type == TypePercent
	}
	row(heads, true, false, nil)
	for ri := range t.rows {
		cells := make([]string, n)
		for ci := 0; ci < n; ci++ {
			cells[ci] = t.display(ci, t.cellAt(ri, ci))
		}
		row(cells, false, ri == t.totalsRow, rights)
	}
}
