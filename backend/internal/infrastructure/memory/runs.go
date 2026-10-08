package memory

import (
	"context"
	"encoding/json"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Run meta and task checkpoints (durable execution, application/durable.go).
// They live as long as the Store: a test simulates a restart by building a new
// Orchestrator over the same Store.

var _ application.RunStore = (*Store)(nil)

func (s *Store) PutRunMeta(_ context.Context, _ string, m application.RunMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runMeta == nil {
		s.runMeta = map[string]application.RunMeta{}
	}
	m.RemovedTaskIDs = append([]string{}, m.RemovedTaskIDs...)
	s.runMeta[m.RequestID] = m
	return nil
}

func (s *Store) GetRunMeta(_ context.Context, _, requestID string) (application.RunMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.runMeta[requestID]
	if !ok {
		return m, domain.ErrNotFound
	}
	m.RemovedTaskIDs = append([]string{}, m.RemovedTaskIDs...)
	return m, nil
}

// Checkpoints are stored as JSON so callers never share maps (tool args) with the store.
func (s *Store) PutCheckpoint(_ context.Context, _ string, c application.TaskCheckpoint) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checkpoints == nil {
		s.checkpoints = map[string][]byte{}
	}
	s.checkpoints[c.TaskID] = b
	return nil
}

func (s *Store) GetCheckpoint(_ context.Context, _, taskID string) (application.TaskCheckpoint, error) {
	s.mu.Lock()
	b, ok := s.checkpoints[taskID]
	s.mu.Unlock()
	var c application.TaskCheckpoint
	if !ok {
		return c, domain.ErrNotFound
	}
	return c, json.Unmarshal(b, &c)
}

func (s *Store) DeleteCheckpoint(_ context.Context, _, taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.checkpoints, taskID)
	return nil
}
