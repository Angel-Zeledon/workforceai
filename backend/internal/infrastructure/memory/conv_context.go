package memory

import (
	"context"

	"aiworkforce/backend/internal/application"
)

// Conversation context of /clear and /compact (application.ConvContextStore).

var _ application.ConvContextStore = (*Store)(nil)

type convCtxKey struct{ org, conv string }

func (s *Store) GetConvContext(_ context.Context, org, conv string) (application.ConvContext, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convCtx[convCtxKey{org, conv}]
	return c, ok, nil
}

func (s *Store) PutConvContext(_ context.Context, org string, c application.ConvContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.convCtx == nil {
		s.convCtx = map[convCtxKey]application.ConvContext{}
	}
	s.convCtx[convCtxKey{org, c.ConversationID}] = c
	return nil
}
