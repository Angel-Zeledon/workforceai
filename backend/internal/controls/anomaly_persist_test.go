package controls

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/counters"
)

// restarted builds a new Service (a new process) over the same controls and
// counters stores.
func restarted(t *testing.T, prev *Service, cs counters.Store, now *time.Time) (*Service, *captured) {
	t.Helper()
	c := &captured{}
	s := New(prev.Store,
		func(_ context.Context, _, _, action, _, _ string, _ map[string]any) {
			c.audits = append(c.audits, action)
		},
		func(_ context.Context, _, typ string, _ map[string]any) { c.events = append(c.events, typ) })
	s.SetEnvLevel(LevelNone)
	s.Now = func() time.Time { return *now }
	s.Counters = cs
	return s, c
}

// A7: the anomaly windows survive a restart when Counters is set.
func TestAnomalyCountersSurviveRestart(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	cs := counters.NewMemStore()
	s1, c1 := newSvc(t, &now)
	s1.Counters = cs
	ctx := context.Background()
	for i := 0; i < 4; i++ { // threshold is 5 within an hour
		s1.ObserveApprovalRejected(ctx, "o", "sales")
	}
	if len(c1.events) != 0 {
		t.Fatalf("4 rejections must not fire: %v", c1.events)
	}

	s2, c2 := restarted(t, s1, cs, &now)
	now = now.Add(time.Minute)
	s2.ObserveApprovalRejected(ctx, "o", "sales")
	if len(c2.events) != 1 || c2.events[0] != "anomaly.detected" {
		t.Fatalf("the 5th rejection after a restart must fire: %v", c2.events)
	}

	// The cooldown also survives: a third process does not fire again at once.
	s3, c3 := restarted(t, s2, cs, &now)
	now = now.Add(time.Minute)
	s3.ObserveApprovalRejected(ctx, "o", "sales")
	if len(c3.events) != 0 {
		t.Fatalf("cooldown forgotten after restart: %v", c3.events)
	}

	// Organizations are isolated.
	s3.ObserveApprovalRejected(ctx, "other", "sales")
	if len(c3.events) != 0 {
		t.Fatal("another organization inherited the counters")
	}
}

// Without persistence the old behavior stays: a restart forgets the windows.
func TestAnomalyCountersWithoutStoreAreForgotten(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	s1, _ := newSvc(t, &now)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		s1.ObserveApprovalRejected(ctx, "o", "sales")
	}
	s2, c2 := restarted(t, s1, nil, &now)
	s2.ObserveApprovalRejected(ctx, "o", "sales")
	if len(c2.events) != 0 {
		t.Fatalf("in-memory counters must start empty: %v", c2.events)
	}
}

type failingCounters struct{ counters.Store }

func (failingCounters) LoadCounters(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("db down")
}

// An unreadable snapshot skips the observation instead of clobbering the
// stored baseline with an empty one.
func TestAnomalyUnreadableCountersDoNotOverwrite(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	cs := counters.NewMemStore()
	_ = cs.SaveCounters(context.Background(), "o", counters.ScopeAnomaly, []byte(`{"rejections":["2026-10-07T09:59:00Z"]}`))
	s, c := newSvc(t, &now)
	s.Counters = failingCounters{cs}
	s.ObserveApprovalRejected(context.Background(), "o", "sales")
	if len(c.events) != 0 {
		t.Fatal("unexpected detection")
	}
	b, _ := cs.LoadCounters(context.Background(), "o", counters.ScopeAnomaly)
	if string(b) != `{"rejections":["2026-10-07T09:59:00Z"]}` {
		t.Fatalf("stored snapshot overwritten: %s", b)
	}
}
