package application

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The runtime prices calls with its own copy of the table; both must be identical.
func TestPriceTableMatchesRuntimeCopy(t *testing.T) {
	path := filepath.Join("..", "..", "..", "agent-runtime", "app", "model_prices.json")
	other, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("runtime copy not available (%v): only the backend tree is mounted", err)
	}
	if !bytes.Equal(other, modelPricesJSON) {
		t.Fatalf("%s differs from backend/internal/application/model_prices.json: keep both copies identical", path)
	}
}

func TestPriceForKnownAndUnknownModels(t *testing.T) {
	t.Setenv("PRICE_IN_PER_M", "")
	t.Setenv("PRICE_OUT_PER_M", "")
	cases := []struct {
		model   string
		in, out float64
	}{
		{"deepseek/deepseek-chat", 0.30, 1.20},
		{"deepseek-v4-pro", 1.32, 3.96},
		{"anthropic/claude-opus-5-5", 4, 20},
		{"claude-sonnet-5-5", 2, 10},
		{"anthropic/claude-haiku-4-5-20251001", 1, 5},
		{"CLAUDE-SONNET-5-5", 2, 10},
		{"some-unknown-model", defaultPrice[0], defaultPrice[1]},
		{"simulation-claude-sonnet", defaultPrice[0], defaultPrice[1]},
	}
	for _, c := range cases {
		in, out := priceFor(c.model)
		if in != c.in || out != c.out {
			t.Errorf("priceFor(%q) = %v/%v, want %v/%v", c.model, in, out, c.in, c.out)
		}
	}
	if got := recalcCost("claude-opus-5-5", 1_000_000, 100_000); got != 6 {
		t.Errorf("recalcCost opus 5.5 = %v, want 6", got)
	}
	if providerOfModel("claude-opus-5-5") != "anthropic" || providerOfModel("deepseek-chat") != "deepseek" || providerOfModel("x") != "" {
		t.Error("providerOfModel")
	}
}

func TestPriceForEnvOverride(t *testing.T) {
	t.Setenv("PRICE_IN_PER_M", "7")
	t.Setenv("PRICE_OUT_PER_M", "9")
	if in, out := priceFor("claude-opus-5-5"); in != 7 || out != 9 {
		t.Fatalf("override ignored: %v/%v", in, out)
	}
}

func TestDefaultPriceStaysConservative(t *testing.T) {
	if defaultPrice[0] <= 0 || defaultPrice[1] <= 0 {
		t.Fatal("default rate must be positive")
	}
	for name, p := range priceTable {
		if p[0] <= 0 || p[1] <= 0 {
			t.Errorf("%s has a non-positive rate", name)
		}
	}
}
