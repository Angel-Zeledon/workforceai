package policy

import (
	"testing"
	"time"
)

func TestUsageSnapshotPrunesAndRestores(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	cfg := Config{Limits: []Limit{{ID: "mail", Agent: "*", Tool: "email", Action: "send*", Window: time.Hour, MaxCalls: 2}}}
	e, err := NewEngine(cfg, nil, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	e.RestoreUsage(map[string][]UsageEvent{
		"mail|sales": {{At: now.Add(-2 * time.Hour)}, {At: now.Add(-10 * time.Minute), Amount: 5}},
		"gone|sales": {{At: now}}, // limit no longer configured
	})
	snap := e.UsageSnapshot()
	if len(snap) != 1 || len(snap["mail|sales"]) != 1 || snap["mail|sales"][0].Amount != 5 {
		t.Fatalf("snapshot = %+v", snap)
	}
	e2, _ := NewEngine(cfg, nil, WithClock(func() time.Time { return now }))
	e2.RestoreUsage(snap)
	if n, sum := e2.window("mail|sales", time.Hour); n != 1 || sum != 5 {
		t.Fatalf("restored window = %d, %v", n, sum)
	}
}
