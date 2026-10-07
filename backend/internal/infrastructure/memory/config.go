package memory

import (
	"context"
	"slices"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// configState is the in-memory organization configuration. Like the rest of
// this store it models a single organization; orgID arguments are ignored.
// It deliberately survives Reset (the Postgres store keeps these tables too).
type configState struct {
	settings  *application.OrgSettings
	tones     map[string]string
	schedules map[string]application.Schedule
	order     []string
}

var (
	_ application.ConfigStore = (*Store)(nil)
	_ application.OrgLister   = (*Store)(nil)
)

func (s *Store) GetOrgSettings(_ context.Context, _ string) (application.OrgSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.settings == nil {
		return application.DefaultOrgSettings(), nil
	}
	return *s.cfg.settings, nil
}

func (s *Store) PutOrgSettings(_ context.Context, _ string, st application.OrgSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st.AgentTones, st.Recommendation = nil, nil // derived, never stored
	s.cfg.settings = &st
	return nil
}

func (s *Store) ListAgentTones(_ context.Context, _ string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.cfg.tones))
	for k, v := range s.cfg.tones {
		out[k] = v
	}
	return out, nil
}

func (s *Store) SetAgentTone(_ context.Context, _, agentID, tone string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.tones == nil {
		s.cfg.tones = map[string]string{}
	}
	if tone == "" {
		delete(s.cfg.tones, agentID)
	} else {
		s.cfg.tones[agentID] = tone
	}
	return nil
}

func (s *Store) SetAgentAutonomy(_ context.Context, _, agentID, autonomy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[agentID]
	if !ok {
		return domain.ErrNotFound
	}
	a.Autonomy = autonomy
	s.agents[agentID] = a
	return nil
}

func (s *Store) ListSchedules(_ context.Context, _ string) ([]application.Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]application.Schedule, 0, len(s.cfg.order))
	for _, id := range s.cfg.order {
		out = append(out, s.cfg.schedules[id])
	}
	return out, nil
}

func (s *Store) GetSchedule(_ context.Context, _, id string) (application.Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.cfg.schedules[id]
	if !ok {
		return application.Schedule{}, domain.ErrNotFound
	}
	return sc, nil
}

func (s *Store) PutSchedule(_ context.Context, _ string, sc application.Schedule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.schedules == nil {
		s.cfg.schedules = map[string]application.Schedule{}
	}
	if _, ok := s.cfg.schedules[sc.ID]; !ok {
		s.cfg.order = append(s.cfg.order, sc.ID)
	}
	s.cfg.schedules[sc.ID] = sc
	return nil
}

func (s *Store) DeleteSchedule(_ context.Context, _, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cfg.schedules[id]; !ok {
		return domain.ErrNotFound
	}
	delete(s.cfg.schedules, id)
	s.cfg.order = slices.DeleteFunc(s.cfg.order, func(x string) bool { return x == id })
	return nil
}

func (s *Store) ClaimSchedule(_ context.Context, _, id string, expected, next, ranAt time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.cfg.schedules[id]
	if !ok || !sc.Enabled || sc.NextRunAt == nil || !sc.NextRunAt.Equal(expected) {
		return false, nil
	}
	sc.NextRunAt, sc.LastRunAt = &next, &ranAt
	s.cfg.schedules[id] = sc
	return true, nil
}

// ListOrgIDs lists the organizations of this single-tenant store (none: the
// scheduler always visits the default organization).
func (s *Store) ListOrgIDs(context.Context) ([]string, error) { return nil, nil }
