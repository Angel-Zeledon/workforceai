package policy

import (
	"math"
	"testing"
)

func TestCapAllowsIsHard(t *testing.T) {
	cases := []struct {
		name string
		in   CapInput
		want bool
	}{
		{"fits", CapInput{CapUSD: 1, SpentUSD: 0.5, EstimateUSD: 0.4}, true},
		{"fits exactly", CapInput{CapUSD: 1, SpentUSD: 0.6, ReservedUSD: 0.3, EstimateUSD: 0.1}, true},
		{"one cent over is refused", CapInput{CapUSD: 1, SpentUSD: 0.95, EstimateUSD: 0.06}, false},
		{"in-flight reservations count", CapInput{CapUSD: 1, SpentUSD: 0.5, ReservedUSD: 0.45, EstimateUSD: 0.1}, false},
		{"already over: nothing starts", CapInput{CapUSD: 1, SpentUSD: 1.2}, false},
		{"zero cap fails closed", CapInput{CapUSD: 0}, false},
		{"NaN fails closed", CapInput{CapUSD: 1, SpentUSD: math.NaN()}, false},
		{"negative estimate cannot buy room", CapInput{CapUSD: 1, SpentUSD: 1, EstimateUSD: -5}, true},
		{"negative estimate cannot buy room when over", CapInput{CapUSD: 1, SpentUSD: 1.01, EstimateUSD: -5}, false},
	}
	for _, c := range cases {
		if got := CapAllows(c.in); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestEffectiveCapStrictestWins(t *testing.T) {
	for _, c := range []struct{ cfg, plan, want float64 }{
		{0, 0, 0}, {10, 0, 10}, {0, 2, 2}, {10, 2, 2}, {1, 2, 1}, {-1, 0, 0},
	} {
		if got := EffectiveCap(c.cfg, c.plan); got != c.want {
			t.Errorf("EffectiveCap(%v,%v)=%v want %v", c.cfg, c.plan, got, c.want)
		}
	}
}

func TestEvaluateCapStates(t *testing.T) {
	if s := EvaluateCap(PlanFree, true, 1, 0.2, 0.1); s.State != CapOK || s.Paused || math.Abs(s.RemainingUSD-0.7) > 1e-9 {
		t.Fatalf("ok: %+v", s)
	}
	if s := EvaluateCap(PlanFree, true, 1, 0.7, 0.15); s.State != CapWarn || s.Paused {
		t.Fatalf("warn: %+v", s)
	}
	s := EvaluateCap(PlanFree, true, 1, 0.9, 0.1)
	if s.State != CapExhausted || !s.Paused || s.RemainingUSD != 0 || s.PercentUsed != 100 {
		t.Fatalf("exhausted: %+v", s)
	}
	if s := EvaluateCap(PlanFree, true, 1, 3, 0); !s.Paused || s.PercentUsed != 100 || s.EstimatedUSD != 3 {
		t.Fatalf("overage is reported, never hidden: %+v", s)
	}
	// A hard plan with no cap is paused, not unlimited; a soft plan never pauses.
	if s := EvaluateCap(PlanFree, true, 0, 0, 0); !s.Paused {
		t.Fatalf("hard without cap must pause: %+v", s)
	}
	if s := EvaluateCap(PlanPaid, false, 0, 99, 0); s.Paused || s.State != CapOK {
		t.Fatalf("paid without cap: %+v", s)
	}
	if s := EvaluateCap(PlanPaid, false, 10, 10, 0); s.Paused || s.State != CapExhausted {
		t.Fatalf("soft plan reports exhausted but is not paused by this layer: %+v", s)
	}
}
