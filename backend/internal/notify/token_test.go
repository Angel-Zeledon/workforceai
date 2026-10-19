package notify

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func newTestSigner(t *testing.T) (*Signer, *time.Time) {
	t.Helper()
	s, err := NewSigner(testKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	return s.WithClock(func() time.Time { return now }), &now
}

func TestSignVerifyRoundTrip(t *testing.T) {
	s, _ := newTestSigner(t)
	tok, err := s.Sign(Claims{Org: "o1", Approval: "a1", Decision: DecisionApprove, To: "x@y.z"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Verify(tok)
	if err != nil || c.Org != "o1" || c.Approval != "a1" || c.Decision != DecisionApprove || c.To != "x@y.z" || c.JTI == "" {
		t.Fatalf("verify: %+v %v", c, err)
	}
}

func TestShortKeyRefused(t *testing.T) {
	if _, err := NewSigner([]byte("short")); err == nil {
		t.Fatal("a short key must be refused")
	}
}

func TestSignRejectsBadClaims(t *testing.T) {
	s, _ := newTestSigner(t)
	for _, c := range []Claims{{}, {Org: "o", Approval: "a", Decision: "maybe"}, {Org: "o", Decision: DecisionApprove}} {
		if _, err := s.Sign(c, time.Hour); !errors.Is(err, ErrMalformed) {
			t.Errorf("%+v: want ErrMalformed, got %v", c, err)
		}
	}
	if _, err := s.Sign(Claims{Org: "o", Approval: "a", Decision: DecisionReject}, 0); !errors.Is(err, ErrMalformed) {
		t.Errorf("zero ttl must be refused, got %v", err)
	}
}

func TestVerifyExpiry(t *testing.T) {
	s, now := newTestSigner(t)
	tok, _ := s.Sign(Claims{Org: "o", Approval: "a", Decision: DecisionApprove}, time.Hour)
	*now = now.Add(59 * time.Minute)
	if _, err := s.Verify(tok); err != nil {
		t.Fatalf("still valid: %v", err)
	}
	*now = now.Add(2 * time.Minute)
	if _, err := s.Verify(tok); !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestVerifyTamper(t *testing.T) {
	s, _ := newTestSigner(t)
	tok, _ := s.Sign(Claims{Org: "o", Approval: "a", Decision: DecisionReject}, time.Hour)
	p, sig, _ := strings.Cut(tok, ".")

	// Flip the decision by re-encoding a different payload under the old signature.
	forged, _ := s.Sign(Claims{Org: "o", Approval: "a", Decision: DecisionApprove}, time.Hour)
	fp, _, _ := strings.Cut(forged, ".")
	if _, err := s.Verify(fp + "." + sig); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("swapped payload: %v", err)
	}
	// Flip one signature byte.
	bad := []byte(sig)
	if bad[0] == 'A' {
		bad[0] = 'B'
	} else {
		bad[0] = 'A'
	}
	if _, err := s.Verify(p + "." + string(bad)); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("flipped signature: %v", err)
	}
	// Another key never verifies.
	other, _ := NewSigner([]byte("ffffffffffffffffffffffffffffffff"))
	if _, err := other.Verify(tok); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("other key: %v", err)
	}
	for _, junk := range []string{"", "x", "a.b", ".", "..", strings.Repeat("a", 3000) + ".b"} {
		if _, err := s.Verify(junk); err == nil {
			t.Errorf("junk %q verified", junk)
		}
	}
}

func TestReplayIsSingleUse(t *testing.T) {
	r := NewMemReplay()
	ctx := context.Background()
	exp := time.Now().Add(time.Hour)
	if err := r.Consume(ctx, "o", "j1", exp); err != nil {
		t.Fatal(err)
	}
	if err := r.Consume(ctx, "o", "j1", exp); !errors.Is(err, ErrUsed) {
		t.Fatalf("second use: %v", err)
	}
	// Same jti in another organization is a different token.
	if err := r.Consume(ctx, "o2", "j1", exp); err != nil {
		t.Fatalf("other org: %v", err)
	}
}

func TestReplayRaceExactlyOneWins(t *testing.T) {
	r := NewMemReplay()
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r.Consume(context.Background(), "o", "same", time.Now().Add(time.Hour)) == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d winners, want 1", wins.Load())
	}
}

func TestTokensAreUnique(t *testing.T) {
	s, _ := newTestSigner(t)
	a, _ := s.Sign(Claims{Org: "o", Approval: "a", Decision: DecisionApprove}, time.Hour)
	b, _ := s.Sign(Claims{Org: "o", Approval: "a", Decision: DecisionApprove}, time.Hour)
	if a == b {
		t.Fatal("two tokens for the same approval must differ (jti)")
	}
}
