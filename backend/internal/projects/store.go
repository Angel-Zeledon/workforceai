package projects

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"aiworkforce/backend/internal/domain"
)

// Store persists projects and the custom templates of an organization. Every
// method is scoped by org id (the Postgres implementation also enforces it with
// Row-Level Security, migration 270).
type Store interface {
	// Put upserts a project by (org, id).
	Put(ctx context.Context, r Record) error
	Get(ctx context.Context, org, id string) (Record, error)
	// List returns the projects of the organization, newest first.
	List(ctx context.Context, org string) ([]Record, error)
	// ByRequest finds the project launched as the given orchestrator request.
	ByRequest(ctx context.Context, org, requestID string) (Record, error)

	PutTemplate(ctx context.Context, org string, t Template) error
	ListTemplates(ctx context.Context, org string) ([]Template, error)
}

func cloneRecord(r Record) Record {
	b, _ := json.Marshal(r)
	var out Record
	_ = json.Unmarshal(b, &out)
	return out
}

// MemStore is the in-memory Store (demo mode and tests).
type MemStore struct {
	mu   sync.Mutex
	recs map[string]Record // org|id
	tpls map[string][]Template
}

func NewMemStore() *MemStore {
	return &MemStore{recs: map[string]Record{}, tpls: map[string][]Template{}}
}

var _ Store = (*MemStore)(nil)

func (m *MemStore) Put(_ context.Context, r Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs[r.OrgID+"|"+r.ID] = cloneRecord(r)
	return nil
}

func (m *MemStore) Get(_ context.Context, org, id string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.recs[org+"|"+id]
	if !ok {
		return Record{}, domain.ErrNotFound
	}
	return cloneRecord(r), nil
}

func (m *MemStore) List(_ context.Context, org string) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Record{}
	for k, r := range m.recs {
		if strings.HasPrefix(k, org+"|") {
			out = append(out, cloneRecord(r))
		}
	}
	slices.SortFunc(out, func(a, b Record) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

func (m *MemStore) ByRequest(_ context.Context, org, requestID string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, r := range m.recs {
		if strings.HasPrefix(k, org+"|") && r.RequestID == requestID && requestID != "" {
			return cloneRecord(r), nil
		}
	}
	return Record{}, domain.ErrNotFound
}

func (m *MemStore) PutTemplate(_ context.Context, org string, t Template) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.tpls[org]
	for i := range list {
		if list[i].ID == t.ID {
			list[i] = t
			return nil
		}
	}
	m.tpls[org] = append(list, t)
	return nil
}

func (m *MemStore) ListTemplates(_ context.Context, org string) ([]Template, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.tpls[org]), nil
}
