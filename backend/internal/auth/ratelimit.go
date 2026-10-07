package auth

import (
	"context"
	"fmt"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Limit is "Max requests per Window".
type Limit struct {
	Max    int
	Window time.Duration
}

// LimitResult is the outcome of one Allow call.
type LimitResult struct {
	Allowed    bool
	Remaining  int
	RetryAfter time.Duration // zero when Allowed
}

// Limiter counts hits per key. Implementations must be concurrency-safe.
type Limiter interface {
	Allow(ctx context.Context, key string, l Limit) (LimitResult, error)
}

// MemoryLimiter is a fixed-window in-process limiter.
type MemoryLimiter struct {
	mu      sync.Mutex
	entries map[string]*memWindow
	now     func() time.Time
	calls   int
}

type memWindow struct {
	count   int
	resetAt time.Time
}

func NewMemoryLimiter() *MemoryLimiter {
	return &MemoryLimiter{entries: map[string]*memWindow{}, now: time.Now}
}

// SetClock overrides the time source (tests).
func (m *MemoryLimiter) SetClock(now func() time.Time) { m.now = now }

func (m *MemoryLimiter) Allow(_ context.Context, key string, l Limit) (LimitResult, error) {
	if l.Max <= 0 || l.Window <= 0 {
		return LimitResult{}, fmt.Errorf("auth: invalid limit %+v", l)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if m.calls++; m.calls%1024 == 0 {
		for k, w := range m.entries {
			if !w.resetAt.After(now) {
				delete(m.entries, k)
			}
		}
	}
	w, ok := m.entries[key]
	if !ok || !w.resetAt.After(now) {
		w = &memWindow{resetAt: now.Add(l.Window)}
		m.entries[key] = w
	}
	if w.count >= l.Max {
		return LimitResult{Allowed: false, RetryAfter: w.resetAt.Sub(now)}, nil
	}
	w.count++
	return LimitResult{Allowed: true, Remaining: l.Max - w.count}, nil
}

// RedisLimiter is a fixed-window limiter shared across backend instances.
// The INCR+PEXPIRE pair runs in one Lua script so a crash can never leave a
// counter without a TTL.
type RedisLimiter struct {
	client goredis.Scripter
	prefix string
}

var redisLimitScript = goredis.NewScript(`
local c = redis.call('INCR', KEYS[1])
local ttl = redis.call('PTTL', KEYS[1])
if c == 1 or ttl < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  ttl = tonumber(ARGV[1])
end
return {c, ttl}
`)

// NewRedisLimiter accepts any go-redis client (Client, Cluster, Ring...).
func NewRedisLimiter(client goredis.Scripter, prefix string) *RedisLimiter {
	if prefix == "" {
		prefix = "aiworkforce:rl:"
	}
	return &RedisLimiter{client: client, prefix: prefix}
}

func (r *RedisLimiter) Allow(ctx context.Context, key string, l Limit) (LimitResult, error) {
	if l.Max <= 0 || l.Window <= 0 {
		return LimitResult{}, fmt.Errorf("auth: invalid limit %+v", l)
	}
	res, err := redisLimitScript.Run(ctx, r.client, []string{r.prefix + key}, l.Window.Milliseconds()).Slice()
	if err != nil {
		return LimitResult{}, err
	}
	if len(res) != 2 {
		return LimitResult{}, fmt.Errorf("auth: unexpected redis limiter reply %v", res)
	}
	count, ok1 := res[0].(int64)
	ttl, ok2 := res[1].(int64)
	if !ok1 || !ok2 {
		return LimitResult{}, fmt.Errorf("auth: unexpected redis limiter reply types %T %T", res[0], res[1])
	}
	if count > int64(l.Max) {
		return LimitResult{Allowed: false, RetryAfter: time.Duration(ttl) * time.Millisecond}, nil
	}
	return LimitResult{Allowed: true, Remaining: l.Max - int(count)}, nil
}
