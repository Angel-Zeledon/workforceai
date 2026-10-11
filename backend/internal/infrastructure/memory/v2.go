package memory

import (
	"context"
	"slices"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

type v2State struct {
	profiles     map[string]application.AgentProfile
	versions     map[string][]application.ProfileVersion // agent -> oldest first
	traces       []application.Trace                     // oldest first
	steer        []application.SteerNote
	storePrompts *bool
}

var _ application.V2Store = (*Store)(nil)

func (s *Store) GetAgentProfile(_ context.Context, _, agentID string) (application.AgentProfile, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.v2.profiles[agentID]
	return p, ok, nil
}

func (s *Store) SaveAgentProfile(_ context.Context, _ string, p application.AgentProfile, v application.ProfileVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.v2.profiles == nil {
		s.v2.profiles, s.v2.versions = map[string]application.AgentProfile{}, map[string][]application.ProfileVersion{}
	}
	if cur, ok := s.v2.profiles[p.AgentID]; ok && cur.Version+1 != v.Version {
		return domain.ErrConflict // two editors raced: the second one must reload
	}
	s.v2.profiles[p.AgentID] = p
	s.v2.versions[p.AgentID] = append(s.v2.versions[p.AgentID], v)
	return nil
}

func (s *Store) ListProfileVersions(_ context.Context, _, agentID string) ([]application.ProfileVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.v2.versions[agentID])
	slices.Reverse(out)
	return out, nil
}

func (s *Store) GetProfileVersion(_ context.Context, _, agentID string, version int) (application.ProfileVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.v2.versions[agentID] {
		if v.Version == version {
			return v, nil
		}
	}
	return application.ProfileVersion{}, domain.ErrNotFound
}

func (s *Store) ListAgentProfiles(context.Context, string) ([]application.AgentProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]application.AgentProfile, 0, len(s.v2.profiles))
	for _, p := range s.v2.profiles {
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) AddTrace(_ context.Context, _ string, t application.Trace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.Attempt == 0 {
		n := 0
		for _, x := range s.v2.traces {
			if x.TaskID == t.TaskID {
				n++
			}
		}
		t.Attempt = n + 1
	}
	s.v2.traces = append(s.v2.traces, t)
	return nil
}

func (s *Store) AppendTraceEvent(_ context.Context, _, taskID string, e application.TraceEvent) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.v2.traces) - 1; i >= 0; i-- {
		if s.v2.traces[i].TaskID == taskID {
			t := &s.v2.traces[i]
			if len(t.Events) >= 40 {
				t.Truncated = true
				return true, nil
			}
			t.Events = append(slices.Clone(t.Events), e)
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) ListTraces(_ context.Context, _, agentID string, limit int, before time.Time) ([]application.Trace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []application.Trace{}
	for i := len(s.v2.traces) - 1; i >= 0 && len(out) < limit; i-- {
		t := s.v2.traces[i]
		if t.AgentID == agentID && (before.IsZero() || t.CreatedAt.Before(before)) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *Store) GetTrace(_ context.Context, _, agentID, id string) (application.Trace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.v2.traces {
		if t.ID == id && t.AgentID == agentID {
			return t, nil
		}
	}
	return application.Trace{}, domain.ErrNotFound
}

func (s *Store) PurgeTraces(_ context.Context, _ string, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.v2.traces)
	s.v2.traces = slices.DeleteFunc(s.v2.traces, func(t application.Trace) bool { return t.CreatedAt.Before(before) })
	return n - len(s.v2.traces), nil
}

func (s *Store) GetTraceStorePrompts(context.Context, string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.v2.storePrompts == nil || *s.v2.storePrompts, nil
}

func (s *Store) SetTraceStorePrompts(_ context.Context, _ string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.v2.storePrompts = &on
	return nil
}

func (s *Store) AddSteerNote(_ context.Context, _ string, n application.SteerNote) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.v2.steer = append(s.v2.steer, n)
	return nil
}

func (s *Store) ListSteerNotes(_ context.Context, _, taskID string) ([]application.SteerNote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []application.SteerNote{}
	for _, n := range s.v2.steer {
		if n.TaskID == taskID {
			out = append(out, n)
		}
	}
	return out, nil
}

func (s *Store) MarkSteerDelivered(_ context.Context, _ string, ids []string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.v2.steer {
		if slices.Contains(ids, s.v2.steer[i].ID) && s.v2.steer[i].DeliveredAt == nil {
			t := at
			s.v2.steer[i].DeliveredAt = &t
		}
	}
	return nil
}
