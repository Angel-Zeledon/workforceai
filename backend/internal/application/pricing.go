package application

import (
	"math"
	"os"
	"strconv"
	"strings"
)

// priceTable mirrors MODEL_PRICES in agent-runtime/app/engine.py (USD per
// million tokens: input, output). The backend recalculates every call with its
// own table and bills the greater of both numbers, so it never depends on the
// runtime's honesty (docs/architecture/07-seguridad-costos.md 5.2). Keep in sync.
var priceTable = map[string][2]float64{
	"deepseek-chat":     {0.30, 1.20},
	"deepseek-reasoner": {0.30, 1.20},
	"deepseek-flash":    {0.30, 1.20},
	"deepseek-v4-flash": {0.30, 1.20},
	"deepseek-v4-pro":   {1.32, 3.96},
}

// defaultPrice is the rate for unknown models and simulation (Claude Sonnet).
var defaultPrice = [2]float64{3.0, 15.0}

// priceFor returns (input, output) USD per million tokens. PRICE_IN_PER_M /
// PRICE_OUT_PER_M force a rate, like in the runtime.
func priceFor(model string) (float64, float64) {
	in, errIn := strconv.ParseFloat(os.Getenv("PRICE_IN_PER_M"), 64)
	out, errOut := strconv.ParseFloat(os.Getenv("PRICE_OUT_PER_M"), 64)
	if errIn == nil && errOut == nil {
		return in, out
	}
	key := strings.ToLower(model)
	if i := strings.LastIndex(key, "/"); i >= 0 {
		key = key[i+1:]
	}
	if p, ok := priceTable[key]; ok {
		return p[0], p[1]
	}
	return defaultPrice[0], defaultPrice[1]
}

// recalcCost prices a call from its token counts, rounded like the runtime (5 decimals).
func recalcCost(model string, inTokens, outTokens int) float64 {
	pin, pout := priceFor(model)
	c := float64(inTokens)*pin/1e6 + float64(outTokens)*pout/1e6
	return math.Round(c*1e5) / 1e5
}

// Coarse constants used only when the runtime has no /v1/estimate (basis "fallback").
const (
	fallbackInMin, fallbackInMax        = 1500, 6000
	fallbackInPerDep                    = 1500
	fallbackOutMin, fallbackOutMax      = 300, 4096
	consultReserveIn, consultReserveOut = 2000, 800
)

func fallbackTaskRange(model string, deps int) (lo, hi float64) {
	return recalcCost(model, fallbackInMin, fallbackOutMin), recalcCost(model, fallbackInMax+fallbackInPerDep*deps, fallbackOutMax)
}
