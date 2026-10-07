package connections

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemStore is an in-memory Store (simulation without Postgres, tests).
type MemStore struct {
	mu     sync.Mutex
	conns  map[string]Connection // org/id
	grants []Grant
	usage  []Usage
	states map[string]OAuthState
	used   map[string]bool
	holds  map[string]Hold // org/id
}

// NewMemStore creates an empty store.
func NewMemStore() *MemStore {
	return &MemStore{conns: map[string]Connection{}, states: map[string]OAuthState{}, used: map[string]bool{}, holds: map[string]Hold{}}
}

func key(org, id string) string { return org + "/" + id }

func (m *MemStore) CreateConnection(_ context.Context, c Connection) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.conns[key(c.OrgID, c.ID)]; ok {
		return ErrConflict
	}
	m.conns[key(c.OrgID, c.ID)] = c
	return nil
}

func (m *MemStore) GetConnection(_ context.Context, org, id string) (Connection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[key(org, id)]
	if !ok {
		return Connection{}, ErrNotFound
	}
	return c, nil
}

func (m *MemStore) UpdateConnection(_ context.Context, c Connection) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.conns[key(c.OrgID, c.ID)]; !ok {
		return ErrNotFound
	}
	m.conns[key(c.OrgID, c.ID)] = c
	return nil
}

func (m *MemStore) ListConnections(_ context.Context, org, provider, status string) ([]Connection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Connection
	for _, c := range m.conns {
		if c.OrgID == org && (provider == "" || c.Provider == provider) && (status == "" || c.Status == status) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) PutOAuthState(_ context.Context, s OAuthState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[s.StateHash] = s
	return nil
}

func (m *MemStore) ConsumeOAuthState(_ context.Context, h string, now time.Time) (OAuthState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.states[h]
	if !ok || m.used[h] || now.After(s.ExpiresAt) {
		return OAuthState{}, ErrNotFound
	}
	m.used[h] = true
	return s, nil
}

func (m *MemStore) UpsertGrant(_ context.Context, g Grant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, x := range m.grants {
		if x.ID == g.ID && x.OrgID == g.OrgID {
			m.grants[i] = g
			return nil
		}
	}
	m.grants = append(m.grants, g)
	return nil
}

func (m *MemStore) GetGrant(_ context.Context, org, conn, agent string) (Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.grants) - 1; i >= 0; i-- {
		g := m.grants[i]
		if g.OrgID == org && g.ConnectionID == conn && g.AgentID == agent && (g.Status == GrantActive || g.Status == GrantPendingApproval || g.Status == GrantSuspended) {
			return g, nil
		}
	}
	return Grant{}, ErrNotFound
}

func (m *MemStore) ListGrantsByConnection(_ context.Context, org, conn string) ([]Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Grant
	for _, g := range m.grants {
		if g.OrgID == org && g.ConnectionID == conn {
			out = append(out, g)
		}
	}
	return out, nil
}

func (m *MemStore) ListGrantsByAgent(_ context.Context, org, agent string) ([]Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Grant
	for _, g := range m.grants {
		if g.OrgID == org && g.AgentID == agent {
			out = append(out, g)
		}
	}
	return out, nil
}

func (m *MemStore) RevokeGrants(_ context.Context, org, conn, by string, at time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for i := range m.grants {
		g := &m.grants[i]
		if g.OrgID == org && g.ConnectionID == conn && g.Status != GrantRevoked {
			g.Status, g.RevokedAt, g.RevokedBy = GrantRevoked, &at, by
			n++
		}
	}
	return n, nil
}

func (m *MemStore) AddUsage(_ context.Context, u Usage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage = append(m.usage, u)
	return nil
}

func (m *MemStore) ListUsage(_ context.Context, org string, f UsageFilter) ([]Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	var out []Usage
	skipping := f.Before != ""
	for i := len(m.usage) - 1; i >= 0; i-- { // newest first
		u := m.usage[i]
		if skipping {
			if u.ID == f.Before {
				skipping = false
			}
			continue
		}
		if u.OrgID != org || (f.ConnectionID != "" && u.ConnectionID != f.ConnectionID) ||
			(f.AgentID != "" && u.AgentID != f.AgentID) || (f.Result != "" && u.Decision != f.Result) ||
			(f.From != nil && u.CreatedAt.Before(*f.From)) || (f.To != nil && u.CreatedAt.After(*f.To)) {
			continue
		}
		out = append(out, u)
		if len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

func (m *MemStore) CountUsage(_ context.Context, org, conn, agent string, since time.Time, writesOnly bool) (int, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, b := 0, int64(0)
	for _, u := range m.usage {
		if u.OrgID != org || u.ConnectionID != conn || u.CreatedAt.Before(since) {
			continue
		}
		if agent != "" && u.AgentID != agent {
			continue
		}
		if u.Decision != "allowed" || (u.Status != "succeeded" && u.Status != "scheduled") {
			continue
		}
		if writesOnly && !isWrite(u.Capability) {
			continue
		}
		n++
		b += u.BytesIn + u.BytesOut
	}
	return n, b, nil
}

// isWrite is a coarse classification of stored usage rows: write capabilities
// are the ones that are not "*.read".
func isWrite(capability string) bool {
	return len(capability) < 5 || capability[len(capability)-5:] != ".read"
}

func (m *MemStore) SumCost(_ context.Context, org, conn string, since time.Time) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var s float64
	for _, u := range m.usage {
		if u.OrgID == org && u.ConnectionID == conn && !u.CreatedAt.Before(since) {
			s += u.CostUSD
		}
	}
	return s, nil
}

func (m *MemStore) PutHold(_ context.Context, h Hold) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.holds[key(h.OrgID, h.ID)] = h
	return nil
}

func (m *MemStore) GetHold(_ context.Context, org, id string) (Hold, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.holds[key(org, id)]
	if !ok {
		return Hold{}, ErrNotFound
	}
	return h, nil
}

func (m *MemStore) UpdateHold(ctx context.Context, h Hold) error { return m.PutHold(ctx, h) }

func (m *MemStore) ListHolds(_ context.Context, org, status string) ([]Hold, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Hold
	for _, h := range m.holds {
		if h.OrgID == org && (status == "" || h.Status == status) {
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) ListDueHolds(_ context.Context, now time.Time) ([]Hold, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Hold
	for _, h := range m.holds {
		if h.Status == HoldHeld && !h.HoldUntil.After(now) {
			out = append(out, h)
		}
	}
	return out, nil
}
