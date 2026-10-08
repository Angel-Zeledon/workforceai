package memory

import (
	"context"
	"encoding/json"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Run meta, task checkpoints and executed approvals (durable execution,
// application/durable.go). They live as long as the Store: a test simulates a
// restart by building a new Orchestrator over the same Store.

var (
	_ application.RunStore        = (*Store)(nil)
	_ application.ExecutionLedger = (*Store)(nil)
)

// Run meta and checkpoints are stored as JSON so callers never share maps or
// pointers (tool args, gate, chat link) with the store.
func (s *Store) PutRunMeta(_ context.Context, _ string, m application.RunMeta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runMeta == nil {
		s.runMeta = map[string][]byte{}
	}
	s.runMeta[m.RequestID] = b
	return nil
}

func (s *Store) GetRunMeta(_ context.Context, _, requestID string) (application.RunMeta, error) {
	s.mu.Lock()
	b, ok := s.runMeta[requestID]
	s.mu.Unlock()
	var m application.RunMeta
	if !ok {
		return m, domain.ErrNotFound
	}
	return m, json.Unmarshal(b, &m)
}

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

func (s *Store) ClaimExecution(_ context.Context, _ string, e application.ApprovalExecution) (bool, application.ApprovalExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.executions == nil {
		s.executions = map[string]application.ApprovalExecution{}
	}
	if prev, ok := s.executions[e.ApprovalID]; ok {
		return false, prev, nil
	}
	if e.Status == "" {
		e.Status = application.ExecutionClaimed
	}
	if e.ClaimedAt.IsZero() {
		e.ClaimedAt = time.Now().UTC()
	}
	s.executions[e.ApprovalID] = e
	return true, e, nil
}

func (s *Store) FinishExecution(_ context.Context, _ string, approvalID, status, holdID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.executions[approvalID]
	if !ok {
		return domain.ErrNotFound
	}
	now := time.Now().UTC()
	e.Status, e.HoldID, e.FinishedAt = status, holdID, &now
	s.executions[approvalID] = e
	return nil
}
