package away

import (
	"context"
	"sync"
)

// MemMarks keeps the marks in memory (demo without Postgres; they die with the process, which only means the
// next visit after a restart starts without a summary).
type MemMarks struct {
	mu sync.Mutex
	m  map[string]Mark // org + "\x00" + user
}

var _ MarkStore = (*MemMarks)(nil)

// NewMemMarks builds an empty store.
func NewMemMarks() *MemMarks { return &MemMarks{m: map[string]Mark{}} }

func markKey(org, user string) string { return org + "\x00" + user }

// GetAwayMark returns the mark of the person, found=false when there is none.
func (s *MemMarks) GetAwayMark(_ context.Context, org, user string) (Mark, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.m[markKey(org, user)]
	return m, ok, nil
}

// PutAwayMark stores the mark.
func (s *MemMarks) PutAwayMark(_ context.Context, org, user string, m Mark) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[markKey(org, user)] = m
	return nil
}
