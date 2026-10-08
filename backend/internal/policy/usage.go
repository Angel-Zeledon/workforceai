package policy

import (
	"strings"
	"time"
)

// UsageEvent is one call counted against a windowed limit (exported for
// persistence: internal/counters).
type UsageEvent struct {
	At     time.Time `json:"at"`
	Amount float64   `json:"amount,omitempty"`
}

// UsageSnapshot returns the usage still inside the window of a configured
// limit, keyed like the engine counts it (limit id, or "limit id|agent").
// Keys of limits that no longer exist and expired events are left out.
func (e *Engine) UsageSnapshot() map[string][]UsageEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	windows := map[string]time.Duration{}
	for _, l := range e.cfg.Limits {
		if l.Window > windows[l.ID] {
			windows[l.ID] = l.Window
		}
	}
	now := e.now()
	out := map[string][]UsageEvent{}
	for k, evs := range e.usage {
		id, _, _ := strings.Cut(k, "|")
		w, ok := windows[id]
		if !ok {
			continue
		}
		cut := now.Add(-w)
		var keep []UsageEvent
		for _, ev := range evs {
			if ev.t.After(cut) {
				keep = append(keep, UsageEvent{At: ev.t.UTC(), Amount: ev.amount})
			}
		}
		if len(keep) > 0 {
			out[k] = keep
		}
	}
	return out
}

// RestoreUsage merges previously persisted usage into the engine (used once,
// when the engine of an organization is built after a restart). Events
// already counted in this process are kept.
func (e *Engine) RestoreUsage(u map[string][]UsageEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for k, evs := range u {
		for _, ev := range evs {
			e.usage[k] = append(e.usage[k], usageEvent{t: ev.At, amount: ev.Amount})
		}
	}
}
