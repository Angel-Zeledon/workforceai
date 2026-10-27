package linkimports

import (
	"context"
	"sort"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// MemStore is the in-memory Store (tests and the open demo without Postgres).
type MemStore struct {
	mu      sync.Mutex
	imports map[string]map[string]LinkImport // org -> id -> import
	runs    map[string][]Run                 // org|import -> runs (oldest first)
}

var _ Store = (*MemStore)(nil)

// NewMemStore builds an empty store.
func NewMemStore() *MemStore {
	return &MemStore{imports: map[string]map[string]LinkImport{}, runs: map[string][]Run{}}
}

func clone(li LinkImport) LinkImport {
	li.HeaderSealed = append([]byte(nil), li.HeaderSealed...)
	if len(li.HeaderSealed) == 0 {
		li.HeaderSealed = nil
	}
	for _, p := range []**time.Time{&li.NextRunAt, &li.LastRunAt, &li.LastDataAt} {
		if *p != nil {
			t := **p
			*p = &t
		}
	}
	return li
}

func (m *MemStore) Create(_ context.Context, org string, li LinkImport) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.imports[org] == nil {
		m.imports[org] = map[string]LinkImport{}
	}
	if _, ok := m.imports[org][li.ID]; ok {
		return domain.ErrConflict
	}
	m.imports[org][li.ID] = clone(li)
	return nil
}

func (m *MemStore) Update(_ context.Context, org string, li LinkImport) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.imports[org][li.ID]
	if !ok {
		return domain.ErrNotFound
	}
	cur.Name, cur.URL, cur.FileStem, cur.Schedule, cur.Enabled = li.Name, li.URL, li.FileStem, li.Schedule, li.Enabled
	cur.HeaderName, cur.HeaderSealed, cur.HeaderHint = li.HeaderName, li.HeaderSealed, li.HeaderHint
	cur.NextRunAt, cur.UpdatedAt = li.NextRunAt, li.UpdatedAt
	m.imports[org][li.ID] = clone(cur)
	return nil
}

func (m *MemStore) Get(_ context.Context, org, id string) (LinkImport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	li, ok := m.imports[org][id]
	if !ok {
		return LinkImport{}, domain.ErrNotFound
	}
	return clone(li), nil
}

func (m *MemStore) List(_ context.Context, org string) ([]LinkImport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]LinkImport, 0, len(m.imports[org]))
	for _, li := range m.imports[org] {
		out = append(out, clone(li))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (m *MemStore) Delete(_ context.Context, org, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.imports[org][id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.imports[org], id)
	delete(m.runs, org+"|"+id)
	return nil
}

func (m *MemStore) Due(_ context.Context, org string, now time.Time) ([]LinkImport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []LinkImport
	for _, li := range m.imports[org] {
		if li.Enabled && li.Schedule != ScheduleOnce && li.NextRunAt != nil && !li.NextRunAt.After(now) {
			out = append(out, clone(li))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NextRunAt.Before(*out[j].NextRunAt) })
	return out, nil
}

func (m *MemStore) Claim(_ context.Context, org, id string, expected, next time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	li, ok := m.imports[org][id]
	if !ok {
		return false, domain.ErrNotFound
	}
	if li.NextRunAt == nil || !li.NextRunAt.Equal(expected) {
		return false, nil
	}
	li.NextRunAt = &next
	m.imports[org][id] = li
	return true, nil
}

func (m *MemStore) RecordRun(_ context.Context, org string, r Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	li, ok := m.imports[org][r.ImportID]
	if !ok {
		return domain.ErrNotFound
	}
	at := r.StartedAt
	li.LastRunAt, li.LastStatus, li.LastCode, li.RunCount = &at, r.Status, r.Code, li.RunCount+1
	if r.Status == RunOK || r.Status == RunUnchanged {
		li.LastHash, li.LastFileID, li.LastPath, li.LastVersion = r.Hash, r.FileID, r.Path, r.Version
	}
	if r.Status == RunOK {
		li.LastDataAt = &at
	}
	m.imports[org][r.ImportID] = li
	k := org + "|" + r.ImportID
	m.runs[k] = append(m.runs[k], r)
	if n := len(m.runs[k]); n > RunsKept {
		m.runs[k] = append([]Run(nil), m.runs[k][n-RunsKept:]...)
	}
	return nil
}

func (m *MemStore) Runs(_ context.Context, org, importID string, limit int) ([]Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rs := m.runs[org+"|"+importID]
	out := make([]Run, 0, min(limit, len(rs)))
	for i := len(rs) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, rs[i])
	}
	return out, nil
}
