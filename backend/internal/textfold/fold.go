// Package textfold is the accent-insensitive text normalization shared by the
// full-text indexes (drive files, long-term memory). It mirrors, rune for
// rune, the SQL function mem_fold() of migration 550, so what is indexed and
// what is queried fold the same way ("planeación" == "planeacion", "Año" == "ano").
package textfold

import (
	"sort"
	"strings"
	"unicode"
)

const (
	from = "ÁÀÄÂÃáàäâãÉÈËÊéèëêÍÌÏÎíìïîÓÒÖÔÕóòöôõÚÙÜÛúùüûÑñÇç"
	to   = "AAAAAaaaaaEEEEeeeeIIIIiiiiOOOOOoooooUUUUuuuuNnCc"
)

var table = func() map[rune]rune {
	f, t := []rune(from), []rune(to)
	m := make(map[rune]rune, len(f))
	for i := range f {
		m[f[i]] = t[i]
	}
	return m
}()

// Fold lowercases, removes the accents of Spanish text and the thousands
// separators of numbers. Apart from the separators it keeps the rune count.
func Fold(s string) string {
	return joinNumbers(strings.Map(func(r rune) rune {
		if m, ok := table[r]; ok {
			r = m
		}
		return unicode.ToLower(r)
	}, s))
}

// stop are words that carry no topic (Spanish and English). Only used to pick
// query terms; the database drops its own stop words again.
var stop = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`
		el la los las un una unos unas de del al y o u e a en con por para sin sobre entre desde hasta que se su sus lo le les
		mi mis tu tus es son ser fue han hay como mas pero si no ya muy este esta estos estas ese esa eso nos nuestro nuestra
		the a an of to in on for and or with from by at is are was were be been it its this that these those as we our you your
		tarea solicitud necesito quiero favor puedes hacer hazme preparar`) {
		m[w] = true
	}
	return m
}()

// Terms extracts up to max distinct, folded, searchable words from free text,
// in order of first appearance (callers put the most specific text first).
// Words are made only of letters and digits, so they are safe to put in a
// tsquery. Numbers are kept and thousands separators are removed ("$50,000"
// and "50000" are the same term).
func Terms(text string, max int) []string {
	text = Fold(text)
	words := strings.FieldsFunc(text, func(r rune) bool { return !(unicode.IsLetter(r) || unicode.IsDigit(r)) })
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		if len(w) < 2 || len(w) < 3 && !isNumber(w) || stop[w] || seen[w] || !asciiWord(w) {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

// joinNumbers removes thousands separators between digits: "50,000" -> "50000",
// "1.234.567" -> "1234567". SQL mem_fold() does the same, so a figure matches
// however it was written.
func joinNumbers(s string) string {
	r := []rune(s)
	var b strings.Builder
	for i := 0; i < len(r); i++ {
		if (r[i] == ',' || r[i] == '.') && i > 0 && unicode.IsDigit(r[i-1]) && i+3 < len(r) &&
			unicode.IsDigit(r[i+1]) && unicode.IsDigit(r[i+2]) && unicode.IsDigit(r[i+3]) &&
			(i+4 >= len(r) || !unicode.IsDigit(r[i+4])) {
			continue
		}
		b.WriteRune(r[i])
	}
	return b.String()
}

func isNumber(w string) bool {
	for _, r := range w {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return w != ""
}

func asciiWord(w string) bool {
	for _, r := range w {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// Sorted returns a sorted copy (stable output for tests and cache keys).
func Sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
