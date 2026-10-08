package memory

import (
	"context"

	"aiworkforce/backend/internal/application"
)

var _ application.ModelPolicyStore = (*Store)(nil)

func (s *Store) GetModelPolicy(_ context.Context, orgID string) (application.ModelPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modelPolicies[orgID], nil
}

func (s *Store) PutModelPolicy(_ context.Context, orgID string, p application.ModelPolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.modelPolicies == nil {
		s.modelPolicies = map[string]application.ModelPolicy{}
	}
	s.modelPolicies[orgID] = p
	return nil
}
