package artifacts

import (
	"context"
	"strings"
	"unicode/utf8"

	"aiworkforce/backend/internal/application"
)

// ContextArtifacts implements application.ContextSource (W3): the backend reads
// the artifacts a project task explicitly references and hands their text to
// the runtime as delimited data. An artifact is readable when the agent has
// access to it or it belongs to the same request; anything else is skipped
// silently. The total is bounded by budgetTokens (about 4 bytes per token).
func (s *Service) ContextArtifacts(ctx context.Context, org, agentID, requestID string, ids []string, budgetTokens int) []application.ContextArtifact {
	ctx = application.WithOrg(ctx, org)
	left := budgetTokens * 4
	var out []application.ContextArtifact
	for _, id := range ids {
		if left <= 0 {
			break
		}
		a, err := s.Get(ctx, id, 0)
		if err != nil {
			continue
		}
		sameRequest := a.RequestID != nil && *a.RequestID == requestID
		if !sameRequest && modeOf(a.Meta, agentID) == ModeNone {
			continue
		}
		text := strings.TrimSpace(string(a.Content))
		trunc := false
		if len(text) > left {
			cut := left
			for cut > 0 && !utf8.RuneStart(text[cut]) {
				cut--
			}
			text, trunc = text[:cut], true
		}
		left -= len(text)
		out = append(out, application.ContextArtifact{ID: a.ID, Title: a.Title, Kind: string(a.Kind), Text: text, Truncated: trunc})
	}
	return out
}
