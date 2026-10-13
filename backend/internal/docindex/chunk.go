// Package docindex is the passage index of the drive (capability 6, "big
// files"; docs/architecture/agent-search.md). Files are cut into overlapping
// chunks that remember their page (from the "[page N]" markers of paged
// formats), line and section; chunks are searched by keyword (Postgres full
// text) and, when an embeddings provider is configured and allowed, by meaning
// (exact cosine in Go over a prefiltered candidate set), fused with reciprocal
// rank fusion. Every hit is a citation: file, version, page/line, snippet.
//
// The index is derived data: it can always be rebuilt from the drive, it only
// ever covers the CURRENT version of live files, and folder permissions are
// applied in the query (before ranking), never after.
package docindex

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"aiworkforce/backend/internal/drive"
)

// ChunkOptions sizes the chunks (characters).
type ChunkOptions struct {
	Target  int // a chunk is closed once it reaches this size (default 1000)
	Max     int // never larger than this, except the context line (default 1200)
	Overlap int // characters repeated from the end of the previous chunk (default 150)
}

func (o ChunkOptions) norm() ChunkOptions {
	if o.Target <= 0 {
		o.Target = 1000
	}
	if o.Max < o.Target {
		o.Max = o.Target + o.Target/5
	}
	if o.Overlap <= 0 || o.Overlap >= o.Target/2 {
		o.Overlap = 150
	}
	return o
}

// Chunk is a passage of a file version. Start/End are CHARACTER offsets into
// the extracted text (what fs.read takes as offset); Text excludes page markers.
type Chunk struct {
	Idx     int
	Page    int // 0 when the text has no page markers
	Line    int // 1-based line of the start
	Start   int
	End     int
	Heading string // section heading (+ table header when the chunk starts mid-table)
	Text    string
	Hash    string // content hash (reuse of embeddings across versions)
}

// unit is a span of the source text (byte offsets) that is never split unless
// it is longer than Max on its own.
type unit struct {
	start, end int
	heading    bool
	tableRow   bool
	blankAfter bool
	marker     bool // a "[page N]" marker
}

var (
	mdHeading  = regexp.MustCompile(`^#{1,6}\s+\S`)
	numHeading = regexp.MustCompile(`^(\d+(\.\d+)*\.?|[IVXLC]+\.)\s+\p{Lu}`)
	sentEnd    = regexp.MustCompile(`[.!?;:…][)"'»”]?\s+`)
)

// isHeading guesses whether a line is a section title.
func isHeading(line string) bool {
	l := strings.TrimSpace(line)
	if l == "" || utf8.RuneCountInString(l) > 100 {
		return false
	}
	if mdHeading.MatchString(l) {
		return true
	}
	if strings.ContainsAny(l[len(l)-1:], ".,;") {
		return false
	}
	if numHeading.MatchString(l) && utf8.RuneCountInString(l) <= 80 {
		return true
	}
	letters, upper := 0, 0
	for _, r := range l {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	return letters >= 4 && upper == letters // ALL CAPS TITLE
}

func isTableRow(line string) bool {
	return strings.Count(line, "\t") >= 1 || strings.Count(line, "|") >= 2 || strings.Count(line, ";") >= 3 || strings.Count(line, ",") >= 3 && !strings.Contains(line, ". ")
}

// units splits text into lines, and long prose lines into sentences.
func units(text string, maxLen int) []unit {
	var out []unit
	pos := 0
	for pos < len(text) {
		nl := strings.IndexByte(text[pos:], '\n')
		end := len(text)
		if nl >= 0 {
			end = pos + nl
		}
		line := text[pos:end]
		next := end + 1
		if strings.TrimSpace(line) != "" {
			blank := next < len(text) && strings.TrimSpace(lineAt(text, next)) == ""
			// page markers are units of their own: a chunk never spans two pages
			seg := pos
			for _, m := range drive.PageMarker().FindAllStringIndex(line, -1) {
				out = appendLine(out, text, seg, pos+m[0], false)
				out = append(out, unit{start: pos + m[0], end: pos + m[1], marker: true})
				seg = pos + m[1]
			}
			out = appendLine(out, text, seg, end, blank)
		}
		pos = next
	}
	// hard-split anything longer than maxLen at a space
	var res []unit
	for _, u := range out {
		for utf8.RuneCountInString(u.Text(text)) > maxLen {
			cut := splitPoint(text[u.start:u.end], maxLen)
			res = append(res, unit{start: u.start, end: u.start + cut, tableRow: u.tableRow})
			u.start += cut
		}
		res = append(res, u)
	}
	return res
}

// appendLine classifies the line text[pos:end] (or a part of it between page markers).
func appendLine(out []unit, text string, pos, end int, blank bool) []unit {
	line := text[pos:end]
	if strings.TrimSpace(line) == "" {
		return out
	}
	switch {
	case isHeading(line):
		return append(out, unit{start: pos, end: end, heading: true, blankAfter: blank})
	case isTableRow(line):
		return append(out, unit{start: pos, end: end, tableRow: true, blankAfter: blank})
	}
	// sentences of a prose line (kept whole when short)
	s := pos
	for _, m := range sentEnd.FindAllStringIndex(line, -1) {
		cut := pos + m[1]
		if cut-s >= 40 && cut < end {
			out = append(out, unit{start: s, end: cut})
			s = cut
		}
	}
	return append(out, unit{start: s, end: end, blankAfter: blank})
}

func (u unit) Text(text string) string { return text[u.start:u.end] }

func lineAt(text string, pos int) string {
	nl := strings.IndexByte(text[pos:], '\n')
	if nl < 0 {
		return text[pos:]
	}
	return text[pos : pos+nl]
}

// splitPoint returns a byte index <= maxLen runes, preferably after a space.
func splitPoint(s string, maxLen int) int {
	i, n := 0, 0
	for i < len(s) && n < maxLen {
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
		n++
	}
	if sp := strings.LastIndexAny(s[:i], " \t"); sp > i/2 {
		return sp + 1
	}
	return i
}

type pageMark struct{ at, page int }

func pageMarks(text string) []pageMark {
	var out []pageMark
	for _, m := range drive.PageMarker().FindAllStringSubmatchIndex(text, -1) {
		if p, err := strconv.Atoi(text[m[2]:m[3]]); err == nil {
			out = append(out, pageMark{at: m[0], page: p})
		}
	}
	return out
}

// Split cuts extracted text into chunks: paragraph and sentence aware,
// headings and table headers carried as context, page markers turned into the
// page of each chunk. maxChunks > 0 stops early (the caller reports the file
// as too large when the result reaches it).
func Split(text string, o ChunkOptions, maxChunks int) []Chunk {
	o = o.norm()
	us := units(text, o.Max)
	marks := pageMarks(text)
	paged := len(marks) > 0
	var chunks []Chunk
	// incremental byte -> rune / line conversion (offsets only grow, or step back by the overlap)
	conv := newOffsets(text)
	heading, tableHeader := "", ""
	inTable := false
	i := 0
	for i < len(us) {
		start := i
		size := 0
		ctxHeading := heading
		if us[i].tableRow && inTable && tableHeader != "" && tableHeader != strings.TrimSpace(us[i].Text(text)) {
			ctxHeading = joinCtx(heading, tableHeader)
		}
		j := i
		for j < len(us) {
			if us[j].marker && j > i {
				break // a new page starts a new chunk
			}
			n := utf8.RuneCountInString(us[j].Text(text)) + 1
			if size > 0 && size+n > o.Max {
				break
			}
			// a heading opens a new chunk when the current one is already useful
			if us[j].heading && size >= o.Target/2 {
				break
			}
			size += n
			j++
			if size >= o.Target {
				break
			}
		}
		// track context across the units consumed
		for k := start; k < j; k++ {
			u := us[k]
			switch {
			case u.marker: // a page break does not end a section or a table
			case u.heading:
				heading = strings.TrimSpace(strings.TrimLeft(u.Text(text), "# "))
				inTable, tableHeader = false, ""
			case u.tableRow:
				if !inTable {
					inTable, tableHeader = true, strings.TrimSpace(u.Text(text))
				}
			default:
				inTable, tableHeader = false, ""
			}
			if u.blankAfter && !u.tableRow {
				inTable, tableHeader = false, ""
			}
		}
		bs, be := us[start].start, us[j-1].end
		body := stripMarks(text[bs:be])
		if strings.TrimSpace(body) != "" {
			c := Chunk{Idx: len(chunks), Start: conv.runes(bs), End: conv.runes(be), Line: conv.line(bs), Heading: clip(ctxHeading, 300),
				Text: clip(strings.TrimSpace(body), 3800)}
			if paged {
				c.Page = pageAt(marks, bs, be)
			}
			c.Hash = hashOf(c.Heading, c.Text)
			chunks = append(chunks, c)
			if maxChunks > 0 && len(chunks) >= maxChunks {
				return chunks
			}
		}
		if j >= len(us) {
			break
		}
		if us[j].marker {
			i = j // no overlap across pages: a citation names one page
			continue
		}
		// overlap: restart at the last units that fit in o.Overlap (sentence aware)
		next, ov := j, 0
		for next-1 > start {
			n := utf8.RuneCountInString(us[next-1].Text(text)) + 1
			if ov+n > o.Overlap || us[next-1].heading {
				break
			}
			ov += n
			next--
		}
		i = next
	}
	return chunks
}

// pageAt is the page of a chunk: the last marker at or before its start; when
// the chunk starts before any marker but contains one, that marker's page;
// text before the first marker is page 1.
func pageAt(marks []pageMark, bs, be int) int {
	p := 0
	for _, m := range marks {
		if m.at <= bs {
			p = m.page
			continue
		}
		if p == 0 && m.at < be && m.at-bs < 16 {
			return m.page // the chunk begins right before the first marker
		}
		break
	}
	if p == 0 {
		return 1
	}
	return p
}

func stripMarks(s string) string {
	return drive.PageMarker().ReplaceAllString(s, "")
}

func joinCtx(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + " | " + b
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func hashOf(heading, text string) string {
	h := sha256.Sum256([]byte(heading + "\x00" + text))
	return hex.EncodeToString(h[:16])
}

// offsets converts byte offsets to rune offsets and line numbers, caching the
// last position so that a forward walk is linear.
type offsets struct {
	text     string
	b, r, ln int
}

func newOffsets(text string) *offsets { return &offsets{text: text, ln: 1} }

func (o *offsets) seek(b int) {
	for o.b > b {
		c, w := utf8.DecodeLastRuneInString(o.text[:o.b])
		if c == '\n' {
			o.ln--
		}
		o.b -= w
		o.r--
	}
	for o.b < b && o.b < len(o.text) {
		c, w := utf8.DecodeRuneInString(o.text[o.b:])
		if c == '\n' {
			o.ln++
		}
		o.b += w
		o.r++
	}
}

func (o *offsets) runes(b int) int { o.seek(b); return o.r }
func (o *offsets) line(b int) int  { o.seek(b); return o.ln }
