package memory

import (
	"context"
	"fmt"
	"slices"

	"aiworkforce/backend/internal/domain"
)

// DeleteAgent removes an agent that was just created and never finished being set up (a hire that failed half way).
func (s *Store) DeleteAgent(_ context.Context, _ string, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.agents[id]; !ok {
		return fmt.Errorf("%w: agent %s", domain.ErrNotFound, id)
	}
	delete(s.agents, id)
	s.agentOrder = slices.DeleteFunc(s.agentOrder, func(x string) bool { return x == id })
	return nil
}
