package memory

import (
	"context"
	"sort"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.RehearsalStore = (*Store)(nil)

// rehearsalState holds the per-agent switches (configuration: they survive a
// demo reset) and the rehearsed actions (execution data: cleared by a reset).
type rehearsalState struct {
	agents  map[[2]string]domain.AgentRehearsal
	actions []rehearsalRow
}

type rehearsalRow struct {
	org string
	a   domain.RehearsalAction
}

func cloneRehearsal(a domain.RehearsalAction) domain.RehearsalAction {
	a.Recipients = append([]string{}, a.Recipients...)
	a.Flags = append([]string{}, a.Flags...)
	p := make(map[string]string, len(a.Preview))
	for k, v := range a.Preview {
		p[k] = v
	}
	a.Preview = p
	if a.Amount != nil {
		v := *a.Amount
		a.Amount = &v
	}
	if a.VerdictAt != nil {
		v := *a.VerdictAt
		a.VerdictAt = &v
	}
	return a
}

func (s *Store) GetAgentRehearsal(_ context.Context, orgID, agentID string) (domain.AgentRehearsal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rehearsal.agents[[2]string{orgID, agentID}]
	if !ok {
		return domain.AgentRehearsal{}, domain.ErrNotFound
	}
	return r, nil
}

func (s *Store) ListAgentRehearsals(_ context.Context, orgID string) ([]domain.AgentRehearsal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.AgentRehearsal{}
	for k, r := range s.rehearsal.agents {
		if k[0] == orgID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out, nil
}

func (s *Store) PutAgentRehearsal(_ context.Context, orgID string, r domain.AgentRehearsal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rehearsal.agents == nil {
		s.rehearsal.agents = map[[2]string]domain.AgentRehearsal{}
	}
	s.rehearsal.agents[[2]string{orgID, r.AgentID}] = r
	return nil
}

func (s *Store) AddRehearsalAction(_ context.Context, orgID string, a domain.RehearsalAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rehearsal.actions = append(s.rehearsal.actions, rehearsalRow{org: orgID, a: cloneRehearsal(a)})
	return nil
}

func (s *Store) GetRehearsalAction(_ context.Context, orgID, id string) (domain.RehearsalAction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rehearsal.actions {
		if r.org == orgID && r.a.ID == id {
			return cloneRehearsal(r.a), nil
		}
	}
	return domain.RehearsalAction{}, domain.ErrNotFound
}

func (s *Store) ListRehearsalActions(_ context.Context, orgID string, q domain.RehearsalQuery) ([]domain.RehearsalAction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.RehearsalAction{}
	for i := len(s.rehearsal.actions) - 1; i >= 0; i-- { // newest first
		r := s.rehearsal.actions[i]
		if r.org != orgID || (q.RequestID != "" && r.a.RequestID != q.RequestID) || (q.AgentID != "" && r.a.AgentID != q.AgentID) {
			continue
		}
		out = append(out, cloneRehearsal(r.a))
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

func (s *Store) SetRehearsalVerdict(_ context.Context, orgID, id, verdict, by string, at time.Time) (domain.RehearsalAction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.rehearsal.actions {
		r := &s.rehearsal.actions[i]
		if r.org != orgID || r.a.ID != id {
			continue
		}
		r.a.Verdict, r.a.VerdictBy, r.a.VerdictAt = verdict, by, &at
		if verdict == "" {
			r.a.VerdictBy, r.a.VerdictAt = "", nil
		}
		return cloneRehearsal(r.a), nil
	}
	return domain.RehearsalAction{}, domain.ErrNotFound
}
