package pdftext_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-pdf/fpdf"

	"aiworkforce/backend/internal/officedoc"
	"aiworkforce/backend/internal/pdftext"
)

// textPDF builds a real PDF with the platform's own generator (the same bytes
// fs.write_document produces).
func textPDF(t *testing.T, paragraphs ...string) []byte {
	t.Helper()
	blocks := []any{}
	for _, p := range paragraphs {
		blocks = append(blocks, map[string]any{"type": "paragraph", "text": p})
	}
	doc, err := officedoc.Parse(map[string]any{"title": "Quarterly report", "blocks": blocks})
	if err != nil {
		t.Fatal(err)
	}
	data, err := officedoc.Build("pdf", doc)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// pagesPDF builds a PDF whose pages carry the given text ("" = a page with no
// text layer, like a scan).
func pagesPDF(t *testing.T, texts ...string) []byte {
	t.Helper()
	p := fpdf.New("P", "mm", "A4", "")
	p.SetFont("Helvetica", "", 12)
	for _, tx := range texts {
		p.AddPage()
		if tx != "" {
			p.MultiCell(0, 6, tx, "", "L", false)
		} else {
			p.Rect(20, 20, 100, 100, "F") // a picture-like shape, no text
		}
	}
	var b bytes.Buffer
	if err := p.Output(&b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const para = "Revenue grew twelve percent in the third quarter while operating costs stayed flat across every region."

func TestExtractTextWithPageMarkers(t *testing.T) {
	data := textPDF(t, para, "Net margin reached 18 percent.")
	r, err := pdftext.Extract(context.Background(), data, nil, pdftext.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.Text, "[page 1]\n") || !strings.Contains(r.Text, "Revenue") || !strings.Contains(r.Text, "Net margin") {
		t.Fatalf("unexpected text: %q", r.Text)
	}
	if r.Scanned || r.Pages != 1 || r.PagesRead != 1 || r.Truncated {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestExtractMultiPageAndRange(t *testing.T) {
	data := pagesPDF(t, "First page: "+para, "Second page: "+para, "Third page: "+para)
	r, err := pdftext.Extract(context.Background(), data, nil, pdftext.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"[page 1]", "[page 2]", "[page 3]", "Third page"} {
		if !strings.Contains(r.Text, m) {
			t.Fatalf("missing %q in %q", m, r.Text)
		}
	}
	pages, err := pdftext.ParsePages("2-3", r.Pages)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := pdftext.Extract(context.Background(), data, pages, pdftext.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r2.Text, "[page 1]") || !strings.Contains(r2.Text, "[page 2]") || r2.PagesRead != 2 {
		t.Fatalf("range not honoured: %+v", r2)
	}
}

func TestScannedDetection(t *testing.T) {
	data := pagesPDF(t, "", "", "")
	r, err := pdftext.Extract(context.Background(), data, nil, pdftext.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Scanned || len(r.ImagePages) != 3 || !strings.Contains(r.Text, "no text layer") {
		t.Fatalf("a PDF without text must be reported as scanned: %+v", r)
	}
	// One text page among three image pages is still a scan.
	mixed := pagesPDF(t, "", para, "", "")
	r, err = pdftext.Extract(context.Background(), mixed, nil, pdftext.Limits{})
	if err != nil || !r.Scanned || len(r.ImagePages) != 3 {
		t.Fatalf("mostly-image PDF: %+v %v", r, err)
	}
	// A text PDF is not.
	r, err = pdftext.Extract(context.Background(), pagesPDF(t, para, para), nil, pdftext.Limits{})
	if err != nil || r.Scanned || len(r.ImagePages) != 0 {
		t.Fatalf("text PDF flagged as scanned: %+v %v", r, err)
	}
}

func TestLimits(t *testing.T) {
	data := pagesPDF(t, para, para, para, para, para)
	r, err := pdftext.Extract(context.Background(), data, nil, pdftext.Limits{MaxPages: 2})
	if err != nil || r.PagesRead != 2 || !r.Truncated || r.Pages != 5 {
		t.Fatalf("page cap: %+v %v", r, err)
	}
	r, err = pdftext.Extract(context.Background(), data, nil, pdftext.Limits{MaxChars: 50})
	if err != nil || !r.Truncated || r.Chars > 50 {
		t.Fatalf("char cap: chars=%d truncated=%v err=%v", r.Chars, r.Truncated, err)
	}
	_, err = pdftext.Extract(context.Background(), data, nil, pdftext.Limits{MaxBytes: 100})
	if pdftext.CodeOf(err) != pdftext.CodeTooLarge {
		t.Fatalf("size cap: %v", err)
	}
}

func TestCorruptInputNeverPanics(t *testing.T) {
	good := textPDF(t, para)
	cases := map[string][]byte{
		"empty":       {},
		"not a pdf":   []byte("hello world, not a pdf at all"),
		"header only": []byte("%PDF-1.4\n"),
		"truncated":   good[:len(good)/2],
		"garbage":     append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte{0xff, 0x00, 'x', '('}, 4000)...),
		"bad xref":    []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\nstartxref\n999999\n%%EOF\n"),
		"pdf 2.0":     append([]byte("%PDF-2.0\n"), good[9:]...),
	}
	// Flip bytes in the middle of a valid file (content streams, xref).
	for _, off := range []int{len(good) / 3, len(good) * 2 / 3, len(good) - 40} {
		m := append([]byte(nil), good...)
		for i := off; i < off+20 && i < len(m); i++ {
			m[i] ^= 0x5a
		}
		cases["mutated@"+itoa(off)] = m
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			r, err := pdftext.Extract(context.Background(), data, nil, pdftext.Limits{Timeout: 5 * time.Second})
			if err == nil {
				return // some mutations still parse: fine, as long as nothing panicked
			}
			if code := pdftext.CodeOf(err); code == "" {
				t.Fatalf("error without a stable code: %v (%+v)", err, r)
			}
		})
	}
}

func TestEncrypted(t *testing.T) {
	p := fpdf.New("P", "mm", "A4", "")
	p.SetProtection(fpdf.CnProtectPrint, "user-secret", "owner-secret")
	p.SetFont("Helvetica", "", 12)
	p.AddPage()
	p.Cell(40, 10, para)
	var b bytes.Buffer
	if err := p.Output(&b); err != nil {
		t.Fatal(err)
	}
	_, err := pdftext.Extract(context.Background(), b.Bytes(), nil, pdftext.Limits{})
	if code := pdftext.CodeOf(err); code != pdftext.CodeEncrypted {
		t.Fatalf("a password-protected PDF must fail with a stable code, got %v", err)
	}
}

func TestTimeoutNeverHangs(t *testing.T) {
	data := pagesPDF(t, para, para, para)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := pdftext.Extract(context.Background(), data, nil, pdftext.Limits{Timeout: time.Nanosecond})
		if err != nil && pdftext.CodeOf(err) == "" {
			t.Errorf("uncoded error: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Extract did not return")
	}
}

func TestParsePages(t *testing.T) {
	ok := map[string][]int{"3": {3}, "3-5": {3, 4, 5}, "1,3,7-8": {1, 3, 7, 8}, " 2 , 2 ,1": {1, 2}, "9-12": {9, 10}}
	for in, want := range ok {
		got, err := pdftext.ParsePages(in, 10)
		if err != nil || len(got) != len(want) {
			t.Fatalf("%q: %v %v", in, got, err)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%q: %v", in, got)
			}
		}
	}
	for _, in := range []string{"0", "a", "5-3", "-2", "1-5000", "11-12"} {
		if _, err := pdftext.ParsePages(in, 10); pdftext.CodeOf(err) != pdftext.CodePages {
			t.Fatalf("%q must be refused, got %v", in, err)
		}
	}
	if p, err := pdftext.ParsePages("", 10); p != nil || err != nil {
		t.Fatal("empty means all pages")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
