package rewards

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"aiworkforce/backend/internal/application"
)

// Daily goal: one mini-goal per day (UTC), paid in coins ONLY because of work
// that was already paid by the normal rules. The goal never creates coins from
// nothing and never counts anything but real ledger entries:
//
//   - progress is derived from today's earn entries of finished tasks (the same
//     append-only ledger as everything else), so it cannot be inflated;
//   - the reward is one ledger entry with the idempotency key "earn:daily:<date>":
//     a day pays at most once, whatever the retries, restarts or replicas;
//   - the goal of the day is a pure function of the date (no randomness, no
//     per-user variation), so everybody sees the same, predictable goal.
const (
	ReasonDailyGoal = "daily_goal"
	RefDaily        = "daily"
)

// goalDef is one entry of the rotation.
type goalDef struct {
	ID     string // stable code; the UI translates "daily.<id>"
	Kind   string // tasks | first_try
	Target int
	Reward int64
}

// dailyRotation is deliberately small and modest: the rewards are a fraction of
// what the tasks themselves pay.
var dailyRotation = []goalDef{
	{ID: "one_task", Kind: "tasks", Target: 1, Reward: 10},
	{ID: "two_tasks", Kind: "tasks", Target: 2, Reward: 15},
	{ID: "first_try", Kind: "first_try", Target: 1, Reward: 15},
	{ID: "three_tasks", Kind: "tasks", Target: 3, Reward: 20},
}

// DailyGoal is the state of today's goal.
type DailyGoal struct {
	Date     string `json:"date"` // UTC, YYYY-MM-DD
	ID       string `json:"id"`
	Target   int    `json:"target"`
	Progress int    `json:"progress"`
	Reward   int64  `json:"reward"`
	Done     bool   `json:"done"`    // the reward was paid (today)
	Reached  bool   `json:"reached"` // the target is met
	Enabled  bool   `json:"enabled"`
	// ResetsAt is when the next goal starts.
	ResetsAt time.Time `json:"resets_at"`
}

func dayOf(t time.Time) (string, time.Time) {
	u := t.UTC()
	start := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	return start.Format("2006-01-02"), start
}

// goalFor picks the goal of a day.
func goalFor(start time.Time) goalDef {
	n := int(start.Unix() / 86400)
	return dailyRotation[((n%len(dailyRotation))+len(dailyRotation))%len(dailyRotation)]
}

// ledgerScan bounds how many entries of today are read.
const ledgerScan = 500

// DailyGoal returns today's goal and pays it the moment its target is met
// (idempotent). It never fails the caller: a ledger error reports progress 0.
func (s *Service) DailyGoal(ctx context.Context, org string) (DailyGoal, error) {
	now := time.Now()
	if s.Clock != nil {
		now = s.Clock()
	}
	date, start := dayOf(now)
	def := goalFor(start)
	g := DailyGoal{Date: date, ID: def.ID, Target: def.Target, Reward: def.Reward, Enabled: s.Rules.Enabled && s.Daily, ResetsAt: start.Add(24 * time.Hour)}
	if !g.Enabled {
		return g, nil
	}
	entries, err := s.Store.Ledger(ctx, org, ledgerScan, 0)
	if err != nil {
		return g, err
	}
	for _, e := range entries { // newest first
		if e.CreatedAt.Before(start) {
			break
		}
		if e.Kind != KindEarn {
			continue
		}
		switch {
		case e.Reason == ReasonDailyGoal && e.RefID == date:
			g.Done = true
		case e.RefKind == RefTask && (e.Reason == ReasonTaskDone || e.Reason == ReasonTaskFirstTry || e.Reason == ReasonTaskRework):
			if def.Kind == "tasks" || (def.Kind == "first_try" && e.Reason == ReasonTaskFirstTry) {
				g.Progress++
			}
		}
	}
	g.Reached = g.Progress >= g.Target
	if g.Reached && !g.Done {
		s.earn(ctx, org, Entry{Kind: KindEarn, Amount: def.Reward, Reason: ReasonDailyGoal, RefKind: RefDaily, RefID: date,
			Title: def.ID, Actor: "system", Detail: map[string]any{"goal": def.ID, "target": def.Target, "progress": g.Progress}, IdemKey: "earn:daily:" + date}, "")
		g.Done = true
	}
	return g, nil
}

// checkDaily is called after a task was paid: the goal may now be reached.
func (s *Service) checkDaily(ctx context.Context, org string) {
	if _, err := s.DailyGoal(ctx, org); err != nil {
		s.log().Warn("rewards: daily goal", "org", org, "err", err)
	}
}

// DailyGoalHandler serves GET /rewards/daily-goal. org reads the tenant of the
// request; write/fail are the API layer's JSON helpers.
func (s *Service) DailyGoalHandler(org func(*http.Request) string, write func(http.ResponseWriter, int, any), fail func(http.ResponseWriter, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		o := org(r)
		ctx := application.WithOrg(r.Context(), o)
		g, err := s.DailyGoal(ctx, o)
		if err != nil {
			fail(w, fmt.Errorf("daily goal: %w", err))
			return
		}
		write(w, http.StatusOK, g)
	}
}
