package memory

import (
	"context"
	"fmt"
	"slices"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.QuestionStore = (*Store)(nil)

func cloneQuestion(q application.Question) application.Question {
	q.Options = append([]string{}, q.Options...)
	if q.ResolvedAt != nil {
		t := *q.ResolvedAt
		q.ResolvedAt = &t
	}
	return q
}

func (s *Store) CreateQuestion(_ context.Context, orgID string, q application.Question) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.questions == nil {
		s.questions = map[string][]application.Question{}
	}
	s.questions[orgID] = append(s.questions[orgID], cloneQuestion(q))
	return nil
}

func (s *Store) findQuestion(orgID, id string) int {
	for i, q := range s.questions[orgID] {
		if q.ID == id {
			return i
		}
	}
	return -1
}

func (s *Store) GetQuestion(_ context.Context, orgID, id string) (application.Question, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.findQuestion(orgID, id)
	if i < 0 {
		return application.Question{}, domain.ErrNotFound
	}
	return cloneQuestion(s.questions[orgID][i]), nil
}

func (s *Store) ResolveQuestion(_ context.Context, orgID string, q application.Question) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.findQuestion(orgID, q.ID)
	if i < 0 {
		return domain.ErrNotFound
	}
	if cur := s.questions[orgID][i]; cur.Status != application.QuestionOpen {
		return fmt.Errorf("%w: the question is already %s", domain.ErrConflict, cur.Status)
	}
	s.questions[orgID][i] = cloneQuestion(q)
	return nil
}

func (s *Store) SetQuestionReminders(_ context.Context, orgID, id string, n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.findQuestion(orgID, id); i >= 0 {
		s.questions[orgID][i].Reminders = n
	}
	return nil
}

func (s *Store) ListQuestions(_ context.Context, orgID, status string) ([]application.Question, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []application.Question{}
	for _, q := range s.questions[orgID] {
		if status == "" || q.Status == status {
			out = append(out, cloneQuestion(q))
		}
	}
	slices.Reverse(out)
	return out, nil
}

func (s *Store) ListQuestionsByTask(_ context.Context, orgID, taskID string) ([]application.Question, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []application.Question{}
	for _, q := range s.questions[orgID] {
		if q.TaskID == taskID {
			out = append(out, cloneQuestion(q))
		}
	}
	return out, nil
}
