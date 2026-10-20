package memory

import (
	"context"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.CounterargumentStore = (*Store)(nil)

// counterargumentState caches the devil's advocate reviews, keyed by org and approval id.
type counterargumentState struct {
	byKey map[[2]string]domain.Counterargument
}

func (s *Store) GetCounterargument(_ context.Context, orgID, approvalID string) (domain.Counterargument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.counter.byKey[[2]string{orgID, approvalID}]
	if !ok {
		return domain.Counterargument{}, domain.ErrNotFound
	}
	return c, nil
}

// PutCounterargument keeps the first review of an approval (see the Postgres store).
func (s *Store) PutCounterargument(_ context.Context, orgID string, c domain.Counterargument) (domain.Counterargument, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.counter.byKey[[2]string{orgID, c.ApprovalID}]; ok {
		return cur, false, nil
	}
	if s.counter.byKey == nil {
		s.counter.byKey = map[[2]string]domain.Counterargument{}
	}
	c.Advisory = true
	s.counter.byKey[[2]string{orgID, c.ApprovalID}] = c
	return c, true, nil
}
