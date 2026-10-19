package application

import (
	"strings"
	"testing"

	"aiworkforce/backend/internal/domain"
)

func TestCallEstimateGrowsWithThePayloadAndUsesTheOutputCap(t *testing.T) {
	small := callEstimateUSD("deepseek-chat", domain.UsageRunTask, map[string]string{"task": "x"})
	big := callEstimateUSD("deepseek-chat", domain.UsageRunTask, map[string]string{"task": strings.Repeat("palabra ", 20000)})
	if !(big > small && small > 0) {
		t.Fatalf("small=%v big=%v", small, big)
	}
	// One 4000-token answer at the deepseek-chat output rate is the floor of any run_task estimate.
	_, out := priceFor("deepseek-chat")
	if floor := out * 4000 / 1e6; small < floor {
		t.Fatalf("estimate %v below the output cap cost %v", small, floor)
	}
	if callEstimateUSD("", domain.UsageConsult, "x") >= callEstimateUSD("", domain.UsageSynthesize, "x") {
		t.Fatal("a consult is capped lower than a synthesis")
	}
}

func TestOutputCapsFollowTheSharedEnvNames(t *testing.T) {
	if callMaxTokens(domain.UsageRunTask) != 4000 {
		t.Fatal("default run_task cap must match agent-runtime/app/cost_control.py")
	}
	t.Setenv("MAX_TOKENS_RUN_TASK", "1500")
	if callMaxTokens(domain.UsageRunTask) != 1500 {
		t.Fatal("MAX_TOKENS_RUN_TASK must override the cap")
	}
	t.Setenv("MAX_TOKENS_RUN_TASK", "junk")
	if callMaxTokens(domain.UsageRunTask) != 4000 {
		t.Fatal("junk falls back to the default")
	}
}

func TestAllowanceIsTheReservationPlusSlack(t *testing.T) {
	var nilRes *Reservation
	if nilRes.Allowance() != 0 || nilRes.Amount() != 0 {
		t.Fatal("nil reservation has no allowance")
	}
	r := &Reservation{usd: 0.04}
	if a := r.Allowance(); a < 0.04*1.24 || a > 0.04*1.26 {
		t.Fatalf("allowance %v", a)
	}
}

func TestCachedTokensBillAtTheDiscountedPrice(t *testing.T) {
	full := recalcCostCached("deepseek-chat", 100000, 0, 1000)
	cached := recalcCostCached("deepseek-chat", 100000, 80000, 1000)
	if !(cached < full) {
		t.Fatalf("cached=%v full=%v", cached, full)
	}
	if recalcCost("deepseek-chat", 100000, 1000) != full {
		t.Fatal("recalcCost must equal the no-cache case")
	}
	if recalcCostCached("deepseek-chat", 100, 500, 0) != recalcCostCached("deepseek-chat", 100, 100, 0) {
		t.Fatal("cached tokens are clamped to the prompt size")
	}
}

func TestPlannerCallsAreSizedByKindAndPayload(t *testing.T) {
	small := callEstimateUSD("deepseek-chat", domain.UsagePlan, map[string]string{"g": "x"})
	big := callEstimateUSD("deepseek-chat", domain.UsagePlan, map[string]string{"g": strings.Repeat("meta ", 20000)})
	if !(big > small && small > 0) {
		t.Fatalf("small=%v big=%v", small, big)
	}
}
