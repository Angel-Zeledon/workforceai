package drive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Passage search (capability 6, docs/architecture/agent-search.md): the drive
// is cut into chunks by internal/docindex and searched by keyword (Postgres
// full text) and, when an embeddings provider is allowed, by meaning. The drive
// only declares the contract here so that internal/docindex can import the
// drive (and not the other way around).

// Search modes of fs.search.
const (
	SearchKeyword  = "keyword"
	SearchSemantic = "semantic"
	SearchHybrid   = "hybrid"
)

// ErrIndexOff: the organization turned the index off (or it is not built yet
// for anything readable). Callers fall back to the file-level search.
var ErrIndexOff = errors.New("doc index is off")

// ChunkQuery is a passage search.
type ChunkQuery struct {
	Query string
	// Under is the set of readable folder prefixes. nil = everything (a person
	// with files:read); an empty non-nil slice = nothing readable.
	Under []string
	// Mode is keyword | semantic | hybrid ("" = hybrid when available).
	Mode  string
	Limit int
	// Locale picks the stemming language when the query is too short to tell.
	Locale string
}

// ChunkHit is one cited passage. Start/End are character offsets in the
// extracted text of that version (what fs.read offset expects).
type ChunkHit struct {
	FileID    string    `json:"file_id"`
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	Version   int       `json:"version"`
	Page      int       `json:"page,omitempty"` // 0 = the format has no pages
	Line      int       `json:"line,omitempty"`
	Start     int       `json:"start"`
	End       int       `json:"end"`
	Heading   string    `json:"heading,omitempty"`
	Snippet   string    `json:"snippet"` // <= 300 chars, «» around matched words
	Score     float64   `json:"score"`
	UpdatedBy Principal `json:"updated_by"`
}

// ChunkResult is the answer of a passage search.
type ChunkResult struct {
	Hits []ChunkHit `json:"hits"`
	// Mode is the mode that actually ran (hybrid can degrade to keyword).
	Mode string `json:"mode"`
	// Note explains a degradation in plain English (no embeddings configured,
	// data policy forbids sending text to the provider, daily cap reached...).
	Note string `json:"note,omitempty"`
}

// ChunkSearcher is implemented by internal/docindex.
type ChunkSearcher interface {
	SearchChunks(ctx context.Context, q ChunkQuery) (ChunkResult, error)
}

// FullText extracts the whole text of a version (0 = current) up to maxChars,
// exactly like ReadText does (same Extract), for the indexer.
func (s *Service) FullText(ctx context.Context, f File, version, maxChars int) (Extracted, Version, error) {
	if maxChars <= 0 {
		maxChars = readExtractCap
	}
	rc, v, err := s.Open(ctx, f, version)
	if err != nil {
		return Extracted{}, Version{}, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, MaxExtractBytes+1))
	if err != nil {
		return Extracted{}, Version{}, err
	}
	return Extract(f.Name, data, maxChars), v, nil
}

// pageMarker matches the page markers that the text extraction puts in paged
// formats ("[page 3]"); a few spellings are accepted.
var pageMarker = regexp.MustCompile(`(?i)\[(?:page|p\.|pág\.?|página|pagina)\s*(\d{1,5})\]`)

// PageMarker exposes the marker pattern (the chunker uses the same one).
func PageMarker() *regexp.Regexp { return pageMarker }

// PageOffset returns the character offset where page n starts in text, or -1.
func PageOffset(text string, n int) int {
	for _, m := range pageMarker.FindAllStringSubmatchIndex(text, -1) {
		if p, err := strconv.Atoi(text[m[2]:m[3]]); err == nil && p == n {
			return utf8.RuneCountInString(text[:m[0]])
		}
	}
	return -1
}

// parseRange reads "start-end" (character offsets).
func parseRange(s string) (int, int, bool) {
	a, b, ok := strings.Cut(strings.TrimSpace(s), "-")
	if !ok {
		return 0, 0, false
	}
	x, e1 := strconv.Atoi(strings.TrimSpace(a))
	y, e2 := strconv.Atoi(strings.TrimSpace(b))
	if e1 != nil || e2 != nil || x < 0 || y <= x {
		return 0, 0, false
	}
	return x, y, true
}

// readWindow turns page/range arguments of fs.read into offset/max_chars. ok
// is false when the arguments are invalid; note is a trusted line for the agent.
func (a *AgentFS) readWindow(ctx context.Context, f File, args map[string]any) (off, mx int, note string, ok bool) {
	off, _ = argInt(args, "offset")
	mx, _ = argInt(args, "max_chars")
	if r := argStr(args, "range"); r != "" {
		x, y, good := parseRange(r)
		if !good {
			return 0, 0, "", false
		}
		return x, min(y-x, MaxReadChars), "", true
	}
	if _, has := args["page"]; !has {
		return off, mx, "", true
	}
	page, good := argInt(args, "page")
	if !good || page < 1 {
		return 0, 0, "", false
	}
	ver, _ := argInt(args, "version")
	x, _, err := a.Svc.FullText(ctx, f, ver, 0)
	if err != nil || !x.Extracted {
		return off, mx, "", true // the read itself reports the problem
	}
	if p := PageOffset(x.Text, page); p >= 0 {
		if mx <= 0 {
			mx = DefaultReadChars
		}
		return p, mx, fmt.Sprintf("page %d starts at offset %d", page, p), true
	}
	return off, mx, fmt.Sprintf("page %d not found (this format has no page markers): reading from offset %d", page, off), true
}

// scopeUnder intersects a requested path with the readable roots: the result
// is never wider than what the agent can read.
func scopeUnder(roots []string, p string) []string {
	out := []string{}
	for _, r := range roots {
		switch {
		case IsUnder(p, r):
			return []string{p}
		case IsUnder(r, p):
			out = append(out, r)
		}
	}
	return out
}

// runChunkSearch answers fs.search from the passage index. ok=false means
// "use the file-level search" (index off, error, or nothing indexed matched).
func (a *AgentFS) runChunkSearch(ctx context.Context, agentID string, args map[string]any, under []string, lim int) (RunResult, bool) {
	org := a.Svc.org(ctx)
	mode := strings.ToLower(argStr(args, "mode"))
	res, err := a.Chunks.SearchChunks(ctx, ChunkQuery{Query: argStr(args, "query"), Under: under, Mode: mode, Limit: lim, Locale: argStr(args, "locale")})
	if err != nil {
		return RunResult{}, false
	}
	// Defense in depth: the index already filtered by folder BEFORE ranking;
	// every hit is checked again against the zone policy before it is shown.
	sc, err := a.scopeOf(ctx, agentID)
	if err != nil {
		return failure(err), true
	}
	hits := res.Hits[:0:0]
	for _, h := range res.Hits {
		if canRead(a.zoneAll(ctx, org, sc, h.Path)) {
			hits = append(hits, h)
		}
	}
	if len(hits) == 0 && mode == "" {
		return RunResult{}, false // nothing indexed matched: try names and file text
	}
	var b strings.Builder
	others := false
	if res.Note != "" {
		fmt.Fprintf(&b, "(search mode: %s; %s)\n", res.Mode, res.Note)
	}
	data := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		cite := h.Path
		if h.Page > 0 {
			cite += fmt.Sprintf(" p.%d", h.Page)
		} else if h.Line > 0 {
			cite += fmt.Sprintf(" l.%d", h.Line)
		}
		fmt.Fprintf(&b, "[%s] v%d | score %.4f | file_id %s | read: offset=%d\n", cite, h.Version, h.Score, h.FileID, h.Start)
		if h.Heading != "" {
			fmt.Fprintf(&b, "  section: %s\n", h.Heading)
		}
		fmt.Fprintf(&b, "  %s\n", h.Snippet)
		others = others || h.UpdatedBy != AgentP(agentID)
		data = append(data, map[string]any{"path": h.Path, "file_id": h.FileID, "version": h.Version, "page": h.Page, "line": h.Line,
			"start": h.Start, "end": h.End, "score": h.Score})
	}
	if len(hits) == 0 {
		b.WriteString("(no matches: I did not find it in the files you can read)\n")
	}
	return RunResult{OK: true, Summary: fmt.Sprintf("%d passage(s), mode %s", len(hits), res.Mode), Text: b.String(), SourceNote: "fs.search",
		AuthoredByOthers: others, Data: map[string]any{"mode": res.Mode, "hits": data}}, true
}
