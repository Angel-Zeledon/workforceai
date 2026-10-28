package memory

import (
	"context"
	"maps"

	"aiworkforce/backend/internal/application"
)

var _ application.ROIStore = (*Store)(nil)

func (s *Store) GetROISettings(_ context.Context, orgID string) (application.ROISettings, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.roi[orgID]
	v.RoleMinutes = maps.Clone(v.RoleMinutes)
	return v, ok, nil
}

func (s *Store) PutROISettings(_ context.Context, orgID string, v application.ROISettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.roi == nil {
		s.roi = map[string]application.ROISettings{}
	}
	v.RoleMinutes = maps.Clone(v.RoleMinutes)
	s.roi[orgID] = v
	return nil
}
