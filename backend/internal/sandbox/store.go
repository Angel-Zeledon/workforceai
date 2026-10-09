package sandbox

import (
	"context"
	"sort"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Store persists runs and the per-organization settings. Every method is
// org-scoped (and, in Postgres, runs under Row-Level Security).
type Store interface {
	CreateRun(ctx context.Context, r Run) error
	GetRun(ctx context.Context, org, id string) (Run, error)
	GetRunByApproval(ctx context.Context, org, approvalID string) (Run, error)
	ListRuns(ctx context.Context, org string, limit int) ([]Run, error)
	// BeginRun atomically moves a run from awaiting_approval to running and
	// records who approved it. It returns false when the run is in any other
	// state (already running, finished, rejected): a run starts at most once.
	BeginRun(ctx context.Context, org, id, approvalID string, approvers []string, at time.Time) (bool, error)
	// FinishRun stores the outcome of a running run.
	FinishRun(ctx context.Context, org string, r Run) error
	// CloseRun moves an awaiting_approval run to a terminal status without
	// running it (rejected, denied). It returns false if it was not awaiting.
	CloseRun(ctx context.Context, org, id, status, reason string, at time.Time) (bool, error)
	// CountStartedSince counts the runs that started at or after since.
	CountStartedSince(ctx context.Context, org string, since time.Time) (int, error)
	GetSettings(ctx context.Context, org string) (Settings, error)
	PutSettings(ctx context.Context, org string, s Settings) error
}

// MemoryStore is an in-memory Store (tests, demo without Postgres).
type MemoryStore struct {
	mu       sync.Mutex
	runs     map[string]Run // org/id
	settings map[string]Settings
}

// NewMemoryStore creates an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{runs: map[string]Run{}, settings: map[string]Settings{}}
}

func key(org, id string) string { return org + "/" + id }

// CreateRun implements Store.
func (m *MemoryStore) CreateRun(_ context.Context, r Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[key(r.OrgID, r.ID)]; ok {
		return domain.ErrConflict
	}
	m.runs[key(r.OrgID, r.ID)] = r
	return nil
}

// GetRun implements Store.
func (m *MemoryStore) GetRun(_ context.Context, org, id string) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[key(org, id)]
	if !ok {
		return Run{}, domain.ErrNotFound
	}
	return r, nil
}

// GetRunByApproval implements Store.
func (m *MemoryStore) GetRunByApproval(_ context.Context, org, approvalID string) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.runs {
		if r.OrgID == org && r.ApprovalID == approvalID && approvalID != "" {
			return r, nil
		}
	}
	return Run{}, domain.ErrNotFound
}

// ListRuns implements Store (newest first).
func (m *MemoryStore) ListRuns(_ context.Context, org string, limit int) ([]Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Run
	for _, r := range m.runs {
		if r.OrgID == org {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// BeginRun implements Store.
func (m *MemoryStore) BeginRun(_ context.Context, org, id, approvalID string, approvers []string, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[key(org, id)]
	if !ok {
		return false, domain.ErrNotFound
	}
	if r.Status != StatusAwaiting || r.ApprovalID != approvalID {
		return false, nil
	}
	r.Status, r.Approvers, r.StartedAt = StatusRunning, append([]string(nil), approvers...), &at
	m.runs[key(org, id)] = r
	return true, nil
}

// FinishRun implements Store.
func (m *MemoryStore) FinishRun(_ context.Context, org string, r Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.runs[key(org, r.ID)]
	if !ok {
		return domain.ErrNotFound
	}
	if cur.Status != StatusRunning {
		return domain.ErrConflict
	}
	cur.Status, cur.DenyReason, cur.ExitCode, cur.DurationMS = r.Status, r.DenyReason, r.ExitCode, r.DurationMS
	cur.Stdout, cur.Stderr, cur.StdoutTruncated, cur.StderrTruncated = r.Stdout, r.Stderr, r.StdoutTruncated, r.StderrTruncated
	cur.Outputs, cur.FinishedAt = r.Outputs, r.FinishedAt
	m.runs[key(org, r.ID)] = cur
	return nil
}

// CloseRun implements Store.
func (m *MemoryStore) CloseRun(_ context.Context, org, id, status, reason string, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[key(org, id)]
	if !ok {
		return false, domain.ErrNotFound
	}
	if r.Status != StatusAwaiting {
		return false, nil
	}
	r.Status, r.DenyReason, r.FinishedAt = status, reason, &at
	m.runs[key(org, id)] = r
	return true, nil
}

// CountStartedSince implements Store.
func (m *MemoryStore) CountStartedSince(_ context.Context, org string, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.runs {
		if r.OrgID == org && r.StartedAt != nil && !r.StartedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

// GetSettings implements Store.
func (m *MemoryStore) GetSettings(_ context.Context, org string) (Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings[org], nil
}

// PutSettings implements Store.
func (m *MemoryStore) PutSettings(_ context.Context, org string, s Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[org] = s
	return nil
}
