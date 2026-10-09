package projects

import (
	"math"
	"testing"
)

func estNodes(n int, agent string) []NodeDef {
	var out []NodeDef
	for i := 0; i < n; i++ {
		a := agent
		out = append(out, NodeDef{ID: pad(i), Kind: KindTask, AgentID: &a, Complexity: "M", EstCostUSD: priorCost("M", 0), ObjectiveID: "o"})
	}
	return out
}

func TestEstimatePricesByAgentModel(t *testing.T) {
	nodes := estNodes(10, "accounting")
	base := estimatePlan(nodes, nil)
	// A claude-fable-5 route costs much more than the deepseek-chat prior.
	pricey := estimatePlanWith(nodes, nil, estimateOpts{AgentModel: map[string]string{"accounting": "anthropic/claude-fable-5"}})
	if pricey.Breakdown.TasksUSD < base.Breakdown.TasksUSD*5 {
		t.Fatalf("expected a pricier estimate: %v vs %v", pricey.Breakdown.TasksUSD, base.Breakdown.TasksUSD)
	}
	if pricey.Model != "anthropic/claude-fable-5" || len(pricey.ByModel) != 1 || !pricey.ByModel[0].Priced {
		t.Fatalf("model reporting: %+v %+v", pricey.Model, pricey.ByModel)
	}
	// Unknown model: falls back to the prior unchanged.
	unk := estimatePlanWith(nodes, nil, estimateOpts{AgentModel: map[string]string{"accounting": "custom/made-up"}})
	if math.Abs(unk.Breakdown.TasksUSD-base.Breakdown.TasksUSD) > 1e-12 || unk.ByModel[0].Priced || unk.Model != estimateModel {
		t.Fatalf("unknown model must keep the prior: %+v", unk)
	}
	// The same rates as the prior (deepseek-chat) give the same total.
	same := estimatePlanWith(nodes, nil, estimateOpts{AgentModel: map[string]string{"accounting": "deepseek/deepseek-chat"}})
	if math.Abs(same.Breakdown.TasksUSD-base.Breakdown.TasksUSD) > 1e-9 {
		t.Fatalf("deepseek-chat must equal the prior: %v vs %v", same.Breakdown.TasksUSD, base.Breakdown.TasksUSD)
	}
}

func TestEstimateIsAdditiveAndCountsSynthesis(t *testing.T) {
	small := estimatePlan(estNodes(5, "a"), nil)
	if small.Breakdown.SynthesisCalls != 1 {
		t.Fatalf("below the budget: one synthesis, got %d", small.Breakdown.SynthesisCalls)
	}
	if math.Abs(small.Total.P50USD-(small.Breakdown.TasksUSD+small.Breakdown.SynthesisUSD)) > 1e-12 {
		t.Fatal("total must be tasks + synthesis")
	}
	big := estimatePlanWith(estNodes(100, "a"), nil, estimateOpts{PlannerUSD: 0.01, PlannerCalls: 7})
	// 100 tasks * 900 out tokens = 90000 > 12000 -> groups capped at 6, plus the final pass
	if big.Breakdown.SynthesisCalls != defaultSynthGroups+1 {
		t.Fatalf("hierarchical synthesis calls = %d", big.Breakdown.SynthesisCalls)
	}
	if big.Breakdown.PlannerCalls != 7 || big.Total.P50USD != big.Breakdown.TasksUSD+big.Breakdown.SynthesisUSD {
		t.Fatalf("planner is reported, not added: %+v", big.Breakdown)
	}
	if big.Total.Calls < 100+7 {
		t.Fatalf("calls must include synthesis and planner: %d", big.Total.Calls)
	}
	custom := estimatePlanWith(estNodes(100, "a"), nil, estimateOpts{SynthTokens: 20000, SynthMaxGroups: 3})
	if custom.Breakdown.SynthesisCalls != 3+1 {
		t.Fatalf("custom thresholds: %d", custom.Breakdown.SynthesisCalls)
	}
}
