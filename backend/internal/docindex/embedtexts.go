package docindex

import (
	"context"
	"unicode/utf8"
)

// EmbedModel is the embeddings model the organization would use right now
// ("" when it stays keyword only: no provider, or the data policy says no).
func (s *Service) EmbedModel(ctx context.Context, org string) string {
	if s.cfg.Embeddings == nil {
		return ""
	}
	if ch := s.cfg.Embeddings(ctx, org); ch.Embedder != nil {
		return ch.Embedder.Model()
	}
	return ""
}

// EmbedTexts embeds free texts for other features (the long-term memory) with
// the same resolver, data policy, daily token cap, budget and ledger as the
// passage index. reason is "" on success, otherwise one of the Reason* codes
// and no vector is returned: the caller falls back to keyword search. The
// texts must already be free of secrets; they are sent to the provider as is.
func (s *Service) EmbedTexts(ctx context.Context, org string, texts []string) (vecs [][]float32, model, reason string) {
	if len(texts) == 0 {
		return nil, "", ""
	}
	if s.cfg.Embeddings == nil {
		return nil, "", ReasonEmbedOff
	}
	ch := s.cfg.Embeddings(ctx, org)
	if ch.Embedder == nil {
		return nil, "", ch.Reason
	}
	model = ch.Embedder.Model()
	est := 0
	for _, t := range texts {
		est += utf8.RuneCountInString(t)/4 + 1
	}
	if reason = s.allowEmbed(ctx, org, est); reason != "" {
		return nil, model, reason
	}
	ectx, cancel := context.WithTimeout(ctx, s.cfg.Limits.EmbedTimeout)
	defer cancel()
	vecs, tokens, err := ch.Embedder.Embed(ectx, texts)
	if err != nil || len(vecs) != len(texts) {
		s.cfg.Log.Warn("docindex: embeddings failed: keyword only", "org", org, "provider", ch.Embedder.Provider())
		return nil, model, ReasonEmbedFailure
	}
	s.bookEmbed(ctx, org, max(tokens, est), model)
	return vecs, model, ""
}
