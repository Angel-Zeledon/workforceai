// Package counters persists the state of windowed counters (the windowed
// limits of the policy engine and the anomaly detection windows) so that a
// restart does not reset them. Each component serializes its own per-org
// state as JSON under a scope; the store only keeps the latest snapshot.
//
// Postgres implementation: infrastructure/postgres (migration
// 360_window_counters.sql, RLS by org). Without DATABASE_URL the in-memory
// MemStore is used (same code path, nothing survives a process restart).
package counters

import (
	"context"
	"sync"
)

// Scopes in use.
const (
	ScopePolicyLimits = "policy.limits"
	ScopeAnomaly      = "anomaly"
)

// Store keeps one JSON snapshot per (organization, scope).
type Store interface {
	// LoadCounters returns the snapshot, or nil (and no error) when there is none.
	LoadCounters(ctx context.Context, org, scope string) ([]byte, error)
	// SaveCounters replaces the snapshot.
	SaveCounters(ctx context.Context, org, scope string, state []byte) error
}

// MemStore is the in-process Store (no DATABASE_URL, tests).
type MemStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore { return &MemStore{m: map[string][]byte{}} }

func key(org, scope string) string { return org + "\x00" + scope }

// LoadCounters implements Store.
func (s *MemStore) LoadCounters(_ context.Context, org, scope string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.m[key(org, scope)]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), b...), nil
}

// SaveCounters implements Store.
func (s *MemStore) SaveCounters(_ context.Context, org, scope string, state []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string][]byte{}
	}
	s.m[key(org, scope)] = append([]byte(nil), state...)
	return nil
}
