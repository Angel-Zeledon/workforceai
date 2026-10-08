package counters

import (
	"context"
	"testing"
)

func TestMemStoreRoundTripCopiesAndScopes(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	if b, err := s.LoadCounters(ctx, "o", ScopeAnomaly); b != nil || err != nil {
		t.Fatalf("empty = %s, %v", b, err)
	}
	in := []byte(`{"a":1}`)
	_ = s.SaveCounters(ctx, "o", ScopeAnomaly, in)
	in[2] = 'X' // the store keeps its own copy
	b, _ := s.LoadCounters(ctx, "o", ScopeAnomaly)
	if string(b) != `{"a":1}` {
		t.Fatalf("got %s", b)
	}
	if b, _ := s.LoadCounters(ctx, "o", ScopePolicyLimits); b != nil {
		t.Fatal("scopes must not mix")
	}
	if b, _ := s.LoadCounters(ctx, "other", ScopeAnomaly); b != nil {
		t.Fatal("organizations must not mix")
	}
}
