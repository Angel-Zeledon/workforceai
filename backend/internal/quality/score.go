// Package quality computes the advisory quality score and reputation of an agent (docs/architecture/quality-score.md).
//
// The score is derived only from signals the platform already records: how humans decided the agent's
// approvals, how its tasks ended, what the quality review (Q1) said, how many retries it needed, and the
// results of the organization's golden cases. It is a pure function of counts, so the same data always
// gives the same score, and the response carries the methodology (components, weights, sample sizes).
// It is advisory: nothing here changes an agent's autonomy or permissions.
package quality

import (
	"math"
	"time"
)

// Component keys.
const (
	CompApprovals = "approvals" // approved / (approved + rejected by a human)
	CompTasks     = "tasks"     // done / (done + failed)
	CompReview    = "review"    // reviewed outputs accepted without rework
	CompRetries   = "retries"   // 1 - retries per finished task (floored at 0)
	CompGolden    = "golden"    // passed golden-case runs / runs
)

// Status of a score.
const (
	StatusOK           = "ok"
	StatusInsufficient = "insufficient_data"
)

// Trend values.
const (
	TrendUp      = "up"
	TrendDown    = "down"
	TrendStable  = "stable"
	TrendUnknown = "unknown"
)

// Reputation levels (labels over the score; never shown without a score).
const (
	LevelExcellent = "excellent" // >= 90
	LevelGood      = "good"      // >= 75
	LevelFair      = "fair"      // >= 50
	LevelNeedsWork = "needs_attention"
)

// Window bounds in days.
const (
	DefaultWindowDays = 30
	MinWindowDays     = 7
	MaxWindowDays     = 90
)

const (
	// MinComponentSample is the least number of observations a component needs to count.
	MinComponentSample = 5
	// MinGoldenSample is the same for golden runs, which are scarcer by nature.
	MinGoldenSample = 3
	// MinTotalSample is the least total of observations over the counted components to show a score.
	MinTotalSample = 10
	// trendDelta is the change (in points) under which the trend is "stable".
	trendDelta = 5
	// MethodologyVersion changes whenever weights or definitions change.
	MethodologyVersion = 1
)

// Counts are the raw observations of one agent inside one time range.
type Counts struct {
	Approved        int `json:"approved"`
	Rejected        int `json:"rejected"`
	TasksDone       int `json:"tasks_done"`
	TasksFailed     int `json:"tasks_failed"`
	Reviewed        int `json:"reviewed"`          // outputs the quality review judged
	ReviewFirstPass int `json:"review_first_pass"` // of those, accepted with no rework
	Retries         int `json:"retries"`           // task.retrying events
	// RetriesTracked says the source can count retries at all; without it the retries component has no sample
	// (an untracked signal must not look like "no retries").
	RetriesTracked bool `json:"retries_tracked"`
	GoldenRuns     int  `json:"golden_runs"`
	GoldenPassed   int  `json:"golden_passed"`
}

type spec struct {
	key    string
	weight float64
	min    int
}

// specs is the methodology. Weights sum to 1; a component below its minimum sample is left out
// and the remaining weights are renormalized, so a missing signal never counts as a bad one.
var specs = []spec{
	{CompApprovals, 0.30, MinComponentSample},
	{CompTasks, 0.25, MinComponentSample},
	{CompReview, 0.15, MinComponentSample},
	{CompRetries, 0.10, MinComponentSample},
	{CompGolden, 0.20, MinGoldenSample},
}

// Component is one scored signal. Rate is nil when the sample is too small.
type Component struct {
	Key      string   `json:"key"`
	Weight   float64  `json:"weight"`           // nominal weight
	Used     float64  `json:"effective_weight"` // weight after renormalization (0 when left out)
	Rate     *float64 `json:"rate"`             // 0..1
	Sample   int      `json:"sample"`
	MinNeeds int      `json:"min_sample"`
	Included bool     `json:"included"`
}

// Result is the score of one counted range.
type Result struct {
	Status     string      `json:"status"`
	Score      *int        `json:"score"` // 0..100, nil when status is insufficient_data
	Level      string      `json:"level,omitempty"`
	Components []Component `json:"components"`
	Sample     int         `json:"sample"` // observations over the included components
}

func pct(num, den int) float64 { return float64(num) / float64(den) }

// rateOf returns the rate and sample of one component.
func rateOf(key string, c Counts) (rate float64, n int) {
	switch key {
	case CompApprovals:
		n = c.Approved + c.Rejected
		if n > 0 {
			rate = pct(c.Approved, n)
		}
	case CompTasks:
		n = c.TasksDone + c.TasksFailed
		if n > 0 {
			rate = pct(c.TasksDone, n)
		}
	case CompReview:
		n = c.Reviewed
		if n > 0 {
			rate = pct(min(c.ReviewFirstPass, n), n)
		}
	case CompRetries:
		if c.RetriesTracked {
			n = c.TasksDone + c.TasksFailed
		}
		if n > 0 {
			rate = math.Max(0, 1-pct(c.Retries, n))
		}
	case CompGolden:
		n = c.GoldenRuns
		if n > 0 {
			rate = pct(min(c.GoldenPassed, n), n)
		}
	}
	return rate, n
}

// Level maps a score to its reputation label.
func Level(score int) string {
	switch {
	case score >= 90:
		return LevelExcellent
	case score >= 75:
		return LevelGood
	case score >= 50:
		return LevelFair
	}
	return LevelNeedsWork
}

// Compute scores one range of counts.
func Compute(c Counts) Result {
	res := Result{Status: StatusInsufficient, Components: make([]Component, 0, len(specs))}
	var wsum, acc float64
	for _, sp := range specs {
		rate, n := rateOf(sp.key, c)
		comp := Component{Key: sp.key, Weight: sp.weight, Sample: n, MinNeeds: sp.min}
		if n >= sp.min {
			r := rate
			comp.Rate, comp.Included = &r, true
			wsum += sp.weight
			acc += sp.weight * rate
			res.Sample += n
		}
		res.Components = append(res.Components, comp)
	}
	if wsum == 0 || res.Sample < MinTotalSample {
		return res
	}
	for i := range res.Components {
		if res.Components[i].Included {
			res.Components[i].Used = round3(res.Components[i].Weight / wsum)
		}
	}
	s := int(math.Round(100 * acc / wsum))
	res.Status, res.Score, res.Level = StatusOK, &s, Level(s)
	return res
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

// Trend compares the current range with the previous one of the same length. It is "unknown"
// unless both have a score.
func Trend(cur, prev Result) (string, *int) {
	if cur.Score == nil || prev.Score == nil {
		return TrendUnknown, nil
	}
	d := *cur.Score - *prev.Score
	switch {
	case d >= trendDelta:
		return TrendUp, &d
	case d <= -trendDelta:
		return TrendDown, &d
	}
	return TrendStable, &d
}

// ClampWindow bounds a requested window in days (0 = default).
func ClampWindow(days int) int {
	if days <= 0 {
		return DefaultWindowDays
	}
	return min(max(days, MinWindowDays), MaxWindowDays)
}

// Range is [From, To).
type Range struct{ From, To time.Time }

// In reports whether t is inside the range.
func (r Range) In(t time.Time) bool { return !t.Before(r.From) && t.Before(r.To) }

// Ranges returns the current window ending at now and the previous one of the same length.
func Ranges(now time.Time, days int) (cur, prev Range) {
	d := time.Duration(days) * 24 * time.Hour
	cur = Range{From: now.Add(-d), To: now.Add(time.Nanosecond)}
	prev = Range{From: now.Add(-2 * d), To: cur.From}
	return
}

// MethodComponent describes one component in the methodology block.
type MethodComponent struct {
	Key       string  `json:"key"`
	Weight    float64 `json:"weight"`
	MinSample int     `json:"min_sample"`
}

// Methodology is returned with every response so the number is never opaque.
type Methodology struct {
	Version        int               `json:"version"`
	WindowDays     int               `json:"window_days"`
	Components     []MethodComponent `json:"components"`
	MinTotalSample int               `json:"min_total_sample"`
	TrendDelta     int               `json:"trend_delta"`
	Advisory       bool              `json:"advisory"` // always true: the score never changes autonomy or permissions
}

// Describe builds the methodology block for a window.
func Describe(days int) Methodology {
	m := Methodology{Version: MethodologyVersion, WindowDays: days, MinTotalSample: MinTotalSample, TrendDelta: trendDelta, Advisory: true}
	for _, sp := range specs {
		m.Components = append(m.Components, MethodComponent{Key: sp.key, Weight: sp.weight, MinSample: sp.min})
	}
	return m
}
