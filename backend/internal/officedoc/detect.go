package officedoc

import (
	"regexp"
	"strings"
)

var formatPatterns = []struct {
	format string
	re     *regexp.Regexp
}{
	{FormatXLSX, regexp.MustCompile(`(?i)\b(excel|xlsx?|spreadsheet|hoja[s]? de c[aá]lculo|planilla|google sheets?|worksheet)\b`)},
	{FormatPDF, regexp.MustCompile(`(?i)\bpdf\b`)},
	{FormatDOCX, regexp.MustCompile(`(?i)\b(word|docx?|documento (de|en) word|word document)\b`)},
	// "a document" counts only when the person asks for one to be made
	{FormatDOCX, regexp.MustCompile(`(?i)\b(hazme|haz|genera|generame|cr[eé]a|prepara|redacta|dame|necesito|quiero|elabora|make|create|generate|write|prepare|draft)\b[^.\n]{0,40}\b(documento|document)\b`)},
}

var formatMarker = regexp.MustCompile(`(?i)\[\[format:(xlsx|docx|pdf)\]\]`)

// FormatFromText returns the office format a request asks for ("excel" ->
// xlsx, "word" or "make me a document" -> docx, "pdf" -> pdf), or "" when it
// names none. When it names several, the one mentioned first wins.
func FormatFromText(text string) string {
	if m := formatMarker.FindStringSubmatch(text); m != nil { // [[format:xlsx]] from a slash command wins
		return strings.ToLower(m[1])
	}
	best, at := "", -1
	for _, p := range formatPatterns {
		if loc := p.re.FindStringIndex(text); loc != nil && (at < 0 || loc[0] < at) {
			best, at = p.format, loc[0]
		}
	}
	return best
}

// Prepare validates and normalizes a document built in Go (the same rules as a
// document an agent sends).
func Prepare(d Doc) (Doc, error) {
	if err := d.normalize(); err != nil {
		return d, err
	}
	return d, nil
}

// Ext is the file extension (with dot) of a format.
func Ext(format string) string { return "." + strings.ToLower(format) }
