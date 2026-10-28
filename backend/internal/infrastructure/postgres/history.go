package postgres

import (
	"context"
	"encoding/json"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Read side of the event log and of the cost ledger over a time window (the away summary,
// docs/architecture/away-summary.md). Both use the existing (org_id, ts) indexes of events and usage_entries.

func eventLimit(n int) int {
	if n <= 0 || n > domain.MaxEventQuery {
		return domain.MaxEventQuery
	}
	return n
}

// ListEvents returns the saved events of the window, oldest first (newest first with q.Desc). The payload is
// returned as raw JSON (json.RawMessage), the same bytes the WebSocket clients received.
func (s *Store) ListEvents(ctx context.Context, orgID string, q domain.EventQuery) ([]domain.Event, error) {
	order := "ASC"
	if q.Desc {
		order = "DESC"
	}
	types := q.Types
	if types == nil {
		types = []string{}
	}
	return many(ctx, s, orgID, func(r scanner) (domain.Event, error) {
		var e domain.Event
		var agent *string
		var payload []byte
		err := r.Scan(&e.ID, &e.Type, &agent, &payload, &e.TS)
		e.OrgID = orgID
		if agent != nil {
			e.AgentID = *agent
		}
		if len(payload) > 0 {
			e.Payload = json.RawMessage(payload)
		}
		return e, err
	}, `SELECT id, type, agent_id, payload, ts FROM events
		WHERE org_id=$1 AND ts >= $2 AND ts < $3 AND (cardinality($4::text[]) = 0 OR type = ANY($4::text[]))
		ORDER BY ts `+order+`, id `+order+` LIMIT $5`, orgID, q.From, q.To, types, eventLimit(q.Limit))
}

// CountEvents counts the saved events of the window by type.
func (s *Store) CountEvents(ctx context.Context, orgID string, from, to time.Time) (map[string]int, error) {
	type row struct {
		typ string
		n   int
	}
	rows, err := many(ctx, s, orgID, func(r scanner) (row, error) {
		var x row
		err := r.Scan(&x.typ, &x.n)
		return x, err
	}, `SELECT type, count(*)::int FROM events WHERE org_id=$1 AND ts >= $2 AND ts < $3 GROUP BY type`, orgID, from, to)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, x := range rows {
		out[x.typ] = x.n
	}
	return out, nil
}

// SpendByAgent sums the cost ledger of the window per agent, highest spend first.
func (s *Store) SpendByAgent(ctx context.Context, orgID string, from, to time.Time) ([]domain.SpendRow, error) {
	return many(ctx, s, orgID, func(r scanner) (domain.SpendRow, error) {
		var x domain.SpendRow
		err := r.Scan(&x.AgentID, &x.CostUSD, &x.Calls)
		return x, err
	}, `SELECT COALESCE(agent_id, ''), COALESCE(SUM(cost_usd), 0)::float8, count(*)::int FROM usage_entries
		WHERE org_id=$1 AND ts >= $2 AND ts < $3 GROUP BY COALESCE(agent_id, '') ORDER BY 2 DESC, 1`, orgID, from, to)
}
