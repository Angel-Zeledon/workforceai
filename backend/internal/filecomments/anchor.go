// Package filecomments is F7: comments anchored to a passage of a document of the Drive, and the request "change
// this passage" that goes to an agent through the normal edit path (docs/architecture/anchored-comments.md).
//
// An anchor keeps the exact quote, a little context before and after, the version of the file it was made on and the
// offsets there. When the file changes, the anchor is located again by the quote and the context (Locate). If the
// quote is gone the comment is not lost: it stays with the status "lost" and the interface says the text changed.
package filecomments

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Status of an anchor against a text.
const (
	StatusIntact = "intact" // the quote is where it was made
	StatusMoved  = "moved"  // the quote is elsewhere (text was added or removed around it)
	StatusLost   = "lost"   // the quote no longer exists: the passage was rewritten
)

const (
	// MaxQuoteRunes bounds the quoted passage; ContextRunes the text kept before and after it.
	MaxQuoteRunes = 2000
	ContextRunes  = 48
)

// Anchor is where a comment points: an exact passage, its neighbourhood and the version it was made on. Start and End
// are rune offsets in the text of that version (of the raw file text, markup included).
type Anchor struct {
	Quote   string `json:"quote"`
	Prefix  string `json:"prefix"`
	Suffix  string `json:"suffix"`
	Version int    `json:"version"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
}

// Location says where an anchor is in a text now. Start and End are -1 when it is lost.
type Location struct {
	Status string `json:"status"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
}

// folded is a text reduced for comparison: whitespace runs are one space and the markup that a rendered page does not
// show (emphasis, headings, quotes, list bullets, link syntax) is dropped. idx[i] is the rune offset in the original
// of folded rune i. The quote selected in a rendered page and the raw file text meet in this form.
type folded struct {
	r   []rune
	idx []int
}

func isMarkup(r rune) bool {
	switch r {
	case '*', '_', '`', '#', '>', '~', '|', '\\', '[', ']':
		return true
	}
	return false
}

func fold(text string) folded {
	src := []rune(text)
	f := folded{r: make([]rune, 0, len(src)), idx: make([]int, 0, len(src))}
	lineStart := true
	for i := 0; i < len(src); i++ {
		r := src[i]
		switch {
		case r == '\n':
			lineStart = true
			f.space(i)
		case unicode.IsSpace(r) || r == 0xA0:
			f.space(i)
		case lineStart && (r == '-' || r == '+') && i+1 < len(src) && src[i+1] == ' ':
			// a list bullet: the page shows no "-" there
		case r == ']' && i+1 < len(src) && src[i+1] == '(':
			// the url of a link: the page shows only its text
			j := i + 2
			for j < len(src) && src[j] != ')' && src[j] != '\n' {
				j++
			}
			if j < len(src) && src[j] == ')' {
				i = j
			}
		case isMarkup(r):
			// dropped; a "> " or "## " prefix does not end the start of the line
			if r != '#' && r != '>' {
				lineStart = false
			}
		default:
			lineStart = false
			f.r = append(f.r, r)
			f.idx = append(f.idx, i)
		}
	}
	lo, hi := 0, len(f.r)
	for lo < hi && f.r[lo] == ' ' {
		lo++
	}
	for hi > lo && f.r[hi-1] == ' ' {
		hi--
	}
	f.r, f.idx = f.r[lo:hi], f.idx[lo:hi]
	return f
}

func (f *folded) space(at int) {
	if n := len(f.r); n == 0 || f.r[n-1] == ' ' {
		return
	}
	f.r = append(f.r, ' ')
	f.idx = append(f.idx, at)
}

// occurrences returns the folded start offsets of q in f (at most 200).
func (f folded) occurrences(q []rune) []int {
	if len(q) == 0 || len(q) > len(f.r) {
		return nil
	}
	hay, needle := string(f.r), string(q)
	var out []int
	from, runeAt, last := 0, 0, 0 // byte offsets and the rune count up to last
	for len(out) < 200 {
		i := strings.Index(hay[from:], needle)
		if i < 0 {
			break
		}
		pos := from + i
		runeAt += utf8.RuneCountInString(hay[last:pos])
		last = pos
		out = append(out, runeAt)
		_, w := utf8.DecodeRuneInString(hay[pos:])
		from = pos + w
	}
	return out
}

// span maps a folded range back to the original text: [Start, End).
func (f folded) span(s, e int) (int, int) { return f.idx[s], f.idx[e-1] + 1 }

func runeSlice(rs []rune, from, to int) string {
	from, to = max(from, 0), min(to, len(rs))
	if from >= to {
		return ""
	}
	return string(rs[from:to])
}

// MakeAnchor builds the anchor of quote (as selected in the viewer) in text, the text of version. The quote must exist
// in the text, markup aside; the offsets, prefix and suffix come from the text, not from the client.
func MakeAnchor(text, quote string, version int) (Anchor, error) {
	quote = strings.TrimSpace(strings.ToValidUTF8(quote, ""))
	if quote == "" || utf8.RuneCountInString(quote) > MaxQuoteRunes {
		return Anchor{}, fmt.Errorf("the quote needs 1 to %d characters", MaxQuoteRunes)
	}
	f := fold(text)
	q := fold(quote).r
	occ := f.occurrences(q)
	if len(occ) == 0 {
		return Anchor{}, fmt.Errorf("the quote is not in the text of the version")
	}
	s, e := f.span(occ[0], occ[0]+len(q))
	rs := []rune(text)
	return Anchor{
		Quote:   quote,
		Prefix:  strings.TrimSpace(runeSlice(rs, s-ContextRunes, s)),
		Suffix:  strings.TrimSpace(runeSlice(rs, e, e+ContextRunes)),
		Version: version, Start: s, End: e,
	}, nil
}

// Locate finds the anchor in text. Among several equal passages it picks the one whose context matches best and, on a
// tie, the closest to where the anchor was made.
func Locate(text string, a Anchor) Location {
	f := fold(text)
	q := fold(a.Quote).r
	occ := f.occurrences(q)
	if len(occ) == 0 {
		return Location{Status: StatusLost, Start: -1, End: -1}
	}
	pre, suf := fold(a.Prefix).r, fold(a.Suffix).r
	best, bestScore, bestDist := -1, -1, 0
	for _, o := range occ {
		score := commonSuffix(trimSpaceRight(f.r[:o]), pre) + commonPrefix(trimSpaceLeft(f.r[o+len(q):]), suf)
		s, _ := f.span(o, o+len(q))
		dist := s - a.Start
		if dist < 0 {
			dist = -dist
		}
		if score > bestScore || (score == bestScore && dist < bestDist) {
			best, bestScore, bestDist = o, score, dist
		}
	}
	s, e := f.span(best, best+len(q))
	st := StatusMoved
	if s == a.Start && e == a.End {
		st = StatusIntact
	}
	return Location{Status: st, Start: s, End: e}
}

func commonSuffix(a, b []rune) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

func commonPrefix(a, b []rune) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func trimSpaceRight(a []rune) []rune {
	for len(a) > 0 && a[len(a)-1] == ' ' {
		a = a[:len(a)-1]
	}
	return a
}

func trimSpaceLeft(a []rune) []rune {
	for len(a) > 0 && a[0] == ' ' {
		a = a[1:]
	}
	return a
}
