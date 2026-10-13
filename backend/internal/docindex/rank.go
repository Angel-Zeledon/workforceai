package docindex

import (
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"aiworkforce/backend/internal/textfold"
)

// Key identifies a chunk.
type Key struct {
	FileID  string
	Version int
	Idx     int
}

// Cosine is the cosine similarity of two vectors (0 when either is empty or
// the dimensions differ).
func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// RRFK is the usual constant of reciprocal rank fusion.
const RRFK = 60

// Fuse combines ranked lists with reciprocal rank fusion: score(d) = sum over
// the lists of 1/(k + rank). Ties keep the order of the first list.
func Fuse(k int, lists ...[]Key) []Scored {
	if k <= 0 {
		k = RRFK
	}
	score := map[Key]float64{}
	first := map[Key]int{}
	n := 0
	for _, l := range lists {
		for r, key := range l {
			score[key] += 1 / float64(k+r+1)
			if _, ok := first[key]; !ok {
				first[key] = n
				n++
			}
		}
	}
	out := make([]Scored, 0, len(score))
	for key, s := range score {
		out = append(out, Scored{Key: key, Score: s})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return first[out[i].Key] < first[out[j].Key]
	})
	return out
}

// Scored is a chunk with a score.
type Scored struct {
	Key   Key
	Score float64
}

// TopCosine ranks candidates by similarity to q and keeps the best k (only
// positive similarities).
func TopCosine(q []float32, cands []Candidate, k int) []Scored {
	out := make([]Scored, 0, len(cands))
	for _, c := range cands {
		if s := Cosine(q, c.Vec); s > 0 {
			out = append(out, Scored{Key: c.Key, Score: s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out
}

// queryTerms are the folded words of a query (for snippets).
func queryTerms(q string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(textfold.Fold(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if utf8.RuneCountInString(w) < 2 || stop[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// SnippetMax is the length limit of a snippet (characters).
const SnippetMax = 300

// Snippet picks a window of at most SnippetMax characters around the first
// matched query word and marks the matches with «». Matching is accent and
// case insensitive and also accepts word prefixes (stemming-like).
func Snippet(text, query string) string {
	text = strings.Join(strings.Fields(text), " ")
	rs := []rune(text)
	terms := queryTerms(query)
	// fold rune by rune so that indexes keep matching the original text
	folded := make([]string, len(rs))
	for i, r := range rs {
		folded[i] = textfold.Fold(string(r))
	}
	type span struct{ a, b int }
	var spans []span
	i := 0
	for i < len(rs) {
		if !isWordRune(rs[i]) || i > 0 && isWordRune(rs[i-1]) {
			i++
			continue
		}
		j := i
		for j < len(rs) && isWordRune(rs[j]) {
			j++
		}
		w := strings.Join(folded[i:j], "")
		for _, t := range terms {
			if strings.HasPrefix(w, stemOf(t)) {
				spans = append(spans, span{i, j})
				break
			}
		}
		i = j
	}
	start := 0
	if len(spans) > 0 && spans[0].a > SnippetMax/3 {
		start = spans[0].a - SnippetMax/3
		for start > 0 && isWordRune(rs[start-1]) {
			start++
		}
	}
	// reserve room for the markers so the result never exceeds SnippetMax
	budget := SnippetMax - 2
	if start > 0 {
		budget--
	}
	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	n := 0
	k := 0
	for p := start; p < len(rs); p++ {
		for k < len(spans) && spans[k].b <= p {
			k++
		}
		open := k < len(spans) && spans[k].a == p
		cost := 1
		if open {
			cost = 2 + (spans[k].b - spans[k].a)
		}
		if n+cost > budget {
			b.WriteString("…")
			break
		}
		if open {
			b.WriteString("«" + string(rs[spans[k].a:spans[k].b]) + "»")
			n += cost
			p = spans[k].b - 1
			continue
		}
		b.WriteRune(rs[p])
		n++
	}
	return b.String()
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// stemOf is a crude prefix stem for highlighting only (FTS does the real one).
func stemOf(t string) string {
	r := []rune(t)
	switch {
	case len(r) > 7:
		return string(r[:len(r)-3])
	case len(r) > 4:
		return string(r[:len(r)-2])
	}
	return t
}

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`de la el en y a los las del un una que por con para se su al lo es como mas o
		the of and to in a is for on that by with as at an be or from this are it was
		do da os as em um uma no na`) {
		stop[w] = true
	}
}

var langWords = map[string][]string{
	"spanish":    strings.Fields("de la que el en los las del por con para una es se al lo como mas pero sus este esta entre cuando todo"),
	"english":    strings.Fields("the of and to in is for that with as on by this are be from was it not or have an which"),
	"portuguese": strings.Fields("de que o a do da em os as para com uma no na por mais dos das se ao como foi ou seu sua"),
}

// DetectLang returns the Postgres text search configuration that fits the text
// best ("spanish", "english", "portuguese"), or fallback when it cannot tell.
func DetectLang(text, fallback string) string {
	if utf8.RuneCountInString(text) > 4000 {
		text = string([]rune(text)[:4000])
	}
	counts := map[string]int{}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) })
	set := map[string]map[string]bool{}
	for l, ws := range langWords {
		set[l] = map[string]bool{}
		for _, w := range ws {
			set[l][w] = true
		}
	}
	for _, w := range words {
		for l := range set {
			if set[l][w] {
				counts[l]++
			}
		}
	}
	best, bn, second := "", 0, 0
	for _, l := range []string{"spanish", "english", "portuguese"} {
		switch c := counts[l]; {
		case c > bn:
			second, best, bn = bn, l, c
		case c > second:
			second = c
		}
	}
	if bn >= 2 && bn > second {
		return best
	}
	return fallback
}

// LangOfLocale maps an organization locale to a text search configuration.
func LangOfLocale(loc string) string {
	switch {
	case strings.HasPrefix(loc, "en"):
		return "english"
	case strings.HasPrefix(loc, "pt"):
		return "portuguese"
	case strings.HasPrefix(loc, "es"):
		return "spanish"
	}
	return "simple"
}

// ValidLang guards the configuration names that reach SQL.
func ValidLang(l string) string {
	switch l {
	case "spanish", "english", "portuguese", "simple":
		return l
	}
	return "simple"
}
