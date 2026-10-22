package trust

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Record is the trust state of one agent.
type Record struct {
	AgentID string `json:"agent_id"`
	// StartLevel is the level the agent had when tracking started (the demo reset restores it).
	StartLevel string `json:"start_level"`
	// Level is the level the ladder last saw; when it differs from the agent's autonomy, somebody changed it
	// elsewhere (onboarding pack) and the evidence starts again.
	Level         string    `json:"level"`
	LevelSince    time.Time `json:"level_since"`
	TrackingSince time.Time `json:"tracking_since"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Proposal kinds and statuses.
const (
	KindPromotion = "promotion"
	KindDemotion  = "demotion"

	StatusPending    = "pending"
	StatusAccepted   = "accepted"
	StatusDeclined   = "declined"
	StatusApplied    = "applied"    // automatic demotion
	StatusReverted   = "reverted"   // an automatic demotion a human undid
	StatusSuperseded = "superseded" // the evidence or the level changed before anybody decided
)

// Proposal is a suggested (or, for automatic demotions, applied) change of level with the evidence behind it.
type Proposal struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	Kind      string    `json:"kind"`
	FromLevel string    `json:"from_level"`
	ToLevel   string    `json:"to_level"`
	Status    string    `json:"status"`
	Auto      bool      `json:"auto"`
	Reasons   []string  `json:"reasons"` // trigger keys (demotion) or "criteria_met" (promotion)
	Evidence  Evidence  `json:"evidence"`
	CreatedAt time.Time `json:"created_at"`
	// ResolvedAt / ResolvedBy / Note are set when a human accepted, declined or reverted it, or it was superseded.
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy string     `json:"resolved_by,omitempty"`
	Note       string     `json:"note,omitempty"`
}

// Evidence is the snapshot stored with a proposal: what the ladder saw when it made it.
type Evidence struct {
	Since      time.Time   `json:"since"`
	Counts     Counts      `json:"counts"`
	Quality    *Quality    `json:"quality,omitempty"`
	Criteria   []Criterion `json:"criteria"`
	Triggers   []Trigger   `json:"triggers"`
	Ceiling    string      `json:"ceiling"`
	Version    int         `json:"methodology_version"`
}

// Store persists records and proposals (MemStore here; postgres.TrustStore with migration 791).
type Store interface {
	GetRecord(ctx context.Context, org, agentID string) (Record, bool, error)
	PutRecord(ctx context.Context, org string, r Record) error
	ListRecords(ctx context.Context, org string) ([]Record, error)
	CreateProposal(ctx context.Context, org string, p Proposal) error
	GetProposal(ctx context.Context, org, id string) (Proposal, error)
	// ResolveProposal moves a proposal from one status to another in one step (domain.ErrConflict when it is not
	// in status "from", domain.ErrNotFound when missing).
	ResolveProposal(ctx context.Context, org, id, from, to, by, note string, at time.Time) (Proposal, error)
	// ListProposals returns an agent's proposals newest first (agentID "" = all agents; status "" = any).
	ListProposals(ctx context.Context, org, agentID, status string, limit int) ([]Proposal, error)
	// Reset removes the records and proposals of the organization (demo reset).
	Reset(ctx context.Context, org string) error
}

// MemStore keeps the ladder in memory (demo mode and tests).
type MemStore struct {
	mu        sync.Mutex
	records   map[string]map[string]Record // org -> agent -> record
	proposals map[string][]Proposal        // org -> proposals in insertion order
}

// NewMemStore builds an empty store.
func NewMemStore() *MemStore {
	return &MemStore{records: map[string]map[string]Record{}, proposals: map[string][]Proposal{}}
}

var _ Store = (*MemStore)(nil)

func clone(p Proposal) Proposal {
	p.Reasons = append([]string{}, p.Reasons...)
	p.Evidence.Criteria = append([]Criterion{}, p.Evidence.Criteria...)
	p.Evidence.Triggers = append([]Trigger{}, p.Evidence.Triggers...)
	return p
}

func (m *MemStore) GetRecord(_ context.Context, org, agentID string) (Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[org][agentID]
	return r, ok, nil
}

func (m *MemStore) PutRecord(_ context.Context, org string, r Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.records[org] == nil {
		m.records[org] = map[string]Record{}
	}
	m.records[org][r.AgentID] = r
	return nil
}

func (m *MemStore) ListRecords(_ context.Context, org string) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Record{}
	for _, r := range m.records[org] {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out, nil
}

func (m *MemStore) CreateProposal(_ context.Context, org string, p Proposal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.proposals[org] = append(m.proposals[org], clone(p))
	return nil
}

func (m *MemStore) GetProposal(_ context.Context, org, id string) (Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.proposals[org] {
		if p.ID == id {
			return clone(p), nil
		}
	}
	return Proposal{}, fmt.Errorf("%w: proposal", domain.ErrNotFound)
}

func (m *MemStore) ResolveProposal(_ context.Context, org, id, from, to, by, note string, at time.Time) (Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, p := range m.proposals[org] {
		if p.ID != id {
			continue
		}
		if p.Status != from {
			return Proposal{}, fmt.Errorf("%w: proposal is %s", domain.ErrConflict, p.Status)
		}
		t := at
		p.Status, p.ResolvedAt, p.ResolvedBy, p.Note = to, &t, by, note
		m.proposals[org][i] = p
		return clone(p), nil
	}
	return Proposal{}, fmt.Errorf("%w: proposal", domain.ErrNotFound)
}

func (m *MemStore) ListProposals(_ context.Context, org, agentID, status string, limit int) ([]Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := []Proposal{}
	all := m.proposals[org]
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		if (agentID == "" || all[i].AgentID == agentID) && (status == "" || all[i].Status == status) {
			out = append(out, clone(all[i]))
		}
	}
	return out, nil
}

func (m *MemStore) Reset(_ context.Context, org string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, org)
	delete(m.proposals, org)
	return nil
}
