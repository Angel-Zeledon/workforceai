package application

import (
	_ "embed"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
)

// modelPricesJSON is the single price table shared with the runtime
// (agent-runtime/app/model_prices.json must be byte-identical; a test checks
// it). USD per million tokens. The backend recalculates every call with this
// table and bills the greater of both numbers, so it never depends on the
// runtime's honesty (docs/architecture/07-seguridad-costos.md 5.2).
//
//go:embed model_prices.json
var modelPricesJSON []byte

type modelPrice struct {
	Provider string  `json:"provider"`
	Input    float64 `json:"input"`
	Output   float64 `json:"output"`
}

type priceFile struct {
	Default modelPrice            `json:"default"`
	Models  map[string]modelPrice `json:"models"`
}

// priceTable maps a model name (without "provider/") to (input, output);
// defaultPrice is the conservative rate for unknown models and simulation.
var priceTable, defaultPrice = mustLoadPrices(modelPricesJSON)

func mustLoadPrices(raw []byte) (map[string][2]float64, [2]float64) {
	var f priceFile
	if err := json.Unmarshal(raw, &f); err != nil {
		panic("model_prices.json: " + err.Error())
	}
	if f.Default.Input <= 0 || f.Default.Output <= 0 {
		panic("model_prices.json: the default rate must be positive")
	}
	out := make(map[string][2]float64, len(f.Models))
	for k, v := range f.Models {
		out[strings.ToLower(k)] = [2]float64{v.Input, v.Output}
	}
	return out, [2]float64{f.Default.Input, f.Default.Output}
}

// providerOfModel returns the provider of a priced model ("" if unknown).
func providerOfModel(model string) string {
	var f priceFile
	_ = json.Unmarshal(modelPricesJSON, &f)
	return f.Models[strings.ToLower(model)].Provider
}

// warnedModels remembers the unknown models already logged (one warning each).
var warnedModels sync.Map

// priceFor returns (input, output) USD per million tokens. PRICE_IN_PER_M /
// PRICE_OUT_PER_M force a rate, like in the runtime. An unknown model gets the
// default rate and a one-time warning (simulation and empty names are silent).
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
	if key != "" && !strings.Contains(key, "simulat") {
		if _, seen := warnedModels.LoadOrStore(key, true); !seen {
			slog.Warn("model not in the price table: billed at the default rate",
				"model", model, "input_per_m", defaultPrice[0], "output_per_m", defaultPrice[1])
		}
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
