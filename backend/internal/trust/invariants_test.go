package trust

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
)

// setLevel is the only writer of autonomy: without a human it can only lower it, and with a human it never goes
// above the role's ceiling.
func TestSetLevelGuards(t *testing.T) {
	var wrote []string
	s := New(Config{Store: NewMemStore(), SetAutonomy: func(_ context.Context, _, _, level string) error { wrote = append(wrote, level); return nil }})
	now := time.Now()
	sales := domain.Agent{ID: "sales", Role: "sales", Autonomy: LevelApproveEach} // amber: ceiling rules
	if err := s.setLevel(context.Background(), "o", sales, LevelRules, false, now); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("raise without a human: %v", err)
	}
	if err := s.setLevel(context.Background(), "o", domain.Agent{ID: "sales", Role: "sales", Autonomy: LevelRules}, LevelAutonomous, true, now); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("raise above the ceiling: %v", err)
	}
	if err := s.setLevel(context.Background(), "o", domain.Agent{ID: "x", Role: "unknown_role", Autonomy: LevelApproveEach}, LevelRules, true, now); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("unknown roles get the most restrictive ceiling: %v", err)
	}
	if err := s.setLevel(context.Background(), "o", sales, "root", true, now); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown level: %v", err)
	}
	if len(wrote) != 0 {
		t.Fatalf("refused changes wrote %v", wrote)
	}
	if err := s.setLevel(context.Background(), "o", sales, LevelSuggest, false, now); err != nil || len(wrote) != 1 {
		t.Fatalf("lowering needs no human: %v", err)
	}
	if err := s.setLevel(context.Background(), "o", sales, LevelRules, true, now); err != nil || len(wrote) != 2 {
		t.Fatalf("a human raise within the ceiling: %v", err)
	}
}
