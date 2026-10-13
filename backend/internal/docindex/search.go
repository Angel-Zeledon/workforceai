package docindex

import (
	"context"
	"strings"
	"unicode/utf8"

	"aiworkforce/backend/internal/drive"
)

// legDepth is how many results each leg contributes to the fusion.
const legDepth = 50

// SearchChunks implements drive.ChunkSearcher: keyword (FTS), semantic
// (cosine over a prefiltered candidate set) or hybrid (reciprocal rank
// fusion). q.Under is applied inside every query, before ranking: a passage
// outside it is never scored, counted nor returned.
func (s *Service) SearchChunks(ctx context.Context, q drive.ChunkQuery) (drive.ChunkResult, error) {
	org := s.org(ctx)
	st, err := s.cfg.Store.Settings(ctx, org)
	if err != nil {
		return drive.ChunkResult{}, err
	}
	if !st.Enabled {
		return drive.ChunkResult{}, drive.ErrIndexOff
	}
	res := drive.ChunkResult{Hits: []drive.ChunkHit{}, Mode: drive.SearchKeyword}
	query := strings.TrimSpace(q.Query)
	if query == "" || q.Under != nil && len(q.Under) == 0 {
		return res, nil
	}
	if utf8.RuneCountInString(query) > 500 {
		query = string([]rune(query)[:500])
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 8
	}
	limit = min(limit, 50)
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Limits.SearchTimeout)
	defer cancel()

	mode := strings.ToLower(q.Mode)
	wantKw := mode != drive.SearchSemantic
	wantSem := mode != drive.SearchKeyword

	var emb Embedder
	if wantSem {
		emb, res.Note = s.queryEmbedder(ctx, org)
		if emb == nil && !wantKw { // semantic asked but unavailable: keyword, said plainly
			wantKw = true
		}
	}
	loc := q.Locale
	if loc == "" && s.cfg.Locale != nil {
		loc = s.cfg.Locale(ctx, org)
	}
	lang := DetectLang(query, LangOfLocale(loc))

	byKey := map[Key]Passage{}
	var kwKeys, semKeys []Key
	if wantKw {
		ps, err := s.cfg.Store.Keyword(ctx, org, KeywordQuery{Query: query, Lang: lang, Under: q.Under, Limit: legDepth})
		if err != nil {
			return drive.ChunkResult{}, err
		}
		for _, p := range ps {
			byKey[p.Key] = p
			kwKeys = append(kwKeys, p.Key)
		}
	}
	if emb != nil {
		keys, note := s.semantic(ctx, org, emb, query, q.Under)
		if note != "" {
			res.Note = note
		}
		for _, sc := range keys {
			semKeys = append(semKeys, sc.Key)
		}
	}
	var ranked []Scored
	switch {
	case len(semKeys) > 0 && wantKw:
		res.Mode = drive.SearchHybrid
		ranked = Fuse(RRFK, kwKeys, semKeys)
	case len(semKeys) > 0:
		res.Mode = drive.SearchSemantic
		ranked = Fuse(RRFK, semKeys)
	default:
		res.Mode = drive.SearchKeyword
		ranked = Fuse(RRFK, kwKeys)
		if emb != nil && wantSem && res.Note == "" && len(kwKeys) == 0 {
			res.Note = "no passage is close in meaning either"
		}
	}
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	var missing []Key
	for _, r := range ranked {
		if _, ok := byKey[r.Key]; !ok {
			missing = append(missing, r.Key)
		}
	}
	if len(missing) > 0 {
		ps, err := s.cfg.Store.Passages(ctx, org, missing, q.Under)
		if err != nil {
			return drive.ChunkResult{}, err
		}
		for _, p := range ps {
			byKey[p.Key] = p
		}
	}
	for _, r := range ranked {
		p, ok := byKey[r.Key]
		if !ok {
			continue // gone meanwhile, or outside the scope: never shown
		}
		res.Hits = append(res.Hits, drive.ChunkHit{FileID: p.Key.FileID, Path: p.Path, Name: p.Name, Version: p.Key.Version, Page: p.Page, Line: p.Line,
			Start: p.Start, End: p.End, Heading: p.Heading, Snippet: Snippet(p.Text, query), Score: r.Score, UpdatedBy: p.UpdatedBy})
	}
	return res, nil
}

// queryEmbedder returns the embedder for a query, or a note saying why not.
func (s *Service) queryEmbedder(ctx context.Context, org string) (Embedder, string) {
	if s.cfg.Embeddings == nil {
		return nil, "semantic ranking unavailable: no embeddings provider is configured"
	}
	ch := s.cfg.Embeddings(ctx, org)
	if ch.Embedder == nil {
		return nil, "semantic ranking unavailable: " + ch.Note
	}
	return ch.Embedder, ""
}

// semantic embeds the query and ranks the prefiltered candidates by cosine.
func (s *Service) semantic(ctx context.Context, org string, emb Embedder, query string, under []string) ([]Scored, string) {
	est := utf8.RuneCountInString(query)/4 + 1
	if reason := s.allowEmbed(ctx, org, est); reason != "" {
		if reason == ReasonEmbedBudget {
			return nil, "semantic ranking skipped: the budget is exhausted"
		}
		return nil, "semantic ranking skipped: the daily embeddings cap was reached"
	}
	vecs, tokens, err := emb.Embed(ctx, []string{query})
	if err != nil || len(vecs) != 1 {
		return nil, "semantic ranking failed (provider error): keyword results only"
	}
	s.bookEmbed(ctx, org, max(tokens, est), emb.Model())
	cands, err := s.cfg.Store.Candidates(ctx, org, CandidateQuery{Under: under, Model: emb.Model(), OrTerms: queryTerms(query), Limit: s.cfg.Limits.CandidateCap})
	if err != nil {
		return nil, "semantic ranking failed: keyword results only"
	}
	if len(cands) == 0 {
		return nil, "no passage in scope has embeddings yet (still indexing, or the daily embeddings cap was reached): keyword ranking only"
	}
	return TopCosine(vecs[0], cands, legDepth), ""
}
