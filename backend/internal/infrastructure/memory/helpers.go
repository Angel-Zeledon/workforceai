package memory

import (
	"context"
	"fmt"
	"slices"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.HelperStore = (*Store)(nil)

func cloneGroup(g domain.HelperGroup) domain.HelperGroup {
	parts := make([]domain.HelperPart, len(g.Parts))
	for i, p := range g.Parts {
		p.Files = slices.Clone(p.Files)
		parts[i] = p
	}
	g.Parts = parts
	if g.FinishedAt != nil {
		t := *g.FinishedAt
		g.FinishedAt = &t
	}
	return g
}

func (s *Store) CreateHelperGroup(_ context.Context, orgID string, g domain.HelperGroup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.helperGroups == nil {
		s.helperGroups = map[string][]domain.HelperGroup{}
	}
	for _, x := range s.helperGroups[orgID] {
		if x.ID == g.ID || x.RequestID == g.RequestID {
			return fmt.Errorf("%w: the request already has helpers", domain.ErrConflict)
		}
	}
	s.helperGroups[orgID] = append(s.helperGroups[orgID], cloneGroup(g))
	return nil
}

func (s *Store) GetHelperGroup(_ context.Context, orgID, id string) (domain.HelperGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.helperGroups[orgID] {
		if g.ID == id {
			return cloneGroup(g), nil
		}
	}
	return domain.HelperGroup{}, domain.ErrNotFound
}

func (s *Store) GetHelperGroupByRequest(_ context.Context, orgID, requestID string) (domain.HelperGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.helperGroups[orgID] {
		if g.RequestID == requestID {
			return cloneGroup(g), nil
		}
	}
	return domain.HelperGroup{}, domain.ErrNotFound
}

func (s *Store) UpdateHelperGroup(_ context.Context, orgID string, g domain.HelperGroup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.helperGroups[orgID] {
		if x.ID == g.ID {
			g.SpentUSD = x.SpentUSD
			s.helperGroups[orgID][i] = cloneGroup(g)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (s *Store) AddHelperSpend(_ context.Context, orgID, id string, usd float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.helperGroups[orgID] {
		if x.ID == id {
			s.helperGroups[orgID][i].SpentUSD += usd
			return nil
		}
	}
	return domain.ErrNotFound
}

func (s *Store) ListHelperGroups(_ context.Context, orgID string, statuses ...string) ([]domain.HelperGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.HelperGroup
	for _, g := range s.helperGroups[orgID] {
		if len(statuses) == 0 || slices.Contains(statuses, g.Status) {
			out = append(out, cloneGroup(g))
		}
	}
	return out, nil
}
