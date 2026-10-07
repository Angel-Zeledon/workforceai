package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// fakeScripter emulates the Lua script semantics (INCR + PEXPIRE + PTTL) so the
// reply parsing and the allow/deny logic of RedisLimiter can be tested without
// a Redis server.
type fakeScripter struct {
	goredis.Scripter // unimplemented methods panic if reached
	counts           map[string]int64
	lastKey          string
	lastTTL          string
	err              error
	bogus            any
}

func (f *fakeScripter) EvalSha(ctx context.Context, _ string, keys []string, args ...interface{}) *goredis.Cmd {
	cmd := goredis.NewCmd(ctx)
	if f.err != nil {
		cmd.SetErr(f.err)
		return cmd
	}
	if f.bogus != nil {
		cmd.SetVal(f.bogus)
		return cmd
	}
	f.counts[keys[0]]++
	f.lastKey = keys[0]
	cmd.SetVal([]interface{}{f.counts[keys[0]], int64(args[0].(int64))})
	return cmd
}

func TestRedisLimiter(t *testing.T) {
	f := &fakeScripter{counts: map[string]int64{}}
	l := NewRedisLimiter(f, "")
	lim := Limit{Max: 2, Window: 30 * time.Second}
	r1, err := l.Allow(bg, "login:ip:1.2.3.4", lim)
	if err != nil || !r1.Allowed || r1.Remaining != 1 {
		t.Fatalf("1st: %+v %v", r1, err)
	}
	if f.lastKey != "aiworkforce:rl:login:ip:1.2.3.4" {
		t.Fatalf("key = %s", f.lastKey)
	}
	r2, _ := l.Allow(bg, "login:ip:1.2.3.4", lim)
	if !r2.Allowed || r2.Remaining != 0 {
		t.Fatalf("2nd: %+v", r2)
	}
	r3, _ := l.Allow(bg, "login:ip:1.2.3.4", lim)
	if r3.Allowed || r3.RetryAfter != 30*time.Second {
		t.Fatalf("3rd must be denied with TTL: %+v", r3)
	}
	if r, _ := l.Allow(bg, "other", lim); !r.Allowed {
		t.Fatal("independent keys")
	}
}

func TestRedisLimiterErrors(t *testing.T) {
	boom := errors.New("redis down")
	if _, err := NewRedisLimiter(&fakeScripter{err: boom}, "p:").Allow(bg, "k", Limit{Max: 1, Window: time.Second}); err == nil {
		t.Fatal("backend error must surface (callers choose fail-open/closed)")
	}
	if _, err := NewRedisLimiter(&fakeScripter{bogus: "weird"}, "p:").Allow(bg, "k", Limit{Max: 1, Window: time.Second}); err == nil {
		t.Fatal("malformed reply must error")
	}
	if _, err := NewRedisLimiter(&fakeScripter{bogus: []interface{}{"a", "b"}}, "p:").Allow(bg, "k", Limit{Max: 1, Window: time.Second}); err == nil {
		t.Fatal("wrong reply types must error")
	}
	if _, err := NewRedisLimiter(&fakeScripter{}, "p:").Allow(bg, "k", Limit{}); err == nil {
		t.Fatal("invalid limit must error")
	}
}
