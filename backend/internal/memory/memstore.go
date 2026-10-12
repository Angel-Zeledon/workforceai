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
	if old, ok := m.docs[key(org, d.ID)]; ok {
		d.Suppressed = old.Suppressed
		d.CreatedAt = old.CreatedAt
	}
	m.docs[key(org, d.ID)] = d
	return nil
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
		if !strings.HasPrefix(k, org+"|") || !visible(d, p.AgentID) || p.OnlyAgent && d.AgentID != p.AgentID ||
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
