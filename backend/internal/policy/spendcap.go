package policy

import "math"

// Free plan spend cap (docs/architecture/approvals-notify.md).
//
// The free plan has a HARD cap: when spent + reserved + the estimate of the
// next call would pass the cap, the call does not start (the work pauses and
// the organization is told). There is no soft mode, no "grace" and no silent
// overage: every decision here is a pure function of numbers, so it is easy to
// prove. The numbers come from the existing budget ledger (application.Budget).

// Plans.
const (
	PlanFree = "free"
	PlanPaid = "paid"
)

// Cap states shown by the UI.
const (
	CapOK        = "ok"        // under the warning line
	CapWarn      = "warn"      // 80% or more of the cap is spent or held
	CapExhausted = "exhausted" // nothing more can start: the work is paused
)

// CapWarnRatio is the share of the cap where the UI starts to warn.
const CapWarnRatio = 0.8

// capEpsilon absorbs float noise so a call that fits exactly is not refused.
const capEpsilon = 1e-9

// CapInput is one organization's numbers in USD.
type CapInput struct {
	CapUSD      float64
	SpentUSD    float64 // already billed (persisted ledger)
	ReservedUSD float64 // held by calls in flight
	EstimateUSD float64 // what the call about to start is estimated to cost
}

// CapAllows reports whether a call may start under a HARD cap. A cap <= 0 on a
// hard-capped plan allows nothing (a misconfigured free plan fails closed).
func CapAllows(in CapInput) bool {
	if math.IsNaN(in.CapUSD) || math.IsNaN(in.SpentUSD) || math.IsNaN(in.ReservedUSD) || math.IsNaN(in.EstimateUSD) {
		return false
	}
	if in.CapUSD <= 0 {
		return false
	}
	return in.SpentUSD+in.ReservedUSD+math.Max(in.EstimateUSD, 0) <= in.CapUSD+capEpsilon
}

// EffectiveCap combines the operator-wide organization cap with the cap of the
// plan: the strictest positive one wins; 0 means "no cap" only when both are 0.
func EffectiveCap(configCap, planCap float64) float64 {
	switch {
	case configCap > 0 && planCap > 0:
		return math.Min(configCap, planCap)
	case planCap > 0:
		return planCap
	default:
		return math.Max(configCap, 0)
	}
}

// CapStatus is what the UI shows: spent / estimated / cap.
type CapStatus struct {
	Plan         string  `json:"plan"`
	Hard         bool    `json:"hard"`
	CapUSD       float64 `json:"cap_usd"`
	SpentUSD     float64 `json:"spent_usd"`
	ReservedUSD  float64 `json:"reserved_usd"`
	EstimatedUSD float64 `json:"estimated_usd"` // spent + reserved: where the bill is heading
	RemainingUSD float64 `json:"remaining_usd"`
	PercentUsed  float64 `json:"percent_used"` // of the cap, 0..100 (spent + reserved)
	State        string  `json:"state"`
	Paused       bool    `json:"paused"`
}

// EvaluateCap computes the status of a plan. hard=false (paid plan without a
// cap) reports the numbers and never pauses.
func EvaluateCap(plan string, hard bool, capUSD, spent, reserved float64) CapStatus {
	spent, reserved = math.Max(spent, 0), math.Max(reserved, 0)
	st := CapStatus{Plan: plan, Hard: hard, CapUSD: math.Max(capUSD, 0), SpentUSD: spent, ReservedUSD: reserved,
		EstimatedUSD: spent + reserved, State: CapOK}
	if st.CapUSD <= 0 {
		if hard { // a hard-capped plan without a usable cap is exhausted, never "unlimited"
			st.State, st.Paused = CapExhausted, true
			st.PercentUsed = 100
		}
		return st
	}
	st.RemainingUSD = math.Max(st.CapUSD-st.EstimatedUSD, 0)
	st.PercentUsed = math.Min(st.EstimatedUSD/st.CapUSD*100, 100)
	switch {
	case st.EstimatedUSD >= st.CapUSD-capEpsilon:
		st.State = CapExhausted
		st.Paused = hard
	case st.EstimatedUSD >= st.CapUSD*CapWarnRatio:
		st.State = CapWarn
	}
	return st
}
