package rewards_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/rewards"
)

func TestDailyGoalPaysOnceAndOnlyForFinishedWork(t *testing.T) {
	f := newFixture(t)
	f.svc.Daily = true
	// Coins that are not finished work (the welcome grant) are not progress.
	f.svc.GrantWelcome(f.ctx, "org-a", 1000)
	g, err := f.svc.DailyGoal(f.ctx, "org-a")
	if err != nil || g.Progress != 0 || g.Done || g.Reached || !g.Enabled || g.Target < 1 || g.Reward <= 0 {
		t.Fatalf("fresh goal: %+v %v", g, err)
	}
	if again, _ := f.svc.DailyGoal(f.ctx, "org-a"); again.ID != g.ID || again.Date != g.Date {
		t.Fatal("the goal of the day must be stable")
	}
	w0, _ := f.svc.Store.Wallet(f.ctx, "org-a")

	// Finish tasks with a first-try review so every kind of goal is reachable.
	for i := 0; i < 3; i++ {
		f.svc.OnTaskCompleted(f.ctx, "org-a", done(fmt.Sprintf("t%d", i), "agent-1", reviewPass()))
	}
	g, _ = f.svc.DailyGoal(f.ctx, "org-a")
	if !g.Reached || !g.Done {
		t.Fatalf("three first-try tasks must reach any goal: %+v", g)
	}
	w1, _ := f.svc.Store.Wallet(f.ctx, "org-a")
	tasks := int64(3 * 15) // three first-try M tasks: 10 + 5 bonus
	if w1.Balance-w0.Balance != tasks+g.Reward {
		t.Fatalf("balance moved %d, want tasks %d + goal %d", w1.Balance-w0.Balance, tasks, g.Reward)
	}
	// Asking again, or finishing more work, never pays the day a second time.
	for i := 0; i < 5; i++ {
		_, _ = f.svc.DailyGoal(f.ctx, "org-a")
	}
	f.svc.OnTaskCompleted(f.ctx, "org-a", done("t9", "agent-1", reviewPass()))
	w2, _ := f.svc.Store.Wallet(f.ctx, "org-a")
	if w2.Balance-w1.Balance != 15 {
		t.Fatalf("second payment of the day: moved %d, want only the task (15)", w2.Balance-w1.Balance)
	}
	ledger, _ := f.svc.Store.Ledger(f.ctx, "org-a", 100, 0)
	n := 0
	for _, e := range ledger {
		if e.Reason == rewards.ReasonDailyGoal {
			n++
			if e.Amount != g.Reward || e.RefID != g.Date {
				t.Fatalf("entry %+v", e)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d daily goal entries, want 1", n)
	}
}

func TestDailyGoalDoesNotPayWithoutWorkOrWhenDisabled(t *testing.T) {
	f := newFixture(t)
	f.svc.Daily = true
	for i := 0; i < 4; i++ {
		_, _ = f.svc.DailyGoal(f.ctx, "org-a")
	}
	w, _ := f.svc.Store.Wallet(f.ctx, "org-a")
	if w.Balance != 0 {
		t.Fatalf("goal paid without work: %d", w.Balance)
	}
	// A rejected task is not work either.
	f.svc.Rejected = func(_ context.Context, _, _, _ string) bool { return true }
	f.svc.OnTaskCompleted(f.ctx, "org-a", done("r1", "agent-1", nil))
	if g, _ := f.svc.DailyGoal(f.ctx, "org-a"); g.Progress != 0 {
		t.Fatalf("rejected task counted: %+v", g)
	}
	f.svc.Rejected = nil
	f.svc.Rules.Enabled = false
	g, _ := f.svc.DailyGoal(f.ctx, "org-a")
	if g.Enabled || g.Done {
		t.Fatalf("disabled: %+v", g)
	}
}

func TestDailyGoalIsPerOrganization(t *testing.T) {
	f := newFixture(t)
	f.svc.Daily = true
	for i := 0; i < 3; i++ {
		f.svc.OnTaskCompleted(f.ctx, "org-a", done(fmt.Sprintf("a%d", i), "agent-1", reviewPass()))
	}
	if g, _ := f.svc.DailyGoal(f.ctx, "org-b"); g.Progress != 0 || g.Done {
		t.Fatalf("org-b inherited the goal: %+v", g)
	}
}

func TestDailyGoalRotationAndReset(t *testing.T) {
	f := newFixture(t)
	f.svc.Daily = true
	day := time.Date(2026, 10, 19, 15, 30, 0, 0, time.UTC)
	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		d := day.AddDate(0, 0, i)
		f.svc.Clock = func() time.Time { return d }
		g, _ := f.svc.DailyGoal(f.ctx, "org-z")
		seen[g.ID] = true
		if !g.ResetsAt.Equal(time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("resets_at %v", g.ResetsAt)
		}
		// Same instant, same goal.
		if g2, _ := f.svc.DailyGoal(f.ctx, "org-z"); g2.ID != g.ID {
			t.Fatal("unstable")
		}
	}
	if len(seen) < 3 {
		t.Fatalf("the rotation should vary: %v", seen)
	}
}

func reviewPass() *domain.Review { return &domain.Review{Verdict: domain.VerdictPass} }

func TestDailyGoalOffByDefaultChangesNothing(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 3; i++ {
		f.svc.OnTaskCompleted(f.ctx, "org-a", done(fmt.Sprintf("x%d", i), "agent-1", reviewPass()))
	}
	g, _ := f.svc.DailyGoal(f.ctx, "org-a")
	w, _ := f.svc.Store.Wallet(f.ctx, "org-a")
	if g.Enabled || g.Done || w.Balance != 45 {
		t.Fatalf("goal %+v balance %d", g, w.Balance)
	}
}
