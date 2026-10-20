package quality

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// MemStore keeps golden cases and runs in memory (demo mode and tests).
type MemStore struct {
	mu    sync.Mutex
	cases map[string]map[string]Case // org -> id -> case
	runs  map[string][]Run           // org -> runs in insertion order
}

// NewMemStore builds an empty store.
func NewMemStore() *MemStore {
	return &MemStore{cases: map[string]map[string]Case{}, runs: map[string][]Run{}}
}

var _ Store = (*MemStore)(nil)

func notFound() error { return fmt.Errorf("%w: golden case", domain.ErrNotFound) }

func cloneCase(c Case) Case {
	c.Checks = append([]Check{}, c.Checks...)
	return c
}

func (m *MemStore) CreateCase(_ context.Context, org string, c Case) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cases[org] == nil {
		m.cases[org] = map[string]Case{}
	}
	m.cases[org][c.ID] = cloneCase(c)
	return nil
}

func (m *MemStore) GetCase(_ context.Context, org, id string) (Case, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cases[org][id]
	if !ok {
		return Case{}, notFound()
	}
	return cloneCase(c), nil
}

func (m *MemStore) UpdateCase(_ context.Context, org string, c Case) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cases[org][c.ID]; !ok {
		return notFound()
	}
	m.cases[org][c.ID] = cloneCase(c)
	return nil
}

func (m *MemStore) DeleteCase(_ context.Context, org, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cases[org][id]; !ok {
		return notFound()
	}
	delete(m.cases[org], id)
	return nil
}

func (m *MemStore) ListCases(_ context.Context, org, agentID string) ([]Case, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Case{}
	for _, c := range m.cases[org] {
		if agentID == "" || c.AgentID == agentID {
			out = append(out, cloneCase(c))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemStore) AddRun(_ context.Context, org string, r Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[org] = append(m.runs[org], r)
	return nil
}

func (m *MemStore) ListRuns(_ context.Context, org, caseID string, limit int) ([]Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Run{}
	all := m.runs[org]
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		if all[i].CaseID == caseID {
			out = append(out, all[i])
		}
	}
	return out, nil
}

func (m *MemStore) GoldenCounts(_ context.Context, org string, from, to time.Time) (map[string]GoldenCount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]GoldenCount{}
	for _, r := range m.runs[org] {
		if r.CreatedAt.Before(from) || !r.CreatedAt.Before(to) {
			continue
		}
		g := out[r.AgentID]
		g.Runs++
		if r.Passed {
			g.Passed++
		}
		out[r.AgentID] = g
	}
	return out, nil
}

// StoreSignals derives the counts from the application store (tasks and approvals), so it works
// with the memory store too. It cannot count retries (no event listing), so RetriesTracked stays
// false and the retries component stays out of the score. The Postgres store has an equivalent set
// of SQL queries (postgres.QualityStore.Collect).
type StoreSignals struct{ Store application.Store }

var _ Signals = StoreSignals{}

// SystemRejection tells whether a rejection came from the system (timeout, restart) rather than from
// a human decision; those say nothing about the agent's work.
func SystemRejection(note string) bool {
	n := strings.ToLower(strings.TrimSpace(note))
	return strings.HasPrefix(n, "sin respuesta") || strings.HasPrefix(n, "superseded:")
}

// Collect implements Signals.
func (s StoreSignals) Collect(ctx context.Context, org string, ranges []Range) ([]map[string]Counts, error) {
	tasks, err := s.Store.ListTasks(ctx, org, "", "")
	if err != nil {
		return nil, err
	}
	approvals, err := s.Store.ListApprovals(ctx, org, "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]Counts, len(ranges))
	for i := range out {
		out[i] = map[string]Counts{}
	}
	for ri, r := range ranges {
		m := out[ri]
		for _, t := range tasks {
			if t.Status != domain.TaskDone && t.Status != domain.TaskFailed {
				continue
			}
			at := t.CreatedAt
			if t.StartedAt != nil {
				at = *t.StartedAt
			}
			if t.FinishedAt != nil {
				at = *t.FinishedAt
			}
			if !r.In(at) {
				continue
			}
			c := m[t.AgentID]
			if t.Status == domain.TaskDone {
				c.TasksDone++
			} else {
				c.TasksFailed++
			}
			if t.Output != nil && t.Output.Review != nil && t.Output.Review.Verdict != "" {
				c.Reviewed++
				if t.Output.Review.Verdict == domain.VerdictPass && t.Output.Review.Reworks == 0 {
					c.ReviewFirstPass++
				}
			}
			m[t.AgentID] = c
		}
		for _, a := range approvals {
			if a.ResolvedAt == nil || !r.In(*a.ResolvedAt) {
				continue
			}
			c := m[a.AgentID]
			switch {
			case a.Status == domain.ApprovalApproved:
				c.Approved++
			case a.Status == domain.ApprovalRejected && !SystemRejection(a.Note):
				c.Rejected++
			}
			m[a.AgentID] = c
		}
	}
	return out, nil
}
