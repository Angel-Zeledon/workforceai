package application

import "strings"

// ListedPrice returns the USD-per-million-token rates of a model of the shared
// price table ("provider/model" or "model"). ok is false for models that are
// not in the table (and for empty names), so planning code can fall back to
// its own priors instead of the conservative billing default. PRICE_*_PER_M
// overrides are deliberately ignored here: they force billing rates, not
// estimates.
func ListedPrice(model string) (in, out float64, ok bool) {
	key := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(key, "/"); i >= 0 {
		key = key[i+1:]
	}
	p, found := priceTable[key]
	if !found {
		return 0, 0, false
	}
	return p[0], p[1], true
}
