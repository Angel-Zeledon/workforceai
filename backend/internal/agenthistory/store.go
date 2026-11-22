package agenthistory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"aiworkforce/backend/internal/domain"
)

// Store keeps the versions. It is append-only: a version is never changed, and the only deletion is the retention
// of the oldest ones beyond `keep` per agent (never the latest).
type Store interface {
	// Last is the newest version of the agent (ok=false when it has none).
	Last(ctx context.Context, org, agentID string) (Version, bool, error)
	// Append adds v (v.Version must be Last+1, else domain.ErrConflict) and drops the oldest beyond keep.
	Append(ctx context.Context, org string, v Version, keep int) error
	// List returns up to limit versions, newest first.
	List(ctx context.Context, org, agentID string, limit int) ([]Version, error)
	// Get returns one version (domain.ErrNotFound when it does not exist or was dropped by the retention).
	Get(ctx context.Context, org, agentID string, n int) (Version, error)
}

// MemStore is the in-memory Store (demo without a database and tests).
type MemStore struct {
	mu sync.Mutex
	m  map[string][]Version // org|agent -> versions, oldest first
}

// NewMemStore returns an empty store.
func NewMemStore() *MemStore { return &MemStore{m: map[string][]Version{}} }

func key(org, agentID string) string { return org + "|" + agentID }

func (s *MemStore) Last(_ context.Context, org, agentID string) (Version, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.m[key(org, agentID)]
	if len(l) == 0 {
		return Version{}, false, nil
	}
	return l[len(l)-1], true, nil
}

func (s *MemStore) Append(_ context.Context, org string, v Version, keep int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(org, v.AgentID)
	l := s.m[k]
	last := 0
	if len(l) > 0 {
		last = l[len(l)-1].Version
	}
	if v.Version != last+1 {
		return fmt.Errorf("%w: version %d of agent %s already exists", domain.ErrConflict, v.Version, v.AgentID)
	}
	l = append(l, v)
	if keep > 0 && len(l) > keep {
		l = append([]Version(nil), l[len(l)-keep:]...)
	}
	s.m[k] = l
	return nil
}

func (s *MemStore) List(_ context.Context, org, agentID string, limit int) ([]Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.m[key(org, agentID)]
	out := make([]Version, 0, len(l))
	for i := len(l) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		out = append(out, l[i])
	}
	return out, nil
}

func (s *MemStore) Get(_ context.Context, org, agentID string, n int) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.m[key(org, agentID)]
	i := sort.Search(len(l), func(i int) bool { return l[i].Version >= n })
	if i < len(l) && l[i].Version == n {
		return l[i], nil
	}
	return Version{}, fmt.Errorf("%w: version %d of agent %s", domain.ErrNotFound, n, agentID)
}
