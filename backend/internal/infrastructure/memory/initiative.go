package memory

import (
	"context"
	"slices"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.InitiativeStore = (*Store)(nil)

// initiativeState holds the suggestions by org (creation order) and the settings by org.
type initiativeState struct {
	byOrg    map[string][]application.Initiative
	settings map[string]application.InitiativeSettings
}

func (s *Store) CreateInitiative(_ context.Context, orgID string, it application.Initiative) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initiative.byOrg == nil {
		s.initiative.byOrg = map[string][]application.Initiative{}
	}
	s.initiative.byOrg[orgID] = append(s.initiative.byOrg[orgID], it)
	return nil
}

func (s *Store) ListInitiatives(_ context.Context, orgID string, status application.InitiativeStatus, limit int) ([]application.Initiative, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	out := []application.Initiative{}
	for _, it := range s.initiative.byOrg[orgID] {
		if status == "" || it.Status == status {
			out = append(out, it)
		}
	}
	slices.Reverse(out) // newest first
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) findInitiative(orgID, id string) (int, bool) {
	for i, it := range s.initiative.byOrg[orgID] {
		if it.ID == id {
			return i, true
		}
	}
	return 0, false
}

func (s *Store) GetInitiative(_ context.Context, orgID, id string) (application.Initiative, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.findInitiative(orgID, id)
	if !ok {
		return application.Initiative{}, domain.ErrNotFound
	}
	return s.initiative.byOrg[orgID][i], nil
}

func (s *Store) ResolveInitiative(_ context.Context, orgID, id string, from, to application.InitiativeStatus, by, requestID string, at time.Time) (application.Initiative, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.findInitiative(orgID, id)
	if !ok {
		return application.Initiative{}, domain.ErrNotFound
	}
	it := &s.initiative.byOrg[orgID][i]
	if it.Status != from {
		return application.Initiative{}, domain.ErrConflict
	}
	it.Status = to
	if to == application.InitiativePending {
		it.ResolvedAt, it.ResolvedBy, it.RequestID = nil, "", ""
	} else {
		it.ResolvedAt, it.ResolvedBy = &at, by
		if requestID != "" {
			it.RequestID = requestID
		}
	}
	return *it, nil
}

func (s *Store) CountInitiatives(_ context.Context, orgID, agentID string, since time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, it := range s.initiative.byOrg[orgID] {
		if !it.CreatedAt.Before(since) && (agentID == "" || it.AgentID == agentID) {
			n++
		}
	}
	return n, nil
}

func (s *Store) HasInitiativeSignal(_ context.Context, orgID, signal, key string, since time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.initiative.byOrg[orgID] {
		if it.Signal == signal && it.SignalKey == key && !it.CreatedAt.Before(since) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) GetInitiativeSettings(_ context.Context, orgID string) (application.InitiativeSettings, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.initiative.settings[orgID]
	return cloneInitiativeSettings(v), ok, nil
}

func (s *Store) PutInitiativeSettings(_ context.Context, orgID string, v application.InitiativeSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initiative.settings == nil {
		s.initiative.settings = map[string]application.InitiativeSettings{}
	}
	s.initiative.settings[orgID] = cloneInitiativeSettings(v)
	return nil
}

// cloneInitiativeSettings copies the map and the signals so callers never share state with the store.
func cloneInitiativeSettings(v application.InitiativeSettings) application.InitiativeSettings {
	if v.AgentDaily != nil {
		m := make(map[string]int, len(v.AgentDaily))
		for k, n := range v.AgentDaily {
			m[k] = n
		}
		v.AgentDaily = m
	}
	if v.Signals != nil {
		sig := *v.Signals
		v.Signals = &sig
	}
	return v
}
