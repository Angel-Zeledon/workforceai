package memory

import (
	"context"
	"slices"
	"sort"
	"time"

	"aiworkforce/backend/internal/away"
	"aiworkforce/backend/internal/domain"
)

var _ away.History = (*Store)(nil)

// Read side of the event log and of the cost ledger over a time window (the away summary,
// docs/architecture/away-summary.md). The Postgres store answers the same questions with SQL.

func inWindow(ts, from, to time.Time) bool { return !ts.Before(from) && ts.Before(to) }

func eventLimit(n int) int {
	if n <= 0 || n > domain.MaxEventQuery {
		return domain.MaxEventQuery
	}
	return n
}

// ListEvents returns the saved events of the window, oldest first (newest first with q.Desc).
func (s *Store) ListEvents(_ context.Context, orgID string, q domain.EventQuery) ([]domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Event{}
	for _, e := range s.events {
		if (e.OrgID != "" && e.OrgID != orgID) || !inWindow(e.TS, q.From, q.To) {
			continue
		}
		if len(q.Types) > 0 && !slices.Contains(q.Types, e.Type) {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if q.Desc {
			return out[i].TS.After(out[j].TS)
		}
		return out[i].TS.Before(out[j].TS)
	})
	if n := eventLimit(q.Limit); len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// CountEvents counts the saved events of the window by type.
func (s *Store) CountEvents(_ context.Context, orgID string, from, to time.Time) (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for _, e := range s.events {
		if (e.OrgID == "" || e.OrgID == orgID) && inWindow(e.TS, from, to) {
			out[e.Type]++
		}
	}
	return out, nil
}

// SpendByAgent sums the cost ledger of the window per agent, highest spend first.
func (s *Store) SpendByAgent(_ context.Context, _ string, from, to time.Time) ([]domain.SpendRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	by := map[string]*domain.SpendRow{}
	for _, u := range s.usage {
		if !inWindow(u.TS, from, to) {
			continue
		}
		r := by[u.AgentID]
		if r == nil {
			r = &domain.SpendRow{AgentID: u.AgentID}
			by[u.AgentID] = r
		}
		r.CostUSD += u.CostUSD
		r.Calls++
	}
	out := make([]domain.SpendRow, 0, len(by))
	for _, r := range by {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CostUSD != out[j].CostUSD {
			return out[i].CostUSD > out[j].CostUSD
		}
		return out[i].AgentID < out[j].AgentID
	})
	return out, nil
}
