package rituals

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// MemStore keeps rituals in memory (demo without Postgres; data is lost on restart).
type MemStore struct {
	mu        sync.Mutex
	rituals   map[string]map[string]Ritual // org -> id -> ritual
	proposals map[string]map[string]Proposal
	settings  map[string]Settings
}

var _ Store = (*MemStore)(nil)

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{rituals: map[string]map[string]Ritual{}, proposals: map[string]map[string]Proposal{}, settings: map[string]Settings{}}
}

// clone deep-copies through JSON so callers never share slices with the store.
func clone[T any](v T) T {
	var out T
	b, _ := json.Marshal(v)
	_ = json.Unmarshal(b, &out)
	return out
}

func (m *MemStore) CreateRitual(_ context.Context, org string, r Ritual) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rituals[org] == nil {
		m.rituals[org] = map[string]Ritual{}
	}
	if r.Origin == OriginScheduled {
		for _, x := range m.rituals[org] {
			if x.Origin == OriginScheduled && x.Kind == r.Kind && x.PeriodKey == r.PeriodKey {
				return false, nil
			}
		}
	}
	if _, dup := m.rituals[org][r.ID]; dup {
		return false, domain.ErrConflict
	}
	r.Proposals = nil
	m.rituals[org][r.ID] = clone(r)
	return true, nil
}

func (m *MemStore) HasScheduled(_ context.Context, org string, kind Kind, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.rituals[org] {
		if x.Origin == OriginScheduled && x.Kind == kind && x.PeriodKey == key {
			return true, nil
		}
	}
	return false, nil
}

func (m *MemStore) SetRitualSummary(_ context.Context, org, id, summary, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rituals[org][id]
	if !ok {
		return domain.ErrNotFound
	}
	r.Summary, r.SummarySource = summary, source
	m.rituals[org][id] = r
	return nil
}

func (m *MemStore) GetRitual(_ context.Context, org, id string) (Ritual, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rituals[org][id]
	if !ok {
		return Ritual{}, domain.ErrNotFound
	}
	return clone(r), nil
}

func (m *MemStore) ListRituals(_ context.Context, org string, kind Kind, since time.Time, limit int) ([]Ritual, error) {
	if limit <= 0 {
		limit = 30
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Ritual{}
	for _, r := range m.rituals[org] {
		if (kind == "" || r.Kind == kind) && !r.HeldAt.Before(since) {
			out = append(out, clone(r))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].HeldAt.Equal(out[j].HeldAt) {
			return out[i].HeldAt.After(out[j].HeldAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) CreateProposals(_ context.Context, org string, ps []Proposal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.proposals[org] == nil {
		m.proposals[org] = map[string]Proposal{}
	}
	for _, p := range ps {
		m.proposals[org][p.ID] = clone(p)
	}
	return nil
}

func (m *MemStore) GetProposal(_ context.Context, org, id string) (Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.proposals[org][id]
	if !ok {
		return Proposal{}, domain.ErrNotFound
	}
	return clone(p), nil
}

func (m *MemStore) ListProposals(_ context.Context, org string, q ProposalQuery) ([]Proposal, error) {
	if q.Limit <= 0 {
		q.Limit = 100
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Proposal{}
	for _, p := range m.proposals[org] {
		if (q.Status == "" || p.Status == q.Status) && (q.AgentID == "" || p.AgentID == q.AgentID) && (q.RitualID == "" || p.RitualID == q.RitualID) {
			out = append(out, clone(p))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (m *MemStore) ResolveProposal(_ context.Context, org, id, from, to, by string, applied bool, at time.Time) (Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.proposals[org][id]
	if !ok {
		return Proposal{}, domain.ErrNotFound
	}
	if p.Status != from {
		return Proposal{}, domain.ErrConflict
	}
	p.Status = to
	if to == ProposalPending {
		p.ResolvedAt, p.ResolvedBy, p.Applied = nil, "", false
	} else {
		t := at
		p.ResolvedAt, p.ResolvedBy, p.Applied = &t, by, applied
	}
	m.proposals[org][id] = p
	return clone(p), nil
}

func (m *MemStore) GetSettings(_ context.Context, org string) (Settings, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.settings[org]
	return clone(s), ok, nil
}

func (m *MemStore) PutSettings(_ context.Context, org string, s Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[org] = clone(s)
	return nil
}
