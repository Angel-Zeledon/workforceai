// Package pdftext reads the text layer of a PDF, bounded and fail-safe.
//
// It is pure Go (github.com/ledongthuc/pdf, BSD-3-Clause, no dependencies) and
// never renders anything: a scanned PDF (no or almost no text layer) is reported
// as Scanned so the caller can offer the pages as images instead (rendered in the
// sandbox, see docs/architecture/agent-vision-pdf.md).
//
// The parser panics on malformed input and has no notion of a deadline, so every
// call is wrapped: panics become the stable code pdf_invalid, the work runs in a
// goroutine with a wall-clock timeout (pdf_timeout), at most MaxConcurrent
// extractions run at once (a parser stuck on a hostile file keeps its slot but
// can never block the caller), and pages, characters and input bytes are capped.
// The text is DATA: callers delimit it before it reaches a model.
package pdftext

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// Stable failure codes (part of the fs tool and the API contract).
const (
	CodeTooLarge  = "pdf_too_large" // the file is over Limits.MaxBytes
	CodeEncrypted = "pdf_encrypted" // a password is needed
	CodeInvalid   = "pdf_invalid"   // not a PDF, corrupt, or an unsupported structure
	CodeTimeout   = "pdf_timeout"   // the extraction did not finish within Limits.Timeout
	CodeBusy      = "pdf_busy"      // every extraction slot is taken (try again later)
	CodePages     = "pdf_pages_invalid"
)

// Error is a failure with a stable code.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

func fail(code, format string, a ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// CodeOf returns the stable code of err ("" when it has none).
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Limits bound one extraction. Zero values take the defaults.
type Limits struct {
	MaxBytes int64         // input size (default 20 MB)
	MaxPages int           // pages read (default 300); the rest is reported as truncated
	MaxChars int           // characters kept (default 1,000,000)
	Timeout  time.Duration // wall clock (default 8 s)
}

// Defaults.
const (
	DefaultMaxBytes = 20 << 20
	DefaultMaxPages = 300
	DefaultMaxChars = 1_000_000
	DefaultTimeout  = 8 * time.Second
	// MaxConcurrent extractions at once (process-wide).
	MaxConcurrent = 2
	// ThinPageChars: a page with fewer meaningful characters than this has no
	// usable text layer (a scan, a photo, a chart).
	ThinPageChars = 25
)

func (l Limits) withDefaults() Limits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultMaxBytes
	}
	if l.MaxPages <= 0 {
		l.MaxPages = DefaultMaxPages
	}
	if l.MaxChars <= 0 {
		l.MaxChars = DefaultMaxChars
	}
	if l.Timeout <= 0 {
		l.Timeout = DefaultTimeout
	}
	return l
}

// Result is the text of a PDF with page markers ("[page 3]").
type Result struct {
	Text string
	// Pages is the page count the document declares; PagesRead how many were read.
	Pages     int
	PagesRead int
	// Scanned: the text layer is empty or very thin (most pages have no text).
	Scanned bool
	// ImagePages are the pages (1-based, among those read) without usable text.
	ImagePages []int
	// Truncated: pages or characters were cut at the limits.
	Truncated bool
	// Chars is the length of Text in characters.
	Chars int
}

var slots = make(chan struct{}, MaxConcurrent)

// Extract reads the text of pages (nil = all, up to MaxPages) of a PDF.
func Extract(ctx context.Context, data []byte, pages []int, lim Limits) (Result, error) {
	lim = lim.withDefaults()
	if int64(len(data)) > lim.MaxBytes {
		return Result{}, fail(CodeTooLarge, "the PDF is larger than %d MB", lim.MaxBytes>>20)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return Result{}, fail(CodeInvalid, "not a PDF file")
	}
	ctx, cancel := context.WithTimeout(ctx, lim.Timeout)
	defer cancel()
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return Result{}, fail(CodeBusy, "the PDF reader is busy; try again in a moment")
	}
	type out struct {
		r   Result
		err error
	}
	ch := make(chan out, 1)
	go func() {
		defer func() { <-slots }() // the slot is freed only when the parser really stops
		var o out
		func() {
			defer func() {
				if p := recover(); p != nil {
					o = out{err: fail(CodeInvalid, "the PDF could not be read (malformed or unsupported structure)")}
				}
			}()
			o.r, o.err = extract(ctx, data, pages, lim)
		}()
		ch <- o
	}()
	select {
	case o := <-ch:
		return o.r, o.err
	case <-ctx.Done():
		return Result{}, fail(CodeTimeout, "reading the PDF took longer than %s", lim.Timeout)
	}
}

func extract(ctx context.Context, data []byte, pages []int, lim Limits) (Result, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		if errors.Is(err, pdf.ErrInvalidPassword) || strings.Contains(strings.ToLower(err.Error()), "encrypt") {
			return Result{}, fail(CodeEncrypted, "the PDF is protected with a password")
		}
		return Result{}, fail(CodeInvalid, "the PDF could not be read (malformed or unsupported version)")
	}
	total := r.NumPage()
	if total <= 0 {
		return Result{}, fail(CodeInvalid, "the PDF declares no pages")
	}
	res := Result{Pages: total}
	want := pages
	if want == nil {
		n := min(total, lim.MaxPages)
		want = make([]int, n)
		for i := range want {
			want[i] = i + 1
		}
		res.Truncated = total > lim.MaxPages
	} else if len(want) > lim.MaxPages {
		want, res.Truncated = want[:lim.MaxPages], true
	}
	var b strings.Builder
	chars, meaningful := 0, 0
	for _, n := range want {
		if ctx.Err() != nil { // the caller already gave up: stop at a page boundary
			return Result{}, fail(CodeTimeout, "reading the PDF took too long")
		}
		if n < 1 || n > total {
			continue
		}
		text, perr := pageText(r, n)
		if perr != nil {
			text = ""
		}
		text = clean(text)
		m := meaningfulChars(text)
		meaningful += m
		res.PagesRead++
		if m < ThinPageChars {
			res.ImagePages = append(res.ImagePages, n)
		}
		head := "[page " + strconv.Itoa(n) + "]\n"
		if b.Len() > 0 {
			head = "\n\n" + head
		}
		b.WriteString(head)
		chars += utf8.RuneCountInString(head)
		if m < ThinPageChars {
			note := "(no text layer on this page: it is probably an image or a scan)"
			b.WriteString(note)
			chars += len(note)
		} else {
			left := lim.MaxChars - chars
			if utf8.RuneCountInString(text) > left {
				text = cutRunes(text, max(left, 0))
				res.Truncated = true
			}
			b.WriteString(text)
			chars += utf8.RuneCountInString(text)
		}
		if chars >= lim.MaxChars {
			res.Truncated = true
			break
		}
	}
	res.Text, res.Chars = b.String(), chars
	// Scanned: most pages read carry no usable text, or the whole document has
	// almost none (a few page numbers or a header line on every page).
	if res.PagesRead > 0 {
		res.Scanned = len(res.ImagePages)*2 > res.PagesRead || meaningful < ThinPageChars*res.PagesRead/2
	}
	return res, nil
}

func pageText(r *pdf.Reader, n int) (s string, err error) {
	defer func() {
		if p := recover(); p != nil {
			s, err = "", errors.New("page could not be read")
		}
	}()
	p := r.Page(n)
	if p.V.IsNull() {
		return "", nil
	}
	return p.GetPlainText(nil)
}

// clean keeps the layout (newlines, tabs) and drops control characters.
func clean(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == '\r':
			return '\n'
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

func meaningfulChars(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			n++
		}
	}
	return n
}

func cutRunes(s string, n int) string {
	i := 0
	for c := 0; c < n && i < len(s); c++ {
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
	}
	return s[:i]
}

// MaxRangePages bounds a page range ("1-500" is refused, not silently cut).
const MaxRangePages = 1000

// ParsePages parses "3", "3-5", "1,3,7-9" into sorted unique page numbers.
// "" returns nil (all pages). total > 0 bounds the pages to the document.
func ParsePages(spec string, total int) ([]int, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	seen := map[int]bool{}
	var out []int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil || a < 1 {
			return nil, fail(CodePages, "pages must look like 3, 3-5 or 1,3,7-9")
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil || b < a {
				return nil, fail(CodePages, "pages must look like 3, 3-5 or 1,3,7-9")
			}
		}
		if b-a >= MaxRangePages {
			return nil, fail(CodePages, "at most %d pages per range", MaxRangePages)
		}
		for p := a; p <= b; p++ {
			if total > 0 && p > total {
				break
			}
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
			if len(out) > MaxRangePages {
				return nil, fail(CodePages, "at most %d pages", MaxRangePages)
			}
		}
	}
	if len(out) == 0 {
		return nil, fail(CodePages, "no page in that range exists in the document")
	}
	slices.Sort(out)
	return out, nil
}

// PageCount returns the declared page count (bounded by the same guards as Extract).
func PageCount(ctx context.Context, data []byte, lim Limits) (int, error) {
	lim = lim.withDefaults()
	lim.MaxPages = 1
	r, err := Extract(ctx, data, []int{1}, lim)
	return r.Pages, err
}
