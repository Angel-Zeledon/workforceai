package memory

import (
	"context"
	"sync"
	"time"
)

// Locker is an in-process lock used when Redis is unavailable and in tests.
type Locker struct {
	mu   sync.Mutex
	held map[string]time.Time
}

func NewLocker() *Locker { return &Locker{held: map[string]time.Time{}} }

func (l *Locker) Lock(_ context.Context, key string, ttl time.Duration) (func(), bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if exp, ok := l.held[key]; ok && time.Now().Before(exp) {
		return nil, false, nil
	}
	l.held[key] = time.Now().Add(ttl)
	return func() {
		l.mu.Lock()
		delete(l.held, key)
		l.mu.Unlock()
	}, true, nil
}
