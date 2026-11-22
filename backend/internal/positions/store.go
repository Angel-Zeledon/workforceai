package positions

import (
	"context"
	"sort"
	"strings"
	"sync"

	"aiworkforce/backend/internal/domain"
)

// Store keeps the positions and who holds them. Postgres: internal/infrastructure/postgres/positions.go (migration
// 830); MemStore for tests and the demo.
type Store interface {
	// ListPositions lists the positions of a department ("" = every department), oldest first.
	ListPositions(ctx context.Context, org, officeID string) ([]Position, error)
	GetPosition(ctx context.Context, org, id string) (Position, error) // domain.ErrNotFound
	// SavePositions inserts or replaces positions in one transaction (a change and the cut of its subordinates).
	SavePositions(ctx context.Context, org string, ps []Position) error
	// DeletePosition removes a position in one transaction: its subordinates report to its superior and whoever held
	// it holds nothing.
	DeletePosition(ctx context.Context, org, id string) error
	ListAssignments(ctx context.Context, org string) ([]Assignment, error)
	AssignmentOf(ctx context.Context, org, agentID string) (Assignment, error) // domain.ErrNotFound
	SetAssignment(ctx context.Context, org string, a Assignment) error         // one per agent: replaces
	DeleteAssignment(ctx context.Context, org, agentID string) error
}

// MemStore is an in-memory Store, isolated per organization.
type MemStore struct {
	mu        sync.Mutex
	positions map[string]map[string]Position   // org -> id -> position
	assigned  map[string]map[string]Assignment // org -> agent -> assignment
}

// NewMemStore builds an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{positions: map[string]map[string]Position{}, assigned: map[string]map[string]Assignment{}}
}

func clonePosition(p Position) Position {
	p.Skills = append([]string{}, p.Skills...)
	p.Library = append([]Folder{}, p.Library...)
	p.Occupants = nil
	return p
}

func (m *MemStore) ListPositions(_ context.Context, org, officeID string) ([]Position, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Position{}
	for _, p := range m.positions[org] {
		if officeID == "" || p.OfficeID == officeID {
			out = append(out, clonePosition(p))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemStore) GetPosition(_ context.Context, org, id string) (Position, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.positions[org][id]
	if !ok {
		return Position{}, domain.ErrNotFound
	}
	return clonePosition(p), nil
}

func (m *MemStore) SavePositions(_ context.Context, org string, ps []Position) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.positions[org] == nil {
		m.positions[org] = map[string]Position{}
	}
	for _, p := range ps {
		m.positions[org][p.ID] = clonePosition(p)
	}
	return nil
}

func (m *MemStore) DeletePosition(_ context.Context, org, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.positions[org][id]
	if !ok {
		return domain.ErrNotFound
	}
	for k, c := range m.positions[org] {
		if c.ReportsTo == id {
			c.ReportsTo = p.ReportsTo
			m.positions[org][k] = c
		}
	}
	for a, x := range m.assigned[org] {
		if x.PositionID == id {
			delete(m.assigned[org], a)
		}
	}
	delete(m.positions[org], id)
	return nil
}

func (m *MemStore) ListAssignments(_ context.Context, org string) ([]Assignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Assignment{}
	for _, a := range m.assigned[org] {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return strings.Compare(out[i].AgentID, out[j].AgentID) < 0 })
	return out, nil
}

func (m *MemStore) AssignmentOf(_ context.Context, org, agentID string) (Assignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.assigned[org][agentID]
	if !ok {
		return Assignment{}, domain.ErrNotFound
	}
	return a, nil
}

func (m *MemStore) SetAssignment(_ context.Context, org string, a Assignment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.positions[org][a.PositionID]; !ok {
		return domain.ErrNotFound
	}
	if m.assigned[org] == nil {
		m.assigned[org] = map[string]Assignment{}
	}
	m.assigned[org][a.AgentID] = a
	return nil
}

func (m *MemStore) DeleteAssignment(_ context.Context, org, agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.assigned[org], agentID)
	return nil
}
