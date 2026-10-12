// Package demodata generates the sample datasets ("usar datos de ejemplo") that
// stand in for the inputs of the large built-in project templates (H1).
//
// Everything here is FICTIONAL and DETERMINISTIC: the same seed gives the same
// bytes, and the numbers reconcile (a trial balance balances, the intercompany
// balances of two subsidiaries mirror each other once converted at the closing
// rate, a bank statement ends at its closing balance...). The tests prove it by
// re-reading the generated files, not by trusting the generator.
package demodata

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
)

// DefaultSeed is the seed of every dataset unless a caller overrides it.
const DefaultSeed int64 = 20260930

// Params describe the project the data is for.
type Params struct {
	Locale string // "es" (default) or "en"
	Seed   int64
	// Count holds the numeric / list-size parameters of the template by key
	// (e.g. subsidiaries: 3) and Lists the items of its list parameters.
	Count map[string]int
	Lists map[string][]string
}

func (p Params) en() bool { return strings.HasPrefix(p.Locale, "en") }

func (p Params) seed() int64 {
	if p.Seed != 0 {
		return p.Seed
	}
	return DefaultSeed
}

func (p Params) count(key string, def, lo, hi int) int {
	n, ok := p.Count[key]
	if !ok {
		n = def
	}
	return min(max(n, lo), hi)
}

// list returns the items of a list parameter (or the defaults).
func (p Params) list(key string, def ...string) []string {
	if l := p.Lists[key]; len(l) > 0 {
		return l
	}
	return def
}

func (p Params) tr(es, en string) string {
	if p.en() {
		return en
	}
	return es
}

// File is one generated file. Input is the key of the template input it
// belongs to ("" for a companion file such as the README).
type File struct {
	Input string
	Name  string
	Data  []byte
}

// Set is a generated dataset: files plus the values of the non-file inputs.
type Set struct {
	Files  []File
	Values map[string]string
}

type generator func(p Params) Set

var registry = map[string]generator{}

func register(name string, g generator) { registry[name] = g }

// Has reports whether a dataset exists.
func Has(name string) bool { _, ok := registry[name]; return ok }

// Names lists the datasets.
func Names() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Generate builds a dataset.
func Generate(name string, p Params) (Set, error) {
	g, ok := registry[name]
	if !ok {
		return Set{}, fmt.Errorf("unknown demo dataset %q", name)
	}
	s := g(p)
	for i := range s.Files {
		if len(s.Files[i].Data) == 0 {
			return Set{}, fmt.Errorf("demo dataset %q produced an empty file %q", name, s.Files[i].Name)
		}
	}
	return s, nil
}

// ---- helpers ----

func newRNG(p Params, salt string) *rand.Rand {
	h := int64(1469598103934665603)
	for _, c := range salt {
		h = (h ^ int64(c)) * 1099511628211
	}
	return rand.New(rand.NewSource(p.seed() ^ h))
}

// cents is an amount in minor units (never a float: sums are exact).
type cents = int64

func fmtC(c cents) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%s%d.%02d", sign, c/100, c%100)
}

func round6(f float64) float64 { return math.Round(f*1e6) / 1e6 }

func fmtRate(f float64) string { return fmt.Sprintf("%.6f", f) }

// conv converts cents at a rate (local per USD) to local cents.
func conv(c cents, rate float64) cents { return cents(math.Round(float64(c) * rate)) }

// between returns a value in [lo, hi] (inclusive) from the rng.
func between(r *rand.Rand, lo, hi cents) cents { return lo + cents(r.Int63n(int64(hi-lo)+1)) }

// frac is c * f rounded to cents.
func frac(c cents, f float64) cents { return cents(math.Round(float64(c) * f)) }

// csvb builds a CSV document.
type csvb struct{ b strings.Builder }

func (c *csvb) row(fields ...string) {
	for i, f := range fields {
		if i > 0 {
			c.b.WriteByte(',')
		}
		if strings.ContainsAny(f, ",\"\n\r") {
			f = `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
		}
		c.b.WriteString(f)
	}
	c.b.WriteByte('\n')
}

func (c *csvb) bytes() []byte { return []byte(c.b.String()) }

func file(input, name string, data []byte) File { return File{Input: input, Name: name, Data: data} }

func md(input, name, text string) File { return File{Input: input, Name: name, Data: []byte(text)} }

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case strings.ContainsRune("áàä", r):
			b.WriteByte('a')
		case strings.ContainsRune("éèë", r):
			b.WriteByte('e')
		case strings.ContainsRune("íìï", r):
			b.WriteByte('i')
		case strings.ContainsRune("óòö", r):
			b.WriteByte('o')
		case strings.ContainsRune("úùü", r):
			b.WriteByte('u')
		case r == 'ñ':
			b.WriteByte('n')
		default:
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
				b.WriteByte('_')
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

const fictionalES = "Datos FICTICIOS generados para practicar: no corresponden a ninguna empresa real."
const fictionalEN = "FICTIONAL data generated for practice: it does not correspond to any real company."

func (p Params) fiction() string { return p.tr(fictionalES, fictionalEN) }
