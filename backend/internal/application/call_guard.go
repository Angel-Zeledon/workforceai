package application

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strconv"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// Call guard: the budget check BEFORE a live runtime call is sized with the real
// payload instead of a per-task range, so a cap is not crossed by the call that
// follows (live runs overshot request caps by 40-100%: the estimate was a few
// thousand tokens while the calls carried tens of thousands). See
// docs/cost-reduction.md.
//
//	estimate = (payload tokens + fixed prompt overhead) * input price + max output tokens * output price
//
// Prices come from the shared model_prices.json (priceFor). The output caps are
// the same MAX_TOKENS_<KIND> variables the runtime reads (cost_control.py), with
// the same defaults, so both sides agree on the worst case of one call.

const (
	guardCharsPerToken = 3.5
	// guardPromptOverheadTokens is what the runtime adds around the payload: system rules, tool manuals, JSON
	// schema and the framework wrapper (measured in agent-runtime/tests/test_cost_control.py: ~2.5k tokens).
	guardPromptOverheadTokens = 2600
	// guardAllowanceSlack widens the allowance handed to the runtime so that the (more exact) runtime-side
	// estimate only refuses a call when it is clearly above what was reserved.
	guardAllowanceSlack = 1.25
)

var defaultCallMaxTokens = map[domain.UsageKind]int{
	domain.UsageRunTask:    4000,
	domain.UsageConsult:    800,
	domain.UsageSynthesize: 6000,
	domain.UsageReview:     1500,
	domain.UsagePlan:       3000,
	UsagePlanPhases:        2048,
	UsagePlanPhase:         4096,
	UsageReplan:            3000,
	UsageCounterargument:   1200,
	domain.UsageChat:       350,
	UsageSummarize:         2000,
}

// Call kinds that have an output cap (same names and defaults as agent-runtime/app/cost_control.py) but are
// recorded in the ledger as "plan" (planner calls) or "chat".
const (
	UsagePlanPhases domain.UsageKind = "plan_phases"
	UsagePlanPhase  domain.UsageKind = "plan_phase"
	UsageReplan     domain.UsageKind = "replan"
	// UsageCounterargument is the devil's advocate review of a high-risk approval (B8); priced like a planner call.
	UsageCounterargument domain.UsageKind = "counterargument"
	UsageSummarize       domain.UsageKind = "summarize"
)

// callMaxTokens is the output cap of one call kind (MAX_TOKENS_<KIND>, like the runtime).
func callMaxTokens(kind domain.UsageKind) int {
	def, ok := defaultCallMaxTokens[kind]
	if !ok {
		def = 3000
	}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("MAX_TOKENS_" + strings.ToUpper(string(kind))))); err == nil && v > 0 {
		return v
	}
	return def
}

// payloadTokens estimates the input tokens of a request body (JSON bytes / 3.5, rounded up).
func payloadTokens(payload any) int {
	b, err := json.Marshal(payload)
	if err != nil {
		return 0
	}
	return int(math.Ceil(float64(len(b)) / guardCharsPerToken))
}

// callEstimateUSD is the worst case of ONE call of this kind with this payload.
func callEstimateUSD(model string, kind domain.UsageKind, payload any) float64 {
	return recalcCost(model, payloadTokens(payload)+promptOverhead(kind), callMaxTokens(kind))
}

// promptOverhead is the fixed prompt the runtime adds around the payload for this kind of call. Chat-sized
// calls (chat, summarize) carry no tool manuals; the planner calls carry the agent list and the planning rules.
func promptOverhead(kind domain.UsageKind) int {
	switch kind {
	case domain.UsageChat, UsageSummarize:
		return 700
	case domain.UsagePlan, UsagePlanPhases, UsagePlanPhase, UsageReplan, UsageCounterargument:
		return 1200
	}
	return guardPromptOverheadTokens
}

// liveEstimate reports whether the request was estimated from tokens (a live runtime). Simulated and fallback
// estimates keep their per-task ranges: there is no real prompt to size.
func (o *Orchestrator) liveEstimate(ctx context.Context, requestID string) (model string, ok bool) {
	e, found := o.budget.Estimate(ctx, requestID)
	if !found || e.Basis != domain.BasisTokenHeuristic {
		return "", false
	}
	return e.Model, true
}

// Allowance is the most the runtime should let the reserved call cost (reservation + slack); 0 on nil.
func (r *Reservation) Allowance() float64 {
	if r == nil || r.usd <= 0 {
		return 0
	}
	return math.Round(r.usd*guardAllowanceSlack*1e5) / 1e5
}

// Amount is the USD held by the reservation (0 on nil).
func (r *Reservation) Amount() float64 {
	if r == nil {
		return 0
	}
	return r.usd
}

// sizeReservation re-checks the budget with the real payload of a live call. When the call is larger than what
// reserveOrPause held, the reservation is released and taken again at the payload-based size (pausing, with the
// usual budget.exceeded explanation, if the cap does not allow it). Simulation keeps the original reservation.
// It returns the reservation to use and the allowance to send to the runtime (max_cost_usd).
func (o *Orchestrator) sizeReservation(ctx context.Context, rs *run, agentID, taskID string, kind domain.UsageKind,
	res *Reservation, payload any) (*Reservation, float64, error) {
	model, live := o.liveEstimate(ctx, rs.req.ID)
	if !live {
		return res, 0, nil
	}
	need := callEstimateUSD(model, kind, payload)
	if need > res.Amount()+1e-9 {
		res.Release()
		next, err := o.reserveOrPauseSized(ctx, rs, agentID, taskID, kind, need)
		if err != nil {
			return nil, 0, err
		}
		res = next
	}
	return res, res.Allowance(), nil
}

// providerNoBalanceMsg is what a person reads when the model provider account has no money left.
const providerNoBalanceMsg = "tu proveedor de IA se quedó sin saldo: recarga la cuenta o elige otro modelo en Ajustes → Modelo de IA (o escribe /modelo); las tareas pendientes quedan en espera"

// NoteProviderNoBalance tells the UI (event provider.no_balance: the "out of balance" banner) when err is the
// runtime typed provider_no_balance answer, for calls that belong to no task (chat, project planner), where
// the failure would otherwise look like a generic error. It reports whether err was that error.
func (o *Orchestrator) NoteProviderNoBalance(ctx context.Context, agentID string, err error) bool {
	if !errors.Is(err, ErrProviderNoBalance) {
		return false
	}
	o.rec.Emit(ctx, Action{Type: EvProviderNoBalance, AgentID: agentID, Entity: "org", EntityID: o.org(ctx),
		Payload: map[string]any{"reason": "provider_no_balance"}, Text: providerNoBalanceMsg})
	return true
}
