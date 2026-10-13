package drive

import (
	"context"
	"fmt"
	"path"
	"strings"

	"aiworkforce/backend/internal/pdftext"
)

// PDF text (capability 2 of docs/architecture/agent-capabilities.md): the text
// layer with page markers ("[page 3]"), bounded by pdftext's limits. A PDF whose
// text layer is empty or very thin is flagged Scanned: its pages can be seen as
// images (fs.read view:"image", rendered in the sandbox, see vision.go).

// MaxPDFBytes is the largest PDF whose text is extracted (it must be read whole).
const MaxPDFBytes = pdftext.DefaultMaxBytes

func isPDFName(name string) bool { return strings.EqualFold(path.Ext(name), ".pdf") }

// readLimit is how many bytes of a file are read to extract its text.
func readLimit(name string) int64 {
	if isPDFName(name) {
		return MaxPDFBytes
	}
	return MaxExtractBytes
}

// extractPDF returns the text of pages (nil = all) of a PDF, never failing:
// problems are reported with a stable Code and a plain Note.
func extractPDF(data []byte, maxChars int, pages []int) Extracted {
	r, err := pdftext.Extract(context.Background(), data, pages, pdftext.Limits{MaxChars: maxChars})
	if err != nil {
		code := pdftext.CodeOf(err)
		note := map[string]string{
			pdftext.CodeTooLarge:  fmt.Sprintf("pdf larger than %d MB: text not extracted", MaxPDFBytes>>20),
			pdftext.CodeEncrypted: "pdf protected with a password: text not extracted",
			pdftext.CodeTimeout:   "pdf took too long to read: text not extracted",
			pdftext.CodeBusy:      "pdf reader busy: try again in a moment",
		}[code]
		if note == "" {
			code, note = pdftext.CodeInvalid, "pdf could not be read safely (malformed or unsupported)"
		}
		return Extracted{Kind: "pdf", Code: code, Note: note}
	}
	e := Extracted{Text: r.Text, Kind: "pdf", Extracted: true, Truncated: r.Truncated, TotalChars: r.Chars,
		Pages: r.Pages, Scanned: r.Scanned, ImagePages: r.ImagePages}
	switch {
	case r.Scanned:
		e.Note = "scanned pdf: the text layer is empty or very thin"
	case len(r.ImagePages) > 0:
		e.Note = fmt.Sprintf("%d page(s) have no text layer", len(r.ImagePages))
	}
	if r.Truncated {
		e.Note = strings.TrimPrefix(e.Note+"; pages or characters were cut at the safety limits", "; ")
		e.TotalChars = 0
	}
	return e
}
