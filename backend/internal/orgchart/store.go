package orgchart

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Applied records that an industry template was applied to an organization.
type Applied struct {
	Key       string         `json:"key"`
	Version   int            `json:"version"`
	Locale    string         `json:"locale"`
	AppliedBy string         `json:"applied_by"`
	AppliedAt time.Time      `json:"applied_at"`
	Detail    map[string]any `json:"detail"`
}

// Store persists the organization model. Every method is scoped to one organization.
type Store interface {
	ListDepartments(ctx context.Context, org string) ([]Department, error)
	PutDepartment(ctx context.Context, org string, d Department) error
	DeleteDepartment(ctx context.Context, org, id string) error

	ListJobs(ctx context.Context, org string) ([]Job, error)
	PutJob(ctx context.Context, org string, j Job) error
	DeleteJob(ctx context.Context, org, agentID string) error

	ListPlacements(ctx context.Context, org string) ([]Placement, error)
	PutPlacement(ctx context.Context, org string, p Placement) error

	ListRules(ctx context.Context, org string) ([]Rule, error)
	PutRule(ctx context.Context, org string, r Rule) error
	DeleteRule(ctx context.Context, org, id string) error

	GetApplied(ctx context.Context, org, key string) (Applied, bool, error)
	ListApplied(ctx context.Context, org string) ([]Applied, error)
	PutApplied(ctx context.Context, org string, a Applied) error
}

// MemoryStore is the in-memory Store (tests, and the open demo without a database).
type MemoryStore struct {
	mu    sync.Mutex
	deps  map[string]map[string]Department
	jobs  map[string]map[string]Job
	place map[string]map[string]Placement
	rules map[string]map[string]Rule
	appl  map[string]map[string]Applied
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{deps: map[string]map[string]Department{}, jobs: map[string]map[string]Job{},
		place: map[string]map[string]Placement{}, rules: map[string]map[string]Rule{}, appl: map[string]map[string]Applied{}}
}

func sub[T any](m map[string]map[string]T, org string) map[string]T {
	if m[org] == nil {
		m[org] = map[string]T{}
	}
	return m[org]
}

func sorted[T any](m map[string]T) []T {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	out := make([]T, 0, len(ks))
	for _, k := range ks {
		out = append(out, m[k])
	}
	return out
}

func (s *MemoryStore) ListDepartments(_ context.Context, org string) ([]Department, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := sorted(sub(s.deps, org))
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) PutDepartment(_ context.Context, org string, d Department) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub(s.deps, org)[d.ID] = d
	return nil
}

func (s *MemoryStore) DeleteDepartment(_ context.Context, org, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := sub(s.deps, org)[id]; !ok {
		return fmt.Errorf("%w: department %s", domain.ErrNotFound, id)
	}
	delete(sub(s.deps, org), id)
	return nil
}

func (s *MemoryStore) ListJobs(_ context.Context, org string) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sorted(sub(s.jobs, org)), nil
}

func (s *MemoryStore) PutJob(_ context.Context, org string, j Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub(s.jobs, org)[j.AgentID] = j
	return nil
}

func (s *MemoryStore) DeleteJob(_ context.Context, org, agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(sub(s.jobs, org), agentID)
	return nil
}

func (s *MemoryStore) ListPlacements(_ context.Context, org string) ([]Placement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sorted(sub(s.place, org)), nil
}

func (s *MemoryStore) PutPlacement(_ context.Context, org string, p Placement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub(s.place, org)[p.UserID] = p
	return nil
}

func (s *MemoryStore) ListRules(_ context.Context, org string) ([]Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := sorted(sub(s.rules, org))
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) PutRule(_ context.Context, org string, r Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub(s.rules, org)[r.ID] = r
	return nil
}

func (s *MemoryStore) DeleteRule(_ context.Context, org, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := sub(s.rules, org)[id]; !ok {
		return fmt.Errorf("%w: rule %s", domain.ErrNotFound, id)
	}
	delete(sub(s.rules, org), id)
	return nil
}

func (s *MemoryStore) GetApplied(_ context.Context, org, key string) (Applied, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := sub(s.appl, org)[key]
	return a, ok, nil
}

func (s *MemoryStore) ListApplied(_ context.Context, org string) ([]Applied, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sorted(sub(s.appl, org)), nil
}

func (s *MemoryStore) PutApplied(_ context.Context, org string, a Applied) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub(s.appl, org)[a.Key] = a
	return nil
}
