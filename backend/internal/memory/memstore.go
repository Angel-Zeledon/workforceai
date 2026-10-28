package memory

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/textfold"
)

// MemStore is the in-memory DocStore (demo mode without a database, tests).
// It scores like the full-text store does in spirit: the share of the query
// terms found, titles counting triple.
type MemStore struct {
	mu   sync.Mutex
	docs map[string]Doc // org|id
}

// NewMemStore builds an empty store.
func NewMemStore() *MemStore { return &MemStore{docs: map[string]Doc{}} }

var _ DocStore = (*MemStore)(nil)

func key(org, id string) string { return org + "|" + id }

// Upsert implements DocStore.
func (m *MemStore) Upsert(_ context.Context, org string, d Doc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, old := range m.docs { // unique (org, ref)
		if strings.HasPrefix(k, org+"|") && old.Ref == d.Ref && old.ID != d.ID {
			delete(m.docs, k)
		}
	}
	old, had := m.docs[key(org, d.ID)]
	if had {
		d.Suppressed = old.Suppressed
		d.CreatedAt = old.CreatedAt
	}
	// Like Postgres: a new vector wins; an unchanged text keeps its vector; a changed one loses it.
	d.EmbedHash = ContentHash(d)
	switch {
	case len(d.Vec) > 0:
	case had && old.EmbedHash == d.EmbedHash:
		d.Vec, d.EmbedModel = old.Vec, old.EmbedModel
	default:
		d.Vec, d.EmbedModel = nil, ""
	}
	m.docs[key(org, d.ID)] = d
	return nil
}

var _ CustomerRetagger = (*MemStore)(nil)

// RetagCustomer implements CustomerRetagger.
func (m *MemStore) RetagCustomer(_ context.Context, org string, sel RetagSelector, from, to string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, d := range m.docs {
		if !strings.HasPrefix(k, org+"|") || d.CustomerID != from {
			continue
		}
		if (sel.DocID != "" && d.ID == sel.DocID) || (sel.DocID == "" && sel.RequestID != "" && d.RequestID == sel.RequestID) {
			d.CustomerID = to
			m.docs[k] = d
			n++
		}
	}
	return n, nil
}

func visible(d Doc, agentID string) bool {
	return !d.Suppressed && (d.Scope == ScopeOrg || d.AgentID == agentID)
}

// Search implements DocStore.
func (m *MemStore) Search(_ context.Context, org string, p SearchParams) ([]DocHit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []DocHit
	for k, d := range m.docs {
		if !strings.HasPrefix(k, org+"|") || !visible(d, p.AgentID) || !VisibleToCustomer(d, p.CustomerID) || p.OnlyAgent && d.AgentID != p.AgentID ||
			p.ExcludeRequest != "" && d.RequestID == p.ExcludeRequest {
			continue
		}
		title, body := textfold.Fold(d.Title), textfold.Fold(d.Body)
		n := 0.0
		for _, t := range p.Terms {
			if strings.Contains(title, t) {
				n += 3
			} else if strings.Contains(body, t) {
				n++
			}
		}
		if n == 0 {
			continue
		}
		out = append(out, DocHit{Doc: d, Score: n / (n + float64(len(p.Terms)))})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Doc.Ref < out[j].Doc.Ref
	})
	if p.Limit > 0 && len(out) > p.Limit {
		out = out[:p.Limit]
	}
	return out, nil
}

// List implements DocStore.
func (m *MemStore) List(_ context.Context, org, agentID string, limit int) ([]Doc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Doc
	for k, d := range m.docs {
		if strings.HasPrefix(k, org+"|") && !d.Suppressed && d.AgentID == agentID {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Kind == KindNote) != (out[j].Kind == KindNote) {
			return out[i].Kind == KindNote
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GetByRef implements DocStore.
func (m *MemStore) GetByRef(_ context.Context, org, ref string) (Doc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, d := range m.docs {
		if strings.HasPrefix(k, org+"|") && d.Ref == ref {
			return d, nil
		}
	}
	return Doc{}, ErrNotFound
}

// Get implements DocStore.
func (m *MemStore) Get(_ context.Context, org, id string) (Doc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.docs[key(org, id)]; ok {
		return d, nil
	}
	return Doc{}, ErrNotFound
}

// Suppress implements DocStore.
func (m *MemStore) Suppress(_ context.Context, org, id, _ string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[key(org, id)]
	if !ok {
		return ErrNotFound
	}
	d.Suppressed = true
	m.docs[key(org, id)] = d
	return nil
}

// Clear implements DocStore.
func (m *MemStore) Clear(_ context.Context, org string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.docs {
		if strings.HasPrefix(k, org+"|") {
			delete(m.docs, k)
		}
	}
	return nil
}

var _ VecStore = (*MemStore)(nil)

// Candidates implements VecStore.
func (m *MemStore) Candidates(_ context.Context, org string, q VecQuery) ([]VecCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var docs []Doc
	for k, d := range m.docs {
		if strings.HasPrefix(k, org+"|") && len(d.Vec) > 0 && d.EmbedModel == q.Model && visible(d, q.AgentID) && VisibleToCustomer(d, q.CustomerID) &&
			(!q.OnlyAgent || d.AgentID == q.AgentID) && (q.ExcludeRequest == "" || d.RequestID != q.ExcludeRequest) {
			docs = append(docs, d)
		}
	}
	sort.Slice(docs, func(i, j int) bool {
		if !docs[i].UpdatedAt.Equal(docs[j].UpdatedAt) {
			return docs[i].UpdatedAt.After(docs[j].UpdatedAt)
		}
		return docs[i].ID < docs[j].ID
	})
	if q.Limit > 0 && len(docs) > q.Limit {
		docs = docs[:q.Limit]
	}
	out := make([]VecCandidate, len(docs))
	for i, d := range docs {
		out[i] = VecCandidate{ID: d.ID, Vec: d.Vec}
	}
	return out, nil
}

// Unembedded implements VecStore.
func (m *MemStore) Unembedded(_ context.Context, org, model string, limit int) ([]Doc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Doc
	for k, d := range m.docs {
		if strings.HasPrefix(k, org+"|") && !d.Suppressed && (len(d.Vec) == 0 || d.EmbedModel != model) {
			d.Vec = nil
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SetEmbedding implements VecStore.
func (m *MemStore) SetEmbedding(_ context.Context, org, id, hash, model string, vec []float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[key(org, id)]
	if !ok || d.EmbedHash != hash {
		return nil // gone or changed since: the next write embeds it
	}
	d.Vec, d.EmbedModel = vec, model
	m.docs[key(org, id)] = d
	return nil
}
