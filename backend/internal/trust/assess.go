// Package trust is the trust ladder of an agent (docs/architecture/trust-ladder.md, feature B1): autonomy is
// earned, not switched on.
//
// Every agent is tracked from the moment the platform first sees it (hire or first evaluation) and starts on
// probation at its current level. From the decisions people already make (approvals approved or rejected), the
// policy engine's denials, how its tasks end and, when available, the B11 quality score, a pure function (Assess)
// decides one of three things:
//
//   - promote: the evidence is strong enough to SUGGEST one level up, never above the role's ceiling
//     (roles.AutonomyCeiling: template ceiling and risk tier). A promotion is only a proposal; it changes nothing
//     until a human with the right role accepts it (Service.Accept). Nothing in this package raises autonomy on its
//     own: the only code path that raises it requires a human actor.
//   - demote (suggested): the record is weak; a human decides.
//   - demote (automatic): a hard incident (a rejected high-risk action, a burst of rejections, repeated policy
//     denials). Lowering autonomy only adds human review, so it is applied at once, one level, audited, and can be
//     reverted by a human within RevertDays.
//
// After any change of level the evidence starts again: what counts is what happened at the current level.
package trust

import (
	"math"
	"time"
)

// Autonomy levels, from most to least supervision.
const (
	LevelSuggest     = "suggest"
	LevelApproveEach = "approve_each"
	LevelRules       = "rules"
	LevelAutonomous  = "autonomous"
)

// Ladder is the order of the levels (index = rank - 1).
var Ladder = []string{LevelSuggest, LevelApproveEach, LevelRules, LevelAutonomous}

// Rank of a level (suggest=1 .. autonomous=4; unknown = 0).
func Rank(level string) int {
	for i, l := range Ladder {
		if l == level {
			return i + 1
		}
	}
	return 0
}

// Up returns the next level, or "" at the top (or for an unknown level).
func Up(level string) string {
	r := Rank(level)
	if r == 0 || r >= len(Ladder) {
		return ""
	}
	return Ladder[r]
}

// Down returns the previous level, or "" at the bottom (or for an unknown level).
func Down(level string) string {
	r := Rank(level)
	if r <= 1 {
		return ""
	}
	return Ladder[r-2]
}

// Thresholds of the methodology. They are constants on purpose: changing one changes MethodologyVersion, and
// every response carries them (Describe), so a suggestion is never a black box.
const (
	MethodologyVersion = 1
	// WindowDays caps how far back the evidence goes (and it never goes before the agent reached its level).
	WindowDays = 30
	// Probation: the agent is on probation until both ProbationDays passed since tracking started and it has
	// ProbationDecisions human decisions. No promotion is suggested on probation; demotions still apply.
	ProbationDays      = 14
	ProbationDecisions = 10
	// Promotion criteria (all must hold).
	PromoteMinDecisions    = 20
	PromoteMinApprovalRate = 0.95
	PromoteMaxTaskFailRate = 0.10
	MinTaskSample          = 5 // the task criterion needs at least this many finished tasks to count
	PromoteMinQuality      = 75
	// DeclineCooldownDays: after a person declines a promotion, it is not suggested again at the same level for
	// this long.
	DeclineCooldownDays = 7
	// Automatic demotion triggers (any one fires).
	AutoHighRiskRejections = 1 // human rejections of high-risk approvals
	BurstHours             = 72
	AutoBurstRejections    = 3 // human rejections within BurstHours
	AutoPolicyDenials      = 3 // policy-engine denials in the window
	// Suggested demotion triggers (any one fires; a human decides).
	SuggestDemoteMinDecisions = 10
	SuggestDemoteApprovalRate = 0.80 // approval rate under this
	SuggestDemoteTaskFailRate = 0.30 // task failure rate over this (with MinTaskSample)
	SuggestDemoteQuality      = 50   // quality score under this
	// RevertDays: an automatic demotion can be reverted by a human for this long.
	RevertDays = 7
)

// Recommendation values.
const (
	RecNone        = "none"
	RecPromote     = "promote"
	RecDemote      = "demote"      // suggested: a human decides
	RecDemoteAuto  = "demote_auto" // applied automatically
	RecProbation   = "probation"   // nothing to suggest until the probation ends
	RecAtCeiling   = "at_ceiling"  // the evidence is good but the role cannot go higher
	RecCooldown    = "cooldown"    // a promotion was declined recently
	RecGatheringEv = "gathering"   // keep working: the criteria are not met yet
)

// Criterion keys (promotion).
const (
	CritDecisions    = "decisions"
	CritApprovalRate = "approval_rate"
	CritHighRisk     = "high_risk_rejections"
	CritPolicy       = "policy_denials"
	CritTaskFailure  = "task_failure_rate"
	CritQuality      = "quality"
)

// Trigger keys (demotion).
const (
	TrigHighRisk     = "high_risk_rejection"
	TrigBurst        = "rejection_burst"
	TrigPolicy       = "policy_incident"
	TrigApprovalRate = "low_approval_rate"
	TrigTaskFailure  = "high_task_failure_rate"
	TrigQuality      = "low_quality"
)

// Counts are the raw observations of one agent since a moment.
type Counts struct {
	Approved         int `json:"approved"`
	Rejected         int `json:"rejected"`           // by a human (timeouts and restarts are not counted)
	HighRiskRejected int `json:"high_risk_rejected"` // of Rejected, approvals with risk "high"
	RecentRejected   int `json:"recent_rejected"`    // of Rejected, inside the burst window
	PolicyDenials    int `json:"policy_denials"`     // tool requests the policy engine denied
	TasksDone        int `json:"tasks_done"`
	TasksFailed      int `json:"tasks_failed"`
	TasksBlocked     int `json:"tasks_blocked"` // shown, not judged: budgets and pauses block tasks too
}

// Decisions is the number of human decisions.
func (c Counts) Decisions() int { return c.Approved + c.Rejected }

// ApprovalRate is approved / decisions (nil without decisions).
func (c Counts) ApprovalRate() *float64 {
	if c.Decisions() == 0 {
		return nil
	}
	r := float64(c.Approved) / float64(c.Decisions())
	return &r
}

// TaskFailRate is failed / (done + failed) (nil without finished tasks).
func (c Counts) TaskFailRate() *float64 {
	n := c.TasksDone + c.TasksFailed
	if n == 0 {
		return nil
	}
	r := float64(c.TasksFailed) / float64(n)
	return &r
}

// Quality is the B11 signal (nil when the score is not available or there is not enough data).
type Quality struct {
	Score int    `json:"score"`
	Trend string `json:"trend"` // up | down | stable | unknown
}

// Input is everything Assess needs.
type Input struct {
	Level         string
	Ceiling       string
	Now           time.Time
	TrackingSince time.Time
	LevelSince    time.Time
	// ProbationCounts are counted since TrackingSince; Counts since EvidenceSince (level start, capped by the window).
	ProbationCounts Counts
	Counts          Counts
	Quality         *Quality
	// LastDeclined is when a promotion from the current level was last declined (nil = never).
	LastDeclined *time.Time
}

// Criterion is one promotion condition with its observed value.
type Criterion struct {
	Key       string   `json:"key"`
	Value     *float64 `json:"value"` // nil when not available
	Threshold float64  `json:"threshold"`
	Op        string   `json:"op"`     // ">=" or "<="
	Status    string   `json:"status"` // met | unmet | not_available (a missing signal never counts against the agent)
}

// Trigger is one demotion condition.
type Trigger struct {
	Key       string   `json:"key"`
	Value     *float64 `json:"value"`
	Threshold float64  `json:"threshold"`
	Op        string   `json:"op"`
	Auto      bool     `json:"auto"` // fires an automatic demotion
	Fired     bool     `json:"fired"`
}

// Probation is the probation status.
type Probation struct {
	Active       bool      `json:"active"`
	Until        time.Time `json:"until"`
	Decisions    int       `json:"decisions"`
	MinDecisions int       `json:"min_decisions"`
	Days         int       `json:"days"`
}

// Assessment is the verdict of Assess.
type Assessment struct {
	Recommendation string      `json:"recommendation"`
	Target         string      `json:"target,omitempty"` // the level a promotion or demotion would set
	NextLevel      string      `json:"next_level,omitempty"`
	AtCeiling      bool        `json:"at_ceiling"`
	Probation      Probation   `json:"probation"`
	Criteria       []Criterion `json:"criteria"`
	Triggers       []Trigger   `json:"triggers"`
	// Progress toward the next level, 0..1: the share of promotion criteria met, with the decisions criterion
	// counted proportionally (12 of 20 decisions = 0.6 of that criterion). 1 only when every criterion is met.
	Progress float64 `json:"progress"`
}

func f(v float64) *float64 { return &v }

func ge(key string, v *float64, th float64) Criterion {
	c := Criterion{Key: key, Value: v, Threshold: th, Op: ">=", Status: "not_available"}
	if v != nil {
		c.Status = map[bool]string{true: "met", false: "unmet"}[*v >= th-1e-9]
	}
	return c
}

func le(key string, v *float64, th float64) Criterion {
	c := Criterion{Key: key, Value: v, Threshold: th, Op: "<=", Status: "not_available"}
	if v != nil {
		c.Status = map[bool]string{true: "met", false: "unmet"}[*v <= th+1e-9]
	}
	return c
}

// EvidenceSince is when the counted evidence starts: the moment the agent reached its level, but never more than
// WindowDays back.
func EvidenceSince(now, levelSince time.Time) time.Time {
	w := now.Add(-WindowDays * 24 * time.Hour)
	if levelSince.After(w) {
		return levelSince
	}
	return w
}

// BurstSince is when the rejection-burst window starts (never before the level started).
func BurstSince(now, levelSince time.Time) time.Time {
	b := now.Add(-BurstHours * time.Hour)
	if levelSince.After(b) {
		return levelSince
	}
	return b
}

// Assess is the methodology as a pure function: same input, same verdict.
func Assess(in Input) Assessment {
	c := in.Counts
	a := Assessment{Recommendation: RecNone}

	// Probation.
	until := in.TrackingSince.Add(ProbationDays * 24 * time.Hour)
	pd := in.ProbationCounts.Decisions()
	a.Probation = Probation{Until: until, Decisions: pd, MinDecisions: ProbationDecisions, Days: ProbationDays,
		Active: in.Now.Before(until) || pd < ProbationDecisions}

	// Ceiling: an unknown level or ceiling never allows going up.
	next := Up(in.Level)
	if next != "" && Rank(next) <= Rank(in.Ceiling) && Rank(in.Ceiling) > 0 {
		a.NextLevel = next
	}
	a.AtCeiling = a.NextLevel == ""

	// Promotion criteria.
	var hr, pol = float64(c.HighRiskRejected), float64(c.PolicyDenials)
	tfr := c.TaskFailRate()
	if c.TasksDone+c.TasksFailed < MinTaskSample {
		tfr = nil
	}
	var q *float64
	quality := ge(CritQuality, nil, PromoteMinQuality)
	if in.Quality != nil {
		q = f(float64(in.Quality.Score))
		quality = ge(CritQuality, q, PromoteMinQuality)
		if in.Quality.Trend == "down" {
			quality.Status = "unmet" // quality must not be falling
		}
	}
	a.Criteria = []Criterion{
		ge(CritDecisions, f(float64(c.Decisions())), PromoteMinDecisions),
		ge(CritApprovalRate, c.ApprovalRate(), PromoteMinApprovalRate),
		le(CritHighRisk, &hr, 0),
		le(CritPolicy, &pol, 0),
		le(CritTaskFailure, tfr, PromoteMaxTaskFailRate),
		quality,
	}
	// Decisions and approval rate must be MET (there is no promotion without evidence); the other criteria only
	// must not be unmet (a signal that does not exist, like a quality score with too little data, is not held
	// against the agent).
	allMet := true
	var score float64
	for _, cr := range a.Criteria {
		switch cr.Key {
		case CritDecisions:
			score += math.Min(1, float64(c.Decisions())/PromoteMinDecisions)
			allMet = allMet && cr.Status == "met"
		case CritApprovalRate:
			if cr.Status == "met" {
				score++
			}
			allMet = allMet && cr.Status == "met"
		default:
			if cr.Status == "unmet" {
				allMet = false
			} else {
				score++
			}
		}
	}
	a.Progress = math.Round(1000*score/float64(len(a.Criteria))) / 1000
	if !allMet && a.Progress >= 1 {
		a.Progress = 0.999
	}

	// Demotion triggers.
	ar := c.ApprovalRate()
	a.Triggers = []Trigger{
		{Key: TrigHighRisk, Value: &hr, Threshold: AutoHighRiskRejections, Op: ">=", Auto: true, Fired: c.HighRiskRejected >= AutoHighRiskRejections},
		{Key: TrigBurst, Value: f(float64(c.RecentRejected)), Threshold: AutoBurstRejections, Op: ">=", Auto: true, Fired: c.RecentRejected >= AutoBurstRejections},
		{Key: TrigPolicy, Value: &pol, Threshold: AutoPolicyDenials, Op: ">=", Auto: true, Fired: c.PolicyDenials >= AutoPolicyDenials},
		{Key: TrigApprovalRate, Value: ar, Threshold: SuggestDemoteApprovalRate, Op: "<",
			Fired: ar != nil && c.Decisions() >= SuggestDemoteMinDecisions && *ar < SuggestDemoteApprovalRate},
		{Key: TrigTaskFailure, Value: tfr, Threshold: SuggestDemoteTaskFailRate, Op: ">", Fired: tfr != nil && *tfr > SuggestDemoteTaskFailRate},
		{Key: TrigQuality, Value: q, Threshold: SuggestDemoteQuality, Op: "<", Fired: q != nil && *q < SuggestDemoteQuality},
	}
	down := Down(in.Level)
	auto, soft := false, false
	for _, t := range a.Triggers {
		auto = auto || (t.Fired && t.Auto)
		soft = soft || (t.Fired && !t.Auto)
	}
	switch {
	case down != "" && Rank(in.Level) > 0 && auto:
		a.Recommendation, a.Target = RecDemoteAuto, down
	case down != "" && Rank(in.Level) > 0 && soft:
		a.Recommendation, a.Target = RecDemote, down
	case auto || soft:
		a.Recommendation = RecNone // already at the bottom: nothing lower to go to
	case a.Probation.Active:
		a.Recommendation = RecProbation
	case !allMet:
		a.Recommendation = RecGatheringEv
		if a.AtCeiling {
			a.Recommendation = RecAtCeiling
		}
	case a.AtCeiling:
		a.Recommendation = RecAtCeiling
	case in.LastDeclined != nil && in.Now.Sub(*in.LastDeclined) < DeclineCooldownDays*24*time.Hour:
		a.Recommendation = RecCooldown
	default:
		a.Recommendation, a.Target = RecPromote, a.NextLevel
	}
	return a
}

// MethodItem is one threshold in the methodology block.
type MethodItem struct {
	Key       string  `json:"key"`
	Op        string  `json:"op"`
	Threshold float64 `json:"threshold"`
	Auto      bool    `json:"auto,omitempty"`
}

// Methodology is returned with every response: the thresholds behind every suggestion.
type Methodology struct {
	Version            int          `json:"version"`
	WindowDays         int          `json:"window_days"`
	ProbationDays      int          `json:"probation_days"`
	ProbationDecisions int          `json:"probation_decisions"`
	MinTaskSample      int          `json:"min_task_sample"`
	BurstHours         int          `json:"burst_hours"`
	DeclineCooldown    int          `json:"decline_cooldown_days"`
	RevertDays         int          `json:"revert_days"`
	Promotion          []MethodItem `json:"promotion"`
	Demotion           []MethodItem `json:"demotion"`
	// PromotionNeedsHuman and AutoDemotion state the two rules of the ladder explicitly.
	PromotionNeedsHuman bool `json:"promotion_needs_human"` // always true
	AutoDemotion        bool `json:"auto_demotion"`         // hard incidents demote one level at once
}

// Describe builds the methodology block.
func Describe() Methodology {
	return Methodology{Version: MethodologyVersion, WindowDays: WindowDays, ProbationDays: ProbationDays, ProbationDecisions: ProbationDecisions,
		MinTaskSample: MinTaskSample, BurstHours: BurstHours, DeclineCooldown: DeclineCooldownDays, RevertDays: RevertDays,
		PromotionNeedsHuman: true, AutoDemotion: true,
		Promotion: []MethodItem{
			{Key: CritDecisions, Op: ">=", Threshold: PromoteMinDecisions},
			{Key: CritApprovalRate, Op: ">=", Threshold: PromoteMinApprovalRate},
			{Key: CritHighRisk, Op: "<=", Threshold: 0},
			{Key: CritPolicy, Op: "<=", Threshold: 0},
			{Key: CritTaskFailure, Op: "<=", Threshold: PromoteMaxTaskFailRate},
			{Key: CritQuality, Op: ">=", Threshold: PromoteMinQuality},
		},
		Demotion: []MethodItem{
			{Key: TrigHighRisk, Op: ">=", Threshold: AutoHighRiskRejections, Auto: true},
			{Key: TrigBurst, Op: ">=", Threshold: AutoBurstRejections, Auto: true},
			{Key: TrigPolicy, Op: ">=", Threshold: AutoPolicyDenials, Auto: true},
			{Key: TrigApprovalRate, Op: "<", Threshold: SuggestDemoteApprovalRate},
			{Key: TrigTaskFailure, Op: ">", Threshold: SuggestDemoteTaskFailRate},
			{Key: TrigQuality, Op: "<", Threshold: SuggestDemoteQuality},
		},
	}
}
