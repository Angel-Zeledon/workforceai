package memory

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/sanitize"
	"aiworkforce/backend/internal/textfold"
)

// FTS is the full-text Retriever: the documents (task outputs, reports, notes)
// and the drive files the agent may read, both through Postgres full-text
// search (Spanish, accent-insensitive), merged into one ranking.
type FTS struct {
	Docs  DocStore
	Files FileSource // nil: no drive
	// Now is the clock (tests).
	Now func() time.Time
}

var _ Retriever = (*FTS)(nil)

// Retrieve implements Retriever.
func (f *FTS) Retrieve(ctx context.Context, org string, q Query) ([]Item, error) {
	terms := q.Terms
	if len(terms) == 0 {
		terms = textfold.Terms(q.Text, maxQueryTerms)
	}
	if len(terms) == 0 || q.AgentID == "" {
		return []Item{}, nil
	}
	limit := q.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var items []Item
	hits, err := f.Docs.Search(ctx, org, SearchParams{Terms: terms, AgentID: q.AgentID, ExcludeRequest: q.ExcludeRequest, OnlyAgent: q.OnlyAgent, Limit: limit * 2})
	if err != nil {
		return nil, err
	}
	for _, h := range hits {
		items = append(items, docItem(h, terms))
	}
	if f.Files != nil && !q.OnlyAgent {
		// A failing drive leg must not hide the documents: files are simply left out.
		if fi, err := f.Files.Files(ctx, org, q.AgentID, terms, limit*2); err == nil {
			items = append(items, fi...)
		}
	}
	now := time.Now()
	if f.Now != nil {
		now = f.Now()
	}
	return Rank(items, q, now, limit), nil
}

func docItem(h DocHit, terms []string) Item {
	d := h.Doc
	return Item{ID: d.ID, Ref: d.Ref, Kind: d.Kind, Title: d.Title, Snippet: Passage(d.Body, terms, MaxSnippetChars), AgentID: d.AgentID,
		ProjectID: d.ProjectID, Project: d.ProjectName, SourceRef: d.SourceRef, At: d.UpdatedAt, Score: h.Score, Deletable: true}
}

// DriveFiles is the FileSource over the organization's drive: it searches only
// the folders the agent may read, with the exact scope of fs.search.
type DriveFiles struct {
	FS *drive.AgentFS
}

var _ FileSource = (*DriveFiles)(nil)

// Files implements FileSource.
func (d *DriveFiles) Files(ctx context.Context, org, agentID string, terms []string, limit int) ([]Item, error) {
	roots, err := d.FS.ReadableRoots(ctx, agentID)
	if err != nil {
		return nil, err // unknown scope: nothing readable
	}
	if roots == nil {
		roots = []string{}
	}
	hits, err := d.FS.Svc.Retrieve(ctx, drive.RetrieveQuery{Terms: terms, Under: roots, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(hits))
	for _, h := range hits {
		out = append(out, Item{Ref: "file:" + h.Path, Kind: KindFile, Title: h.Name, Path: h.Path, Snippet: Passage(h.Snippet, terms, MaxSnippetChars),
			AgentID: agentIDOf(h.UpdatedBy), Score: h.Score, Foreign: h.UpdatedBy != drive.AgentP(agentID)})
	}
	return out, nil
}

func agentIDOf(p drive.Principal) string {
	if p.IsAgent() {
		return p.ID
	}
	return ""
}

// Rank orders the items of one query: the text score, a bonus for the same
// project (or its drive folder), a small bonus for explicit notes and a mild
// preference for recent work. Items far weaker than the best one are dropped
// (a single common word must not drag in noise), as are duplicates.
func Rank(items []Item, q Query, now time.Time, limit int) []Item {
	best := 0.0
	for i := range items {
		it := &items[i]
		s := it.Score
		if q.PreferProject != "" && it.ProjectID == q.PreferProject ||
			q.PreferPath != "" && it.Path != "" && drive.IsUnder(it.Path, q.PreferPath) {
			s *= 1.4
		}
		switch it.Kind {
		case KindNote:
			s *= 1.15
		case KindFile:
			s *= 0.95
		}
		if !it.At.IsZero() {
			age := math.Max(0, now.Sub(it.At).Hours()/24)
			s *= 1 + 0.15*math.Exp(-age/90)
		}
		it.Score = s
		best = math.Max(best, s)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		return items[i].Ref < items[j].Ref
	})
	out := make([]Item, 0, min(limit, len(items)))
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.Ref] || it.Score < 0.3*best {
			continue
		}
		seen[it.Ref] = true
		out = append(out, it)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// Multi combines several retrievers (full-text today; full-text plus a
// semantic one over pgvector later) with reciprocal-rank fusion, so scores of
// different kinds never have to be comparable. One retriever failing does not
// hide the others.
type Multi struct {
	Retrievers []Retriever
}

var _ Retriever = (*Multi)(nil)

// Retrieve implements Retriever.
func (m *Multi) Retrieve(ctx context.Context, org string, q Query) ([]Item, error) {
	const k = 60.0
	score := map[string]float64{}
	byRef := map[string]Item{}
	var firstErr error
	ok := 0
	for _, r := range m.Retrievers {
		items, err := r.Retrieve(ctx, org, q)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ok++
		for i, it := range items {
			score[it.Ref] += 1 / (k + float64(i+1))
			if _, seen := byRef[it.Ref]; !seen {
				byRef[it.Ref] = it
			}
		}
	}
	if ok == 0 && firstErr != nil {
		return nil, firstErr
	}
	out := make([]Item, 0, len(byRef))
	for ref, it := range byRef {
		it.Score = score[ref]
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Ref < out[j].Ref
	})
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

// Passage picks the most relevant stretch of a text: the line or sentence group
// with the most of the query terms (the start of the text when none matches),
// cut to max characters.
func Passage(text string, terms []string, max int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	best, bestN := "", -1
	for _, p := range pieces(text, max) {
		fp := textfold.Fold(p)
		n := 0
		for _, t := range terms {
			if strings.Contains(fp, t) {
				n++
			}
		}
		if n > bestN {
			best, bestN = p, n
		}
	}
	return cutRunes(strings.Join(strings.Fields(best), " "), max)
}

// pieces splits a text into windows of about max characters on line and
// sentence boundaries.
func pieces(text string, max int) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for len([]rune(line)) > max {
			r := []rune(line)
			cut := max
			for i := max; i > max/2; i-- {
				if r[i-1] == '.' || r[i-1] == ';' {
					cut = i
					break
				}
			}
			out = append(out, string(r[:cut]))
			line = strings.TrimSpace(string(r[cut:]))
		}
		out = append(out, line)
	}
	// Neighbouring short lines read better together.
	var merged []string
	for _, p := range out {
		if n := len(merged); n > 0 && len([]rune(merged[n-1]))+len([]rune(p)) < max {
			merged[n-1] += " " + p
			continue
		}
		merged = append(merged, p)
	}
	return merged
}

func cutRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max-1])) + "…"
}

// Clean makes text from the index safe to show a model or a person: invisible
// and control characters removed, secrets redacted, delimiter forgery
// neutralized, one line.
func Clean(s string, sus *sanitize.Suspects) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t' || r == '\r':
			return ' '
		case unicode.Is(unicode.Cf, r), unicode.Is(unicode.Cc, r), r == 0x2028, r == 0x2029:
			return -1
		}
		return r
	}, s)
	s = sanitize.RedactSecrets(s, sus)
	s = sanitize.Neutralize(s)
	return strings.Join(strings.Fields(s), " ")
}
