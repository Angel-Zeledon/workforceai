package projects

import (
	"context"
	"math"
	"sort"

	"aiworkforce/backend/internal/application"
)

// Q3: cost estimates by the model each agent really uses.
//
// The per-node priors of calc.go are token counts priced at deepseek-chat
// rates. When the organization routes a role to another model (model policy,
// RoleModels) the node is re-priced with the shared price table
// (application.ListedPrice, the same model_prices.json the runtime uses); a
// model that is not in the table keeps the deepseek-chat prior. The expected
// hierarchical synthesis (W3) is added to the total; planner calls are
// reported separately because they are spent before the project exists.

const (
	defaultSynthBudgetTokens = 12000 // mirrors application defaultSynthTokenBudget
	defaultSynthGroups       = 6     // mirrors application defaultSynthMaxGroups
	synthOverheadIn          = 400   // system prompt and request text of a synthesis call
	synthSingleOut           = 1500
	synthGroupOut            = 1200
	synthDigestInPerGroup    = 800 // groupDigest: summary plus up to 8 bounded sections
)

// estimateOpts carries what the pure estimate needs from the live system. The
// zero value reproduces the historical estimate (deepseek-chat priors) plus
// the default-threshold synthesis.
type estimateOpts struct {
	AgentModel     map[string]string // agent id -> "provider/model"
	SynthModel     string            // model of the synthesis (assistant role)
	SynthTokens    int               // SYNTH_TOKEN_BUDGET
	SynthMaxGroups int               // SYNTH_MAX_GROUPS
	PlannerUSD     float64           // already spent by the planner (informational)
	PlannerCalls   int
}

type synthEst struct {
	USD   float64
	Calls int
	Model string
}

// modelOf returns the model a node is priced at and whether it is in the table.
func (o estimateOpts) modelOf(n NodeDef) (string, bool) {
	if n.AgentID == nil || n.human() {
		return estimateModel, false
	}
	m := o.AgentModel[*n.AgentID]
	if _, _, ok := application.ListedPrice(m); ok {
		return m, true
	}
	return estimateModel, false
}

// nodeCost prices one node at its agent's model, or keeps its stored prior.
func (o estimateOpts) nodeCost(n NodeDef) float64 {
	m, listed := o.modelOf(n)
	if !listed || n.EstCostUSD <= 0 {
		return n.EstCostUSD
	}
	pin, pout, _ := application.ListedPrice(m)
	p := priorOf(n.Complexity)
	ctx := math.Min(8000, 800*float64(len(n.DependsOn)))
	c := (p.in+ctx)*pin/1e6 + p.out*pout/1e6
	if n.Kind == KindSubtask {
		c *= 0.5
	}
	return c
}

func (o estimateOpts) synthesis(ls []NodeDef) synthEst {
	budget, maxGroups := o.SynthTokens, o.SynthMaxGroups
	if budget <= 0 {
		budget = defaultSynthBudgetTokens
	}
	if maxGroups <= 0 {
		maxGroups = defaultSynthGroups
	}
	var outTokens float64
	tasks := 0
	for _, n := range ls {
		if n.human() || n.AgentID == nil {
			continue
		}
		outTokens += priorOf(n.Complexity).out
		tasks++
	}
	if tasks == 0 {
		return synthEst{}
	}
	pin, pout, listed := application.ListedPrice(o.SynthModel)
	model := o.SynthModel
	if !listed {
		pin, pout, model = priceIn*1e6, priceOut*1e6, estimateModel
	}
	price := func(in, out float64) float64 { return in*pin/1e6 + out*pout/1e6 }
	if outTokens <= float64(budget) {
		return synthEst{USD: price(outTokens+synthOverheadIn, synthSingleOut), Calls: 1, Model: model}
	}
	groups := int(math.Min(float64(maxGroups), math.Ceil(outTokens/float64(budget))))
	// every group call is fitted to the budget, so its input never exceeds it
	perGroup := math.Min(outTokens/float64(groups), float64(budget)) + synthOverheadIn
	usd := float64(groups) * price(perGroup, synthGroupOut)
	usd += price(math.Min(float64(budget), float64(groups)*synthDigestInPerGroup)+synthOverheadIn, synthSingleOut)
	return synthEst{USD: usd, Calls: groups + 1, Model: model}
}

type modelTally struct {
	m     map[string]*EstModel
	order []string
}

func newModelTally() *modelTally { return &modelTally{m: map[string]*EstModel{}} }

func (t *modelTally) add(n NodeDef, model string, listed bool, usd float64) {
	if n.human() || n.AgentID == nil {
		return
	}
	key := model
	if !listed {
		key = model + "|prior"
	}
	e, ok := t.m[key]
	if !ok {
		e = &EstModel{Model: model, Priced: listed}
		t.m[key] = e
		t.order = append(t.order, key)
	}
	e.P50USD += usd
	e.Calls++
}

func (t *modelTally) list() []EstModel {
	out := make([]EstModel, 0, len(t.order))
	for _, k := range t.order {
		out = append(out, *t.m[k])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].P50USD > out[j].P50USD })
	return out
}

// estimator gathers the live inputs of an estimate: the model of every agent
// (organization role models), the synthesis thresholds and the planner spend.
func (s *Service) estimator(ctx context.Context, planner *PlannerInfo) estimateOpts {
	o := estimateOpts{SynthTokens: s.cfg.SynthTokenBudget, SynthMaxGroups: s.cfg.SynthMaxGroups}
	if planner != nil {
		o.PlannerUSD, o.PlannerCalls = planner.CostUSD, planner.Calls
	}
	if s.cfg.ModelFor == nil || s.cfg.Core == nil {
		return o
	}
	ags, err := s.cfg.Core.ListAgents(ctx, s.org(ctx))
	if err != nil {
		return o
	}
	o.AgentModel = make(map[string]string, len(ags))
	for _, a := range ags {
		m := s.cfg.ModelFor(ctx, a.Role)
		o.AgentModel[a.ID] = m
		if a.Role == "assistant" || a.ID == "assistant" {
			o.SynthModel = m
		}
	}
	return o
}
