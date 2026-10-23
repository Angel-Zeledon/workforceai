package artifacts

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"aiworkforce/backend/internal/application"
)

// ContextArtifacts implements application.ContextSource (W3): the backend reads
// the artifacts a project task explicitly references and hands their text to
// the runtime as delimited data. An artifact is readable when the agent has
// access to it or it belongs to the same request; anything else is skipped
// silently. A task scoped to a customer (B12) only ever receives artifacts of
// that customer (see customerAllows). The total is bounded by budgetTokens
// (about 4 bytes per token).
func (s *Service) ContextArtifacts(ctx context.Context, org, agentID, requestID string, ids []string, budgetTokens int) []application.ContextArtifact {
	ctx = application.WithOrg(ctx, org)
	scope, ok := s.requestCustomer(ctx, org, requestID)
	if !ok {
		s.cfg.Log.Warn("artifacts: customer of request unknown, handing no referenced artifacts", "request", requestID)
		return nil
	}
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
		if !customerAllows(scope, a.Meta, sameRequest) {
			s.cfg.Log.Warn("artifacts: reference of another customer skipped", "request", requestID, "artifact", a.ID)
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

// requestCustomer is the customer a request works for ("" = none). ok=false: it could not be determined and
// the caller must hand nothing (fail closed).
func (s *Service) requestCustomer(ctx context.Context, org, requestID string) (string, bool) {
	if s.cfg.CustomerOf == nil || requestID == "" {
		return application.CustomerFrom(ctx), true
	}
	cid, err := s.cfg.CustomerOf(ctx, org, requestID)
	if err != nil {
		return "", false
	}
	if cid == "" { // the run itself may carry a stricter scope than the (unscoped) request link
		cid = application.CustomerFrom(ctx)
	}
	return cid, true
}

// customerAllows is the customer isolation of the artifacts handed to a task (B12): a task without a customer
// keeps the old rule; a task for customer C receives only artifacts tagged C, plus the outputs of its own
// request (same customer by construction). An artifact without a customer may hold anyone's data, so it never
// reaches a customer-scoped task (there is no "shareable" mark for org-wide artifacts).
func customerAllows(scope string, m Meta, sameRequest bool) bool {
	if scope == "" || sameRequest {
		return true
	}
	return m.CustomerID != nil && *m.CustomerID == scope
}

// CustomerOfArtifact is the customer an artifact is tagged with ("" = none; domain.ErrNotFound when it does not
// exist). The projects use it to refuse, at plan time, references a customer-scoped project may not receive.
func (s *Service) CustomerOfArtifact(ctx context.Context, org, id string) (string, error) {
	cur, err := s.cfg.Store.Get(application.WithOrg(ctx, org), org, id)
	if err != nil {
		return "", err
	}
	if cur.Meta.CustomerID == nil {
		return "", nil
	}
	return *cur.Meta.CustomerID, nil
}

var _ application.CitationResolver = (*Service)(nil)

// ResolveCell implements application.CitationResolver (B6 evidence chain): the
// cached value of sheet!cell of a sheet artifact the agent may read (or that
// belongs to the request), as text, and of the task's customer when the request
// is scoped to one. ok=false for anything else.
func (s *Service) ResolveCell(ctx context.Context, org, agentID, requestID, artifactID, sheet, cell string) (string, bool) {
	ctx = application.WithOrg(ctx, org)
	scope, known := s.requestCustomer(ctx, org, requestID)
	if !known {
		return "", false
	}
	a, err := s.Get(ctx, artifactID, 0)
	if err != nil || a.Kind != KindSheet {
		return "", false
	}
	sameRequest := a.RequestID != nil && *a.RequestID == requestID
	if !sameRequest && modeOf(a.Meta, agentID) == ModeNone {
		return "", false
	}
	if !customerAllows(scope, a.Meta, sameRequest) {
		return "", false
	}
	var doc map[string]any
	if decodeNum(a.Content, &doc) != nil {
		return "", false
	}
	for _, sh := range asList(doc["sheets"]) {
		sm := asMap(sh)
		if !strings.EqualFold(strings.TrimSpace(asStr(sm["name"])), strings.TrimSpace(sheet)) {
			continue
		}
		c := asMap(asMap(sm["cells"])[strings.ToUpper(cell)])
		v, ok := c["v"]
		if !ok || v == nil {
			return "", false
		}
		return fmt.Sprint(v), true
	}
	return "", false
}
