package memory

import (
	"context"
	"sort"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.DebateStore = (*Store)(nil)

// debateState keeps the team debates per organization (newest last).
type debateState struct {
	byOrg map[string][]domain.Debate
}

func (s *Store) CreateDebate(_ context.Context, orgID string, d domain.Debate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.debates.byOrg == nil {
		s.debates.byOrg = map[string][]domain.Debate{}
	}
	for _, cur := range s.debates.byOrg[orgID] {
		if cur.ID == d.ID {
			return domain.ErrConflict
		}
	}
	application.NormalizeDebate(&d)
	s.debates.byOrg[orgID] = append(s.debates.byOrg[orgID], d)
	return nil
}

func (s *Store) GetDebate(_ context.Context, orgID, id string) (domain.Debate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.debates.byOrg[orgID] {
		if d.ID == id {
			return d, nil
		}
	}
	return domain.Debate{}, domain.ErrNotFound
}

func (s *Store) ListDebates(_ context.Context, orgID, requestID string, limit int) ([]domain.Debate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Debate{}
	for _, d := range s.debates.byOrg[orgID] {
		if requestID == "" || d.RequestID == requestID {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
