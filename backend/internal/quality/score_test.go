package quality

import (
	"testing"
	"time"
)

func TestComputeNoData(t *testing.T) {
	r := Compute(Counts{})
	if r.Status != StatusInsufficient || r.Score != nil || r.Level != "" {
		t.Fatalf("no data must not produce a score: %+v", r)
	}
	if len(r.Components) != len(specs) {
		t.Fatalf("components are always listed: %d", len(r.Components))
	}
	for _, c := range r.Components {
		if c.Included || c.Rate != nil || c.Used != 0 {
			t.Fatalf("empty component counted: %+v", c)
		}
	}
}

func TestComputeTinySampleIsInsufficient(t *testing.T) {
	// 4 approvals (< 5) and 4 tasks (< 5): nothing counts, even if all went well.
	r := Compute(Counts{Approved: 4, TasksDone: 4})
	if r.Status != StatusInsufficient || r.Score != nil {
		t.Fatalf("tiny sample: %+v", r)
	}
	// One component at its minimum is still under the total sample.
	r = Compute(Counts{Approved: 5})
	if r.Status != StatusInsufficient || r.Score != nil {
		t.Fatalf("5 observations: %+v", r)
	}
	if !r.Components[0].Included || r.Components[0].Sample != 5 {
		t.Fatalf("approvals reached its minimum: %+v", r.Components[0])
	}
}

func TestComputeAllRejected(t *testing.T) {
	r := Compute(Counts{Rejected: 12})
	if r.Status != StatusOK || r.Score == nil || *r.Score != 0 || r.Level != LevelNeedsWork {
		t.Fatalf("all rejected: %+v", r)
	}
	if r.Components[0].Used != 1 {
		t.Fatalf("the only counted component carries the whole weight: %+v", r.Components[0])
	}
}

func TestComputeMixedRenormalizesWeights(t *testing.T) {
	// approvals 8/10 (0.30), tasks 6/10 (0.25); review, retries and golden below their minimum.
	// Retries share the task sample (10) but there are none, so 1 - 0/10 = 1 with weight 0.10.
	r := Compute(Counts{Approved: 8, Rejected: 2, TasksDone: 6, TasksFailed: 4, RetriesTracked: true})
	if r.Status != StatusOK || r.Score == nil {
		t.Fatalf("mixed: %+v", r)
	}
	// (0.30*0.8 + 0.25*0.6 + 0.10*1.0) / 0.65 = 0.49 / 0.65 = 0.7538
	if *r.Score != 75 || r.Level != LevelGood {
		t.Fatalf("score = %d level %s", *r.Score, r.Level)
	}
	var sum float64
	for _, c := range r.Components {
		sum += c.Used
	}
	if sum < 0.998 || sum > 1.002 {
		t.Fatalf("effective weights must sum to 1, got %v", sum)
	}
	if r.Sample != 30 {
		t.Fatalf("sample = %d", r.Sample)
	}
}

func TestComputeRetriesFloorAndReviewClamp(t *testing.T) {
	r := Compute(Counts{TasksDone: 10, Retries: 50, RetriesTracked: true, Reviewed: 5, ReviewFirstPass: 9})
	for _, c := range r.Components {
		switch c.Key {
		case CompRetries:
			if c.Rate == nil || *c.Rate != 0 {
				t.Fatalf("retries are floored at 0: %+v", c)
			}
		case CompReview:
			if c.Rate == nil || *c.Rate != 1 {
				t.Fatalf("first-pass is clamped to the reviewed count: %+v", c)
			}
		}
	}
}

func TestComputeGoldenCountsFromThree(t *testing.T) {
	r := Compute(Counts{Approved: 10, GoldenRuns: 3, GoldenPassed: 0})
	if r.Score == nil {
		t.Fatalf("expected a score: %+v", r)
	}
	// (0.30*1 + 0.20*0) / 0.50 = 0.6
	if *r.Score != 60 {
		t.Fatalf("score = %d", *r.Score)
	}
}

func TestLevelBoundaries(t *testing.T) {
	for score, want := range map[int]string{100: LevelExcellent, 90: LevelExcellent, 89: LevelGood, 75: LevelGood, 74: LevelFair, 50: LevelFair, 49: LevelNeedsWork, 0: LevelNeedsWork} {
		if got := Level(score); got != want {
			t.Fatalf("Level(%d) = %s, want %s", score, got, want)
		}
	}
}

func score(n int) Result { return Result{Status: StatusOK, Score: &n} }

func TestTrend(t *testing.T) {
	if tr, d := Trend(score(80), Result{Status: StatusInsufficient}); tr != TrendUnknown || d != nil {
		t.Fatalf("no previous score: %s %v", tr, d)
	}
	cases := []struct {
		cur, prev int
		want      string
		delta     int
	}{{80, 70, TrendUp, 10}, {70, 80, TrendDown, -10}, {80, 76, TrendStable, 4}, {75, 80, TrendDown, -5}, {80, 75, TrendUp, 5}}
	for _, c := range cases {
		tr, d := Trend(score(c.cur), score(c.prev))
		if tr != c.want || d == nil || *d != c.delta {
			t.Fatalf("Trend(%d,%d) = %s %v", c.cur, c.prev, tr, d)
		}
	}
}

func TestWindowCutoff(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	cur, prev := Ranges(now, 30)
	at := func(r Range, ok bool, tm time.Time) {
		t.Helper()
		if r.In(tm) != ok {
			t.Fatalf("In(%v) = %v, want %v", tm, !ok, ok)
		}
	}
	at(cur, true, now)
	at(cur, true, now.Add(-30*24*time.Hour))               // the boundary belongs to the current window
	at(cur, false, now.Add(-30*24*time.Hour-time.Second))  // just before it
	at(prev, true, now.Add(-30*24*time.Hour-time.Second))  // ... falls in the previous window
	at(prev, false, now.Add(-30*24*time.Hour))             // windows never overlap
	at(prev, false, now.Add(-60*24*time.Hour-time.Second)) // older than two windows is ignored
	at(cur, false, now.Add(time.Hour))                     // the future is ignored
}

func TestClampWindow(t *testing.T) {
	for in, want := range map[int]int{-3: DefaultWindowDays, 0: DefaultWindowDays, 1: MinWindowDays, 30: 30, 90: 90, 500: MaxWindowDays} {
		if got := ClampWindow(in); got != want {
			t.Fatalf("ClampWindow(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestMethodologyWeightsSumToOne(t *testing.T) {
	m := Describe(30)
	var sum float64
	for _, c := range m.Components {
		sum += c.Weight
	}
	if sum < 0.999 || sum > 1.001 || !m.Advisory || m.WindowDays != 30 {
		t.Fatalf("methodology: %+v (sum %v)", m, sum)
	}
}
