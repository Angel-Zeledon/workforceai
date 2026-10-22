package trust

import (
	"math/rand"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// past probation: tracked 20 days ago with plenty of decisions
func base(level, ceiling string, c Counts) Input {
	now := t0.Add(20 * 24 * time.Hour)
	return Input{Level: level, Ceiling: ceiling, Now: now, TrackingSince: t0, LevelSince: t0, ProbationCounts: c, Counts: c}
}

func TestLadder(t *testing.T) {
	if Up(LevelApproveEach) != LevelRules || Up(LevelAutonomous) != "" || Up("bogus") != "" {
		t.Fatal("Up")
	}
	if Down(LevelRules) != LevelApproveEach || Down(LevelSuggest) != "" || Down("bogus") != "" {
		t.Fatal("Down")
	}
	if Rank(LevelSuggest) != 1 || Rank(LevelAutonomous) != 4 || Rank("x") != 0 {
		t.Fatal("Rank")
	}
}

func TestPromotionNeedsStrongEvidence(t *testing.T) {
	cases := []struct {
		name string
		c    Counts
		q    *Quality
		want string
	}{
		{"20 of 20 approved", Counts{Approved: 20, TasksDone: 10}, nil, RecPromote},
		{"19 of 20 is exactly 95 %", Counts{Approved: 19, Rejected: 1}, nil, RecPromote},
		{"18 of 20 is not enough", Counts{Approved: 18, Rejected: 2}, nil, RecGatheringEv},
		{"too few decisions", Counts{Approved: 19}, nil, RecGatheringEv},
		{"one policy denial blocks it", Counts{Approved: 30, PolicyDenials: 1}, nil, RecGatheringEv},
		{"tasks failing", Counts{Approved: 30, TasksDone: 8, TasksFailed: 2}, nil, RecGatheringEv},
		{"few tasks are not judged", Counts{Approved: 30, TasksDone: 1, TasksFailed: 2}, nil, RecPromote},
		{"good quality", Counts{Approved: 20}, &Quality{Score: 88, Trend: "stable"}, RecPromote},
		{"quality under the bar", Counts{Approved: 20}, &Quality{Score: 70, Trend: "up"}, RecGatheringEv},
		{"quality falling", Counts{Approved: 20}, &Quality{Score: 90, Trend: "down"}, RecGatheringEv},
	}
	for _, tc := range cases {
		in := base(LevelApproveEach, LevelAutonomous, tc.c)
		in.Quality = tc.q
		a := Assess(in)
		if a.Recommendation != tc.want {
			t.Errorf("%s: got %s, want %s (criteria %+v)", tc.name, a.Recommendation, tc.want, a.Criteria)
		}
		if tc.want == RecPromote && (a.Target != LevelRules || a.Progress != 1) {
			t.Errorf("%s: target %q progress %v", tc.name, a.Target, a.Progress)
		}
		if tc.want != RecPromote && (a.Target != "" || a.Progress >= 1) {
			t.Errorf("%s: no promotion must have no target and progress < 1: %q %v", tc.name, a.Target, a.Progress)
		}
	}
}

func TestProbationAndCooldown(t *testing.T) {
	c := Counts{Approved: 25}
	in := base(LevelApproveEach, LevelAutonomous, c)
	in.Now = t0.Add(5 * 24 * time.Hour) // only 5 days tracked
	if a := Assess(in); a.Recommendation != RecProbation || !a.Probation.Active {
		t.Fatalf("young agent: %+v", a)
	}
	in = base(LevelApproveEach, LevelAutonomous, c)
	in.ProbationCounts = Counts{Approved: 3} // old enough, too few decisions since tracking started
	if a := Assess(in); a.Recommendation != RecProbation {
		t.Fatalf("few decisions: %s", a.Recommendation)
	}
	// probation never stops a demotion
	in.Counts = Counts{Approved: 5, Rejected: 1, HighRiskRejected: 1}
	in.Now = t0.Add(time.Hour)
	if a := Assess(in); a.Recommendation != RecDemoteAuto || a.Target != LevelSuggest {
		t.Fatalf("demotion on probation: %+v", a)
	}
	// declined 2 days ago: cooldown; 8 days ago: suggested again
	in = base(LevelApproveEach, LevelAutonomous, c)
	d := in.Now.Add(-2 * 24 * time.Hour)
	in.LastDeclined = &d
	if a := Assess(in); a.Recommendation != RecCooldown || a.Target != "" {
		t.Fatalf("cooldown: %+v", a)
	}
	d = in.Now.Add(-8 * 24 * time.Hour)
	if a := Assess(in); a.Recommendation != RecPromote {
		t.Fatalf("after cooldown: %s", a.Recommendation)
	}
}

func TestDemotionTriggers(t *testing.T) {
	cases := []struct {
		name string
		c    Counts
		want string
		trig string
	}{
		{"rejected high-risk action", Counts{Approved: 30, Rejected: 1, HighRiskRejected: 1}, RecDemoteAuto, TrigHighRisk},
		{"burst of rejections", Counts{Approved: 30, Rejected: 3, RecentRejected: 3}, RecDemoteAuto, TrigBurst},
		{"policy incident", Counts{Approved: 30, PolicyDenials: 3}, RecDemoteAuto, TrigPolicy},
		{"low approval rate", Counts{Approved: 7, Rejected: 3}, RecDemote, TrigApprovalRate},
		{"failing tasks", Counts{Approved: 5, TasksDone: 3, TasksFailed: 3}, RecDemote, TrigTaskFailure},
		{"two old rejections are no burst", Counts{Approved: 30, Rejected: 2, RecentRejected: 2}, RecGatheringEv, ""},
	}
	for _, tc := range cases {
		a := Assess(base(LevelRules, LevelAutonomous, tc.c))
		if a.Recommendation != tc.want {
			t.Errorf("%s: got %s want %s", tc.name, a.Recommendation, tc.want)
			continue
		}
		if tc.trig != "" {
			if a.Target != LevelApproveEach {
				t.Errorf("%s: one level down, got %q", tc.name, a.Target)
			}
			fired := false
			for _, tr := range a.Triggers {
				fired = fired || (tr.Key == tc.trig && tr.Fired)
			}
			if !fired {
				t.Errorf("%s: trigger %s did not fire: %+v", tc.name, tc.trig, a.Triggers)
			}
		}
	}
	// nothing below suggest
	if a := Assess(base(LevelSuggest, LevelAutonomous, Counts{Rejected: 5, HighRiskRejected: 5})); a.Target != "" || a.Recommendation != RecNone {
		t.Fatalf("bottom: %+v", a)
	}
	// low quality suggests a demotion
	in := base(LevelRules, LevelAutonomous, Counts{Approved: 20})
	in.Quality = &Quality{Score: 40, Trend: "stable"}
	if a := Assess(in); a.Recommendation != RecDemote {
		t.Fatalf("low quality: %s", a.Recommendation)
	}
}

// Invariant: whatever the evidence, a promotion is exactly one level up and never above the ceiling, and a
// demotion is exactly one level down.
func TestNeverExceedsCeilingProperty(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 20000; i++ {
		level := Ladder[r.Intn(4)]
		ceiling := []string{LevelSuggest, LevelApproveEach, LevelRules, LevelAutonomous, "", "bogus"}[r.Intn(6)]
		c := Counts{Approved: r.Intn(60), Rejected: r.Intn(3)}
		if r.Intn(4) == 0 {
			c.HighRiskRejected = r.Intn(2)
		}
		in := base(level, ceiling, c)
		if r.Intn(3) == 0 {
			in.Quality = &Quality{Score: r.Intn(101), Trend: []string{"up", "down", "stable", "unknown"}[r.Intn(4)]}
		}
		a := Assess(in)
		switch a.Recommendation {
		case RecPromote:
			if Rank(a.Target) != Rank(level)+1 || Rank(a.Target) > Rank(ceiling) || Rank(ceiling) == 0 {
				t.Fatalf("promotion %s -> %s with ceiling %q", level, a.Target, ceiling)
			}
		case RecDemote, RecDemoteAuto:
			if Rank(a.Target) != Rank(level)-1 {
				t.Fatalf("demotion %s -> %s", level, a.Target)
			}
		default:
			if a.Target != "" {
				t.Fatalf("%s carries a target %q", a.Recommendation, a.Target)
			}
		}
		if Rank(level) >= Rank(ceiling) && a.Recommendation == RecPromote {
			t.Fatalf("promotion at or above the ceiling: %s/%s", level, ceiling)
		}
	}
}

func TestMethodologyIsComplete(t *testing.T) {
	m := Describe()
	if !m.PromotionNeedsHuman || !m.AutoDemotion || len(m.Promotion) != 6 || len(m.Demotion) != 6 || m.WindowDays != WindowDays {
		t.Fatalf("methodology: %+v", m)
	}
}
