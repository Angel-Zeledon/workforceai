package memory

import (
	"context"
	"slices"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.StandingStore = (*Store)(nil)

// standing holds the standing approvals by org (kept in creation order).
type standingState struct {
	byOrg map[string][]application.StandingApproval
}

func (s *Store) CreateStanding(_ context.Context, orgID string, st application.StandingApproval) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.standing.byOrg == nil {
		s.standing.byOrg = map[string][]application.StandingApproval{}
	}
	s.standing.byOrg[orgID] = append(s.standing.byOrg[orgID], st)
	return nil
}

func (s *Store) ListStanding(_ context.Context, orgID, projectID string) ([]application.StandingApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []application.StandingApproval{}
	for _, st := range s.standing.byOrg[orgID] {
		if st.ProjectID == projectID {
			out = append(out, st)
		}
	}
	slices.Reverse(out) // newest first
	return out, nil
}

func (s *Store) find(orgID, id string) (int, bool) {
	for i, st := range s.standing.byOrg[orgID] {
		if st.ID == id {
			return i, true
		}
	}
	return 0, false
}

func (s *Store) GetStanding(_ context.Context, orgID, id string) (application.StandingApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.find(orgID, id)
	if !ok {
		return application.StandingApproval{}, domain.ErrNotFound
	}
	return s.standing.byOrg[orgID][i], nil
}

func (s *Store) RevokeStanding(_ context.Context, orgID, id, by string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.find(orgID, id)
	if !ok {
		return domain.ErrNotFound
	}
	if st := &s.standing.byOrg[orgID][i]; st.RevokedAt == nil {
		st.RevokedAt, st.RevokedBy = &at, by
	}
	return nil
}

func (s *Store) TouchStanding(_ context.Context, orgID, id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.find(orgID, id)
	if !ok {
		return domain.ErrNotFound
	}
	st := &s.standing.byOrg[orgID][i]
	st.UseCount++
	st.LastUsedAt = &at
	return nil
}
