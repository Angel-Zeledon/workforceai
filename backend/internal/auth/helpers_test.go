package auth

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fastParams keeps argon2id cheap in tests (still real argon2id).
var fastParams = PasswordParams{Memory: 8, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

var testSecret = []byte("0123456789abcdef0123456789abcdef-test")

type env struct {
	svc   *Service
	store *MemoryStore
	clock *fakeClock
}

func newEnv(t *testing.T, mut ...func(*Config)) *env {
	t.Helper()
	st := NewMemoryStore()
	clk := newClock()
	cfg := Config{Store: st, Secret: testSecret, PasswordParams: fastParams, Now: clk.Now}
	for _, m := range mut {
		m(&cfg)
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &env{svc: svc, store: st, clock: clk}
}

const goodPW = "Correct-Horse-9"

func (e *env) register(t *testing.T, email, org string) *Session {
	t.Helper()
	s, err := e.svc.Register(context.Background(), RegisterInput{
		Email: email, Password: goodPW, Name: "Test User", OrgName: org + " Inc",
	}, ClientMeta{})
	if err != nil {
		t.Fatalf("register %s: %v", email, err)
	}
	return s
}

func (e *env) principal(t *testing.T, s *Session) Principal {
	t.Helper()
	p, err := e.svc.Authenticate(context.Background(), s.AccessToken)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	return p
}

// addUser registers a user in its own org, then adds it to org of `owner`
// with the given role, and returns the new user's id.
func (e *env) addUserTo(t *testing.T, owner *Session, email string, role Role) string {
	t.Helper()
	other := e.register(t, email, "Personal "+email)
	_, err := e.svc.AddMember(context.Background(), e.principal(t, owner), email, role)
	if err != nil {
		t.Fatalf("add member: %v", err)
	}
	return other.User.ID
}
