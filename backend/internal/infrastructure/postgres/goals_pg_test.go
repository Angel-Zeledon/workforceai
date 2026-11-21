package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/goals"
)

// Company goals (migration 822): round trip, tenant isolation under RLS, and the link of a request to a goal (the
// composite foreign key refuses another organization's goal).
func TestGoalStorePostgres(t *testing.T) {
	st := testStore(t).Store
	gs := &GoalStore{S: st}
	ctx := context.Background()
	seedOrg(t, st, "org-g1")
	seedOrg(t, st, "org-g2")
	at := time.Now().UTC().Truncate(time.Microsecond)
	due := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)

	g := goals.Goal{ID: "goal-1", OfficeID: "d-legal", Title: "Contratos al día", Description: "Todos firmados", Status: goals.StatusActive,
		Period: "2026-T4", DueDate: &due, CreatedBy: "u-ana", CreatedAt: at, UpdatedAt: at}
	if err := gs.CreateGoal(ctx, "org-g1", g); err != nil {
		t.Fatal(err)
	}
	if err := gs.CreateGoal(ctx, "org-g1", g); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate = %v", err)
	}
	got, err := gs.GetGoal(ctx, "org-g1", "goal-1")
	if err != nil || got.Title != g.Title || got.OfficeID != "d-legal" || got.DueDate == nil || !got.DueDate.Equal(due) || got.Period != "2026-T4" {
		t.Fatalf("get = %+v %v", got, err)
	}
	if _, err := gs.GetGoal(ctx, "org-g2", "goal-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("another org reads it: %v", err)
	}
	if l, _ := gs.ListGoals(ctx, "org-g2"); len(l) != 0 {
		t.Fatalf("org-g2 lists %+v", l)
	}
	g.Status, g.DueDate, g.UpdatedAt = goals.StatusAchieved, nil, at.Add(time.Minute)
	if err := gs.UpdateGoal(ctx, "org-g1", g); err != nil {
		t.Fatal(err)
	}
	if err := gs.UpdateGoal(ctx, "org-g2", g); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("another org updates it: %v", err)
	}
	if l, _ := gs.ListGoals(ctx, "org-g1"); len(l) != 1 || l[0].Status != goals.StatusAchieved || l[0].DueDate != nil {
		t.Fatalf("list = %+v", l)
	}

	// a request keeps its goal; none reads back as ""; another organization's goal is refused by the database
	if err := st.CreateRequest(ctx, "org-g1", domain.Request{ID: "req-g1", Text: "x", Status: domain.RequestDone, CreatedAt: at, GoalID: "goal-1"}); err != nil {
		t.Fatal(err)
	}
	if r, err := st.GetRequest(ctx, "org-g1", "req-g1"); err != nil || r.GoalID != "goal-1" {
		t.Fatalf("request = %+v %v", r, err)
	}
	if err := st.CreateRequest(ctx, "org-g1", domain.Request{ID: "req-g0", Text: "x", Status: domain.RequestDone, CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if r, _ := st.GetRequest(ctx, "org-g1", "req-g0"); r.GoalID != "" {
		t.Fatalf("no goal = %q", r.GoalID)
	}
	if err := st.CreateRequest(ctx, "org-g2", domain.Request{ID: "req-g2", Text: "x", Status: domain.RequestDone, CreatedAt: at, GoalID: "goal-1"}); err == nil {
		t.Fatal("org-g2 linked org-g1's goal")
	}
}
