package portal

import (
	"context"
	"sort"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// viewWriteEvery bounds the writes of MarkViewed: one per link per interval.
const viewWriteEvery = time.Minute

// MemStore is the in-memory Store (no database, tests, the open demo).
type MemStore struct {
	mu       sync.Mutex
	branding map[string]Branding
	links    map[string]map[string]Link // org -> id -> link
}

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{branding: map[string]Branding{}, links: map[string]map[string]Link{}}
}

var _ Store = (*MemStore)(nil)

func cloneLink(l Link) Link {
	l.DeliverableIDs = append([]string{}, l.DeliverableIDs...)
	return l
}

func (m *MemStore) GetBranding(_ context.Context, org string) (Branding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.branding[org]
	if !ok {
		return Branding{}, domain.ErrNotFound
	}
	b.Logo = append([]byte(nil), b.Logo...)
	return b, nil
}

func (m *MemStore) PutBranding(_ context.Context, org string, b Branding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b.Logo = append([]byte(nil), b.Logo...)
	m.branding[org] = b
	return nil
}

func (m *MemStore) CreateLink(_ context.Context, org string, l Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, byID := range m.links {
		for _, x := range byID {
			if x.TokenHash == l.TokenHash {
				return domain.ErrConflict
			}
		}
	}
	if m.links[org] == nil {
		m.links[org] = map[string]Link{}
	}
	if _, ok := m.links[org][l.ID]; ok {
		return domain.ErrConflict
	}
	m.links[org][l.ID] = cloneLink(l)
	return nil
}

func (m *MemStore) UpdateLink(_ context.Context, org string, l Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.links[org][l.ID]
	if !ok {
		return domain.ErrNotFound
	}
	cur.Label, cur.Title, cur.DeliverableIDs, cur.UpdatedAt = l.Label, l.Title, append([]string{}, l.DeliverableIDs...), l.UpdatedAt
	m.links[org][l.ID] = cur
	return nil
}

func (m *MemStore) GetLink(_ context.Context, org, id string) (Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[org][id]
	if !ok {
		return Link{}, domain.ErrNotFound
	}
	return cloneLink(l), nil
}

func (m *MemStore) ListLinks(_ context.Context, org, projectID string) ([]Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Link{}
	for _, l := range m.links[org] {
		if l.ProjectID == projectID {
			out = append(out, cloneLink(l))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (m *MemStore) RevokeLink(_ context.Context, org, id, by string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[org][id]
	if !ok {
		return domain.ErrNotFound
	}
	if l.RevokedAt == nil {
		l.RevokedAt, l.RevokedBy = &at, by
		m.links[org][id] = l
	}
	return nil
}

func (m *MemStore) LinkByHash(_ context.Context, hash string) (string, Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for org, byID := range m.links {
		for _, l := range byID {
			if l.TokenHash == hash {
				return org, cloneLink(l), nil
			}
		}
	}
	return "", Link{}, domain.ErrNotFound
}

func (m *MemStore) MarkViewed(_ context.Context, org, id string, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[org][id]
	if !ok {
		return false, domain.ErrNotFound
	}
	if l.LastViewedAt != nil && at.Sub(*l.LastViewedAt) < viewWriteEvery {
		return false, nil
	}
	first := l.FirstViewedAt == nil
	if first {
		l.FirstViewedAt = &at
	}
	l.LastViewedAt = &at
	m.links[org][id] = l
	return first, nil
}
