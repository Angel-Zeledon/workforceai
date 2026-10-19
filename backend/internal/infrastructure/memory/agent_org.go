package memory

import (
	"context"
	"fmt"

	"aiworkforce/backend/internal/domain"
)

// SetAgentDepartment sets the department tag (a plain string; nil clears it) of an agent.
func (s *Store) SetAgentDepartment(_ context.Context, _ string, id string, dept *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	if !ok {
		return fmt.Errorf("%w: agent %s", domain.ErrNotFound, id)
	}
	if dept == nil {
		a.Department = nil
	} else {
		d := *dept
		a.Department = &d
	}
	s.agents[id] = a
	return nil
}

// SetAgentTools replaces the tool labels of an agent (the job description may only narrow them).
func (s *Store) SetAgentTools(_ context.Context, _ string, id string, tools []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	if !ok {
		return fmt.Errorf("%w: agent %s", domain.ErrNotFound, id)
	}
	a.Tools = append([]string{}, tools...)
	s.agents[id] = a
	return nil
}

// SetAgentResponsibilities replaces the responsibilities of an agent (its job description).
func (s *Store) SetAgentResponsibilities(_ context.Context, _ string, id string, resp []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	if !ok {
		return fmt.Errorf("%w: agent %s", domain.ErrNotFound, id)
	}
	a.Responsibilities = append([]string{}, resp...)
	s.agents[id] = a
	return nil
}
