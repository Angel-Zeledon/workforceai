package docindex

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"aiworkforce/backend/internal/drive"
)

func prose(n int, word string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "Sentence number %d talks about %s and other matters of the business. ", i, word)
		if i%5 == 4 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

func TestSplitSizesOverlapAndOffsets(t *testing.T) {
	text := "# Introduction\n\n" + prose(60, "revenue") + "\n## Risks\n\n" + prose(40, "liquidity")
	cs := Split(text, ChunkOptions{}, 0)
	if len(cs) < 5 {
		t.Fatalf("expected several chunks, got %d", len(cs))
	}
	rs := []rune(text)
	for i, c := range cs {
		n := utf8.RuneCountInString(c.Text)
		if n > 1200 {
			t.Fatalf("chunk %d has %d chars (> max)", i, n)
		}
		if i < len(cs)-1 && n < 400 {
			t.Fatalf("chunk %d too small (%d)", i, n)
		}
		// offsets point at the chunk in the original text (character offsets)
		if got := strings.TrimSpace(string(rs[c.Start:c.End])); got != c.Text {
			t.Fatalf("chunk %d offsets do not match its text", i)
		}
		if c.Hash == "" || c.Line < 1 || c.Page != 0 {
			t.Fatalf("chunk %d: hash/line/page wrong: %+v", i, c)
		}
		if i > 0 && c.Start >= cs[i-1].End {
			t.Fatalf("chunk %d does not overlap the previous one", i)
		}
		if i > 0 && cs[i-1].End-c.Start > 300 {
			t.Fatalf("overlap too large: %d", cs[i-1].End-c.Start)
		}
	}
	// sections are carried as context
	last := cs[len(cs)-1]
	if last.Heading != "Risks" || cs[0].Heading != "Introduction" && cs[0].Heading != "" {
		t.Fatalf("headings: first %q last %q", cs[0].Heading, last.Heading)
	}
}

func TestSplitPagesFromMarkers(t *testing.T) {
	var b strings.Builder
	for p := 1; p <= 12; p++ {
		fmt.Fprintf(&b, "[page %d]\n", p)
		b.WriteString(prose(6, fmt.Sprintf("topic%d", p)))
		b.WriteString("\n")
	}
	cs := Split(b.String(), ChunkOptions{}, 0)
	seen := map[int]bool{}
	for _, c := range cs {
		if c.Page < 1 || c.Page > 12 {
			t.Fatalf("page out of range: %+v", c.Page)
		}
		if strings.Contains(c.Text, "[page") {
			t.Fatal("page markers must not be part of the chunk text")
		}
		seen[c.Page] = true
		// the page of a chunk is where it starts: its text mentions that page's topic
		if !strings.Contains(c.Text, fmt.Sprintf("topic%d ", c.Page)) {
			t.Fatalf("chunk says page %d but does not contain its text: %.80q", c.Page, c.Text)
		}
	}
	if len(seen) < 8 {
		t.Fatalf("pages seen: %v", seen)
	}
	if off := drive.PageOffset(b.String(), 7); off < 0 {
		t.Fatal("page 7 marker not found")
	}
}

func TestSplitTableRowsKeepHeader(t *testing.T) {
	var b strings.Builder
	b.WriteString("PRICE LIST\n\nproduct\tprice\tstock\n")
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&b, "item-%03d\t%d.50\t%d\n", i, i*3, i%7)
	}
	cs := Split(b.String(), ChunkOptions{Target: 600, Max: 800}, 0)
	if len(cs) < 3 {
		t.Fatalf("chunks: %d", len(cs))
	}
	for _, c := range cs[1:] {
		if !strings.Contains(c.Heading, "product\tprice\tstock") {
			t.Fatalf("a chunk starting mid-table must carry the header row, got %q", c.Heading)
		}
	}
	for _, c := range cs {
		for _, line := range strings.Split(c.Text, "\n") {
			if strings.HasPrefix(line, "item-") && strings.Count(line, "\t") != 2 {
				t.Fatalf("a table row was cut: %q", line)
			}
		}
	}
}

func TestSplitCapAndHugeLine(t *testing.T) {
	huge := strings.Repeat("word ", 5000) // one 25k-char line
	cs := Split(huge, ChunkOptions{}, 0)
	for _, c := range cs {
		if utf8.RuneCountInString(c.Text) > 1200 {
			t.Fatalf("hard split failed: %d", utf8.RuneCountInString(c.Text))
		}
	}
	if capped := Split(huge, ChunkOptions{}, 3); len(capped) != 3 {
		t.Fatalf("cap: %d", len(capped))
	}
	if len(Split("   \n\n  ", ChunkOptions{}, 0)) != 0 {
		t.Fatal("blank text has no chunks")
	}
}

func TestSnippetBoundedAndHighlighted(t *testing.T) {
	text := strings.Repeat("filler text here. ", 40) + "La facturación de marzo creció 12%. " + strings.Repeat("more filler. ", 40)
	s := Snippet(text, "facturacion marzo")
	if utf8.RuneCountInString(s) > SnippetMax {
		t.Fatalf("snippet too long: %d", utf8.RuneCountInString(s))
	}
	if !strings.Contains(s, "«facturación»") || !strings.Contains(s, "«marzo»") {
		t.Fatalf("not highlighted: %q", s)
	}
}

func TestFuseRRF(t *testing.T) {
	a, b, c, d := Key{"a", 1, 0}, Key{"b", 1, 0}, Key{"c", 1, 0}, Key{"d", 1, 0}
	got := Fuse(60, []Key{a, b, c}, []Key{c, d, a})
	// a: 1/61+1/63, c: 1/63+1/61 (tie, a first by first-list order), b: 1/62, d: 1/62
	if got[0].Key != a || got[1].Key != c {
		t.Fatalf("order: %+v", got)
	}
	if len(got) != 4 || got[0].Score <= got[2].Score {
		t.Fatalf("scores: %+v", got)
	}
}

func TestDetectLang(t *testing.T) {
	if l := DetectLang("El informe de la empresa muestra que las ventas del trimestre crecieron", "simple"); l != "spanish" {
		t.Fatal(l)
	}
	if l := DetectLang("The report of the company shows that the sales of the quarter grew", "simple"); l != "english" {
		t.Fatal(l)
	}
	if l := DetectLang("ventas", "english"); l != "english" {
		t.Fatal(l)
	}
}
