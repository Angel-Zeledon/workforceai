package postgres

import (
	"context"
	"fmt"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

var _ application.QuestionStore = (*Store)(nil)

const questionCols = `id, task_id, request_id, agent_id, project_id, group_key, kind, what_happened, need, options, status, asked_for,
	created_at, resolved_at, resolved_by, answer_kind, answer, file_path, reassign_to, reminders`

func scanQuestion(r scanner) (application.Question, error) {
	var q application.Question
	err := r.Scan(&q.ID, &q.TaskID, &q.RequestID, &q.AgentID, &q.ProjectID, &q.GroupKey, &q.Kind, &q.WhatHappened, &q.Need, &q.Options,
		&q.Status, &q.AskedFor, &q.CreatedAt, &q.ResolvedAt, &q.ResolvedBy, &q.AnswerKind, &q.Answer, &q.FilePath, &q.ReassignTo, &q.Reminders)
	if q.Options == nil {
		q.Options = []string{}
	}
	return q, err
}

func (s *Store) CreateQuestion(ctx context.Context, orgID string, q application.Question) error {
	opts := q.Options
	if opts == nil {
		opts = []string{}
	}
	return s.exec(ctx, orgID, `INSERT INTO task_questions (id, org_id, task_id, request_id, agent_id, project_id, group_key, kind,
		what_happened, need, options, status, asked_for, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		q.ID, orgID, q.TaskID, q.RequestID, q.AgentID, q.ProjectID, q.GroupKey, q.Kind, q.WhatHappened, q.Need, opts, q.Status, q.AskedFor, q.CreatedAt)
}

func (s *Store) GetQuestion(ctx context.Context, orgID, id string) (application.Question, error) {
	q, err := one(ctx, s, orgID, scanQuestion, `SELECT `+questionCols+` FROM task_questions WHERE org_id=$1 AND id=$2`, orgID, id)
	return q, mapErr(err)
}

// ResolveQuestion only matches an open row: the first resolution wins, the others are a conflict.
func (s *Store) ResolveQuestion(ctx context.Context, orgID string, q application.Question) error {
	tag, err := s.execTag(ctx, orgID, `UPDATE task_questions SET status=$3, resolved_at=$4, resolved_by=$5, answer_kind=$6, answer=$7,
		file_path=$8, reassign_to=$9 WHERE org_id=$1 AND id=$2 AND status='open'`,
		orgID, q.ID, q.Status, q.ResolvedAt, q.ResolvedBy, q.AnswerKind, q.Answer, q.FilePath, q.ReassignTo)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, gerr := s.GetQuestion(ctx, orgID, q.ID); gerr != nil {
			return gerr
		}
		return fmt.Errorf("%w: the question is no longer open", domain.ErrConflict)
	}
	return nil
}

func (s *Store) SetQuestionReminders(ctx context.Context, orgID, id string, n int) error {
	return s.exec(ctx, orgID, `UPDATE task_questions SET reminders=$3 WHERE org_id=$1 AND id=$2`, orgID, id, n)
}

func (s *Store) ListQuestions(ctx context.Context, orgID, status string) ([]application.Question, error) {
	return many(ctx, s, orgID, scanQuestion, `SELECT `+questionCols+` FROM task_questions
		WHERE org_id=$1 AND ($2 = '' OR status=$2) ORDER BY created_at DESC, id DESC LIMIT 2000`, orgID, status)
}

func (s *Store) ListQuestionsByTask(ctx context.Context, orgID, taskID string) ([]application.Question, error) {
	return many(ctx, s, orgID, scanQuestion, `SELECT `+questionCols+` FROM task_questions
		WHERE org_id=$1 AND task_id=$2 ORDER BY created_at, id`, orgID, taskID)
}
