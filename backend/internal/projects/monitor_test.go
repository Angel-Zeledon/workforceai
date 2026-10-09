package projects

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
)

type evCapture struct {
	mu sync.Mutex
	ev []domain.Event
}

func (c *evCapture) Publish(_ context.Context, e domain.Event) error {
	c.mu.Lock()
	c.ev = append(c.ev, e)
	c.mu.Unlock()
	return nil
}

func (c *evCapture) count(typ string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.ev {
		if e.Type == typ {
			n++
		}
	}
	return n
}

func newMonitorSvc(store Store, pub *evCapture) *Service {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &application.Recorder{OrgID: "o1", Store: memory.New(), Pub: pub, Log: log}
	return &Service{cfg: Config{Store: store, Rec: rec, Log: log, OrgID: "o1", Poll: 10 * time.Millisecond},
		live: map[string]*liveProject{}, byReq: map[string]*liveProject{}, locks: map[string]*sync.Mutex{}}
}

func detailWithSpend(spent float64) Detail { return Detail{Project: Summary{SpentUSD: spent}} }

func TestBudgetAlertEmitsOncePerCrossingAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	rec := Record{ID: "p1", OrgID: "o1", Name: "Big", Status: StatusRunning, BudgetUSD: 10, Budget: defaultBudgetPolicy(), Locale: "en", RequestID: "r1"}
	_ = store.Put(ctx, rec)
	pub := &evCapture{}
	s := newMonitorSvc(store, pub)

	if s.checkBudgetAlert(ctx, &rec, detailWithSpend(7.9)) || pub.count("project.budget_warning") != 0 {
		t.Fatal("below 80% must not alert")
	}
	if !s.checkBudgetAlert(ctx, &rec, detailWithSpend(8.1)) || pub.count("project.budget_warning") != 1 {
		t.Fatal("crossing 80% must alert once")
	}
	for _, spent := range []float64{8.5, 9.9, 10.4} {
		if s.checkBudgetAlert(ctx, &rec, detailWithSpend(spent)) {
			t.Fatalf("no repeat at %v", spent)
		}
	}
	// Restart: a new service re-reads the record; the persisted flag prevents a repeat.
	fresh, _ := store.Get(ctx, "o1", "p1")
	if warnedPct(fresh.BudgetWarned) != 80 {
		t.Fatalf("persisted warned = %v", fresh.BudgetWarned)
	}
	s2 := newMonitorSvc(store, pub)
	if s2.checkBudgetAlert(ctx, &fresh, detailWithSpend(9)) || pub.count("project.budget_warning") != 1 {
		t.Fatal("a restart must not repeat the alert")
	}
	// The budget is extended: spend falls under 80% (re-arm) and a new crossing alerts again.
	fresh.BudgetUSD = 20
	_ = store.Put(ctx, fresh)
	s2.checkBudgetAlert(ctx, &fresh, detailWithSpend(9))
	if !s2.checkBudgetAlert(ctx, &fresh, detailWithSpend(16.5)) || pub.count("project.budget_warning") != 2 {
		t.Fatal("after an extension the next crossing must alert")
	}
}

func TestBudgetAlertThresholds(t *testing.T) {
	s := newMonitorSvc(NewMemStore(), &evCapture{})
	def := Record{Budget: defaultBudgetPolicy()}
	if got := s.warnThresholds(def); len(got) != 1 || got[0] != 0.8 {
		t.Fatalf("default = %v", got)
	}
	s.cfg.BudgetWarnPct = 0.9
	if got := s.warnThresholds(def); got[0] != 0.9 {
		t.Fatalf("configured = %v", got)
	}
	custom := Record{Budget: BudgetPolicy{WarnAt: []float64{0.95, 0.6, 7}}}
	if got := s.warnThresholds(custom); len(got) != 2 || got[0] != 0.6 || got[1] != 0.95 {
		t.Fatalf("custom = %v", got)
	}
}

func TestMonitorBacksOffWhenIdleAndResetsOnChange(t *testing.T) {
	s := newMonitorSvc(NewMemStore(), &evCapture{})
	s.cfg.PollIdleMax = 640 * time.Millisecond
	lp := &liveProject{}
	for i := 0; i < busyTicks; i++ {
		if d := s.nextDelay(lp, false); d != s.cfg.Poll {
			t.Fatalf("tick %d: %v, want the fast period", i, d)
		}
	}
	prev := s.cfg.Poll
	for i := 0; i < 20; i++ {
		d := s.nextDelay(lp, false)
		if d < prev || d > s.cfg.PollIdleMax {
			t.Fatalf("delay %v must be non-decreasing and <= %v (prev %v)", d, s.cfg.PollIdleMax, prev)
		}
		prev = d
	}
	if prev != s.cfg.PollIdleMax {
		t.Fatalf("idle delay = %v, want the ceiling %v", prev, s.cfg.PollIdleMax)
	}
	if d := s.nextDelay(lp, true); d != s.cfg.Poll {
		t.Fatalf("a change must reset the period, got %v", d)
	}
}

func TestUnchangedSnapshotSkipsTheRebuildUntilTheFullRefresh(t *testing.T) {
	s := newMonitorSvc(NewMemStore(), &evCapture{})
	s.cfg.PollIdleMax = 80 * time.Millisecond
	lp := &liveProject{}
	snap := snapshot{rec: Record{ID: "p1", Status: StatusRunning}, tasks: map[string]domain.Task{"n1": {ID: "t1", Status: domain.TaskRunning}}}
	if !s.snapshotChanged(lp, snap) {
		t.Fatal("the first snapshot is always built")
	}
	if s.snapshotChanged(lp, snap) {
		t.Fatal("an identical snapshot must be skipped")
	}
	snap.tasks = map[string]domain.Task{"n1": {ID: "t1", Status: domain.TaskDone}}
	if !s.snapshotChanged(lp, snap) {
		t.Fatal("a task status change must rebuild")
	}
	snap.approvals = []domain.Approval{{ID: "a1", Status: domain.ApprovalPending}}
	if !s.snapshotChanged(lp, snap) {
		t.Fatal("a new approval must rebuild")
	}
	if s.snapshotChanged(lp, snap) {
		t.Fatal("unchanged again")
	}
	time.Sleep(100 * time.Millisecond)
	if !s.snapshotChanged(lp, snap) {
		t.Fatal("the periodic full refresh must run (guard / wait nodes live outside the snapshot)")
	}
}

func TestCalibrationFactorScalesTheETA(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	agent := "analyst"
	done := func(id string, est, took float64) Node {
		st, fin := t0, t0.Add(time.Duration(took*float64(time.Second)))
		return Node{ID: id, Kind: KindTask, AgentID: &agent, State: StateDone, EstSeconds: est, StartedAt: &st, FinishedAt: &fin}
	}
	nodes := []Node{done("a", 100, 200), done("b", 100, 300), done("c", 50, 100)} // ratios 2, 3, 2
	if f, n := calibrationFactor(nodes); n != 3 || f != 2 {
		t.Fatalf("factor = %v over %d, want the median 2 over 3", f, n)
	}
	if f, _ := calibrationFactor([]Node{done("a", 100, 1)}); f != 0.2 {
		t.Fatalf("clamped factor = %v", f)
	}
	rem := Node{ID: "r", Kind: KindTask, AgentID: &agent, State: StatePending, EstSeconds: 100, DagLevel: 1}
	mk := func(done ...Node) Health {
		d := Detail{Project: Summary{ID: "p", Status: StatusRunning}, Nodes: append(done, rem)}
		return computeHealth(d, t0)
	}
	base := mk() // no finished nodes: planning estimate
	cal := mk(nodes...)
	if cal.Schedule.CalibrationSamples != 3 || cal.Schedule.CalibrationFactor != 2 {
		t.Fatalf("calibration not reported: %+v", cal.Schedule)
	}
	b, c := base.Schedule.EtaP50.Sub(t0), cal.Schedule.EtaP50.Sub(t0)
	if b <= 0 || c != 2*b {
		t.Fatalf("calibrated ETA %v must be 2x the planning ETA %v", c, b)
	}
}
