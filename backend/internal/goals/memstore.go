package goals

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"aiworkforce/backend/internal/domain"
)

// MemStore keeps the goals in memory (no DATABASE_URL, tests).
type MemStore struct {
	mu    sync.RWMutex
	goals map[string]map[string]Goal
}

var _ Store = (*MemStore)(nil)

func NewMemStore() *MemStore { return &MemStore{goals: map[string]map[string]Goal{}} }

func (m *MemStore) ListGoals(_ context.Context, org string) ([]Goal, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Goal, 0, len(m.goals[org]))
	for _, g := range m.goals[org] {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (m *MemStore) GetGoal(_ context.Context, org, id string) (Goal, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	g, ok := m.goals[org][id]
	if !ok {
		return Goal{}, fmt.Errorf("%w: goal", domain.ErrNotFound)
	}
	return g, nil
}

func (m *MemStore) CreateGoal(_ context.Context, org string, g Goal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.goals[org] == nil {
		m.goals[org] = map[string]Goal{}
	}
	if _, ok := m.goals[org][g.ID]; ok {
		return fmt.Errorf("%w: goal exists", domain.ErrConflict)
	}
	m.goals[org][g.ID] = g
	return nil
}

func (m *MemStore) UpdateGoal(_ context.Context, org string, g Goal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.goals[org][g.ID]; !ok {
		return fmt.Errorf("%w: goal", domain.ErrNotFound)
	}
	m.goals[org][g.ID] = g
	return nil
}
