package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"aiworkforce/backend/internal/docindex"
	"aiworkforce/backend/internal/sanitize"
	"aiworkforce/backend/internal/textfold"
)

// Search by meaning. The embedding of a document is stored next to it as
// real[] (migration 786; plain postgres:16 has no pgvector) and the cosine is
// computed here over a prefiltered candidate set, exactly like the drive
// passage index does. Everything is best-effort: no provider, a data policy
// that forbids it, the daily token cap or a provider failure leave a document
// without a vector and a query without a semantic leg, and the memory keeps
// answering by keyword.

// Limits of the semantic leg.
const (
	maxEmbedChars    = 8000 // characters of title + body sent to the provider
	maxQueryChars    = 1000
	embedBatch       = 32
	DefaultCandidate = 3000 // documents scored by cosine per query
	// MinSimilarity is the cosine below which a document is not "about" the
	// query (unrelated texts sit around 0.1-0.2).
	MinSimilarity = 0.25
)

// Embedder turns texts into vectors for one organization with its data
// policy, daily token cap and budget (docindex.Service implements it, the
// same resolver and caps as the drive passage index). reason is "" on success,
// otherwise why there are no vectors (docindex.ReasonEmbed*); model is the
// model that embedded them.
type Embedder interface {
	// EmbedModel is the model the organization would use now ("" = keyword only).
	EmbedModel(ctx context.Context, org string) string
	EmbedTexts(ctx context.Context, org string, texts []string) (vecs [][]float32, model, reason string)
}

// VecQuery selects the documents compared by cosine.
type VecQuery struct {
	AgentID        string
	Model          string
	ExcludeRequest string
	OnlyAgent      bool
	Limit          int
}

// VecCandidate is a document with its vector (the text is read later, only
// for the best ones).
type VecCandidate struct {
	ID  string
	Vec []float32
}

// VecStore is the optional vector side of a DocStore. Every call is scoped to
// one organization, like the DocStore.
type VecStore interface {
	// Candidates returns the vectors of the documents the agent may see
	// (organization scope or its own notes, never suppressed) embedded with
	// Model, newest first, at most Limit.
	Candidates(ctx context.Context, org string, q VecQuery) ([]VecCandidate, error)
	// Unembedded returns up to limit live documents with no vector of model
	// (never embedded, or embedded with another model), newest first.
	Unembedded(ctx context.Context, org, model string, limit int) ([]Doc, error)
	// SetEmbedding stores a vector for the document as it was when hash was
	// computed; it is a no-op when the document changed since.
	SetEmbedding(ctx context.Context, org, id, hash, model string, vec []float32) error
}

// EmbedText is what is embedded for a document.
func EmbedText(d Doc) string {
	return capRunes(strings.TrimSpace(d.Title+"\n"+d.Body), maxEmbedChars)
}

// ContentHash identifies the embedded text: an unchanged document keeps its
// vector when it is indexed again.
func ContentHash(d Doc) string {
	h := sha256.Sum256([]byte(EmbedText(d)))
	return hex.EncodeToString(h[:])
}

// Semantic is the Retriever by meaning over the documents (task outputs,
// reports, notes). Drive files have their own semantic ranking in the passage
// index (fs.search), so they stay in the full-text leg. It returns nothing,
// not an error, when the organization has no embedder or embedding the query
// fails, so Multi falls back to the full-text ranking.
type Semantic struct {
	Docs     DocStore
	Vecs     VecStore
	Embedder Embedder // nil: off
	// Suspects redacts the exact secrets the server knows before the query leaves.
	Suspects *sanitize.Suspects
	// Candidates caps the documents scored by cosine (DefaultCandidate).
	Candidates int
	Now        func() time.Time
}

var _ Retriever = (*Semantic)(nil)

// Retrieve implements Retriever.
func (s *Semantic) Retrieve(ctx context.Context, org string, q Query) ([]Item, error) {
	if s.Embedder == nil || s.Vecs == nil || q.AgentID == "" {
		return []Item{}, nil
	}
	text := strings.TrimSpace(q.Text)
	if text == "" {
		text = strings.Join(q.Terms, " ")
	}
	text = capRunes(sanitize.RedactSecrets(text, s.Suspects), maxQueryChars)
	if strings.TrimSpace(text) == "" {
		return []Item{}, nil
	}
	limit := q.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	vecs, model, reason := s.Embedder.EmbedTexts(ctx, org, []string{text})
	if reason != "" || len(vecs) != 1 {
		return []Item{}, nil // keyword only
	}
	capN := s.Candidates
	if capN <= 0 {
		capN = DefaultCandidate
	}
	cands, err := s.Vecs.Candidates(ctx, org, VecQuery{AgentID: q.AgentID, Model: model, ExcludeRequest: q.ExcludeRequest, OnlyAgent: q.OnlyAgent, Limit: capN})
	if err != nil {
		return nil, err
	}
	type scored struct {
		id  string
		sim float64
	}
	var top []scored
	for _, c := range cands {
		if sim := docindex.Cosine(vecs[0], c.Vec); sim >= MinSimilarity {
			top = append(top, scored{c.ID, sim})
		}
	}
	sort.SliceStable(top, func(i, j int) bool {
		if top[i].sim != top[j].sim {
			return top[i].sim > top[j].sim
		}
		return top[i].id < top[j].id
	})
	if len(top) > limit*2 {
		top = top[:limit*2]
	}
	terms := q.Terms
	if len(terms) == 0 {
		terms = textfold.Terms(q.Text, maxQueryTerms)
	}
	items := make([]Item, 0, len(top))
	for _, t := range top {
		d, err := s.Docs.Get(ctx, org, t.id)
		if err != nil || !visible(d, q.AgentID) || q.OnlyAgent && d.AgentID != q.AgentID ||
			q.ExcludeRequest != "" && d.RequestID == q.ExcludeRequest {
			continue // gone, suppressed or not for this agent since the candidate scan
		}
		it := docItem(DocHit{Doc: d, Score: t.sim}, terms)
		it.cover = 1 // the lexical coverage penalty does not apply to a match by meaning
		items = append(items, it)
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	return Rank(items, q, now, limit), nil
}

// embedDocs fills the vectors it can for documents about to be written
// (already scrubbed of secrets). A document whose text did not change keeps
// the vector it has. It never fails the write: without a vector the document
// is searchable by keyword and the backfill retries later.
func (s *Service) embedDocs(ctx context.Context, org string, docs ...*Doc) {
	if s.cfg.Embedder == nil {
		return
	}
	model := s.cfg.Embedder.EmbedModel(ctx, org)
	if model == "" {
		return
	}
	var need []*Doc
	for _, d := range docs {
		if old, err := s.cfg.Docs.GetByRef(ctx, org, d.Ref); err == nil && old.EmbedModel == model && old.EmbedHash == ContentHash(*d) {
			continue // the store keeps the vector of an unchanged text
		}
		need = append(need, d)
	}
	if len(need) == 0 {
		return
	}
	texts := make([]string, len(need))
	for i, d := range need {
		texts[i] = EmbedText(*d)
	}
	vecs, m, reason := s.cfg.Embedder.EmbedTexts(ctx, org, texts)
	if reason != "" {
		s.cfg.Log.Debug("memory: stored without embedding", "org", org, "reason", reason)
		return
	}
	for i, d := range need {
		d.Vec, d.EmbedModel = vecs[i], m
	}
}

// EmbedPending embeds, best-effort, up to limit documents that have no vector
// of the current model (written before the migration, or while the provider
// was off, failing or over its daily cap). It stops at the first refusal and
// returns the number embedded.
func (s *Service) EmbedPending(ctx context.Context, org string, limit int) int {
	vs, ok := s.cfg.Docs.(VecStore)
	if !ok || s.cfg.Embedder == nil || limit <= 0 {
		return 0
	}
	model := s.cfg.Embedder.EmbedModel(ctx, org)
	if model == "" {
		return 0
	}
	docs, err := vs.Unembedded(ctx, org, model, limit)
	if err != nil {
		s.cfg.Log.Warn("memory: list unembedded", "org", org, "err", err)
		return 0
	}
	n := 0
	for len(docs) > 0 {
		b := docs[:min(embedBatch, len(docs))]
		docs = docs[len(b):]
		texts := make([]string, len(b))
		for i, d := range b {
			texts[i] = EmbedText(d)
		}
		vecs, m, reason := s.cfg.Embedder.EmbedTexts(ctx, org, texts)
		if reason != "" {
			break
		}
		for i, d := range b {
			if err := vs.SetEmbedding(ctx, org, d.ID, ContentHash(d), m, vecs[i]); err != nil {
				s.cfg.Log.Warn("memory: store embedding", "org", org, "doc", d.ID, "err", err)
				return n
			}
			n++
		}
	}
	return n
}

// topUp is the lazy path: a recall that finds an embedder schedules a small
// backfill (at most one per organization every topUpEvery), so documents left
// without a vector by a cap or an outage catch up without a restart.
const (
	topUpEvery = 10 * time.Minute
	topUpBatch = 64
)

func (s *Service) topUp(ctx context.Context, org string) {
	if s.cfg.Embedder == nil {
		return
	}
	if _, ok := s.cfg.Docs.(VecStore); !ok {
		return
	}
	now := s.cfg.Now()
	s.topMu.Lock()
	if last, ok := s.topAt[org]; ok && now.Sub(last) < topUpEvery {
		s.topMu.Unlock()
		return
	}
	if s.topAt == nil {
		s.topAt = map[string]time.Time{}
	}
	s.topAt[org] = now
	s.topMu.Unlock()
	go func() {
		bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		s.EmbedPending(bctx, org, topUpBatch)
	}()
}
