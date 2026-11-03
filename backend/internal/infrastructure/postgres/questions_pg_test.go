package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// "Needs your help" questions (migration 810): storage, first resolution wins, tenant isolation.
func TestTaskQuestionsStorageAndIsolation(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")
	now := time.Now().UTC().Truncate(time.Microsecond)
	q := application.Question{ID: "q1", TaskID: "t1", RequestID: "r1", AgentID: "sales", GroupKey: "project:p1", Kind: "data",
		WhatHappened: "No hay teléfono", Need: "¿Cómo lo contacto?", Options: []string{"Correo", "Omitir"}, Status: application.QuestionOpen,
		AskedFor: "ana", CreatedAt: now}
	if err := st.CreateQuestion(ctx, "org-a", q); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetQuestion(ctx, "org-a", "q1")
	if err != nil || got.Need != q.Need || len(got.Options) != 2 || got.Status != "open" {
		t.Fatalf("get = %+v %v", got, err)
	}
	if _, err := st.GetQuestion(ctx, "org-b", "q1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other org reads it: %v", err)
	}
	q.Status, q.ResolvedAt, q.ResolvedBy, q.AnswerKind, q.Answer = application.QuestionAnswered, &now, "ana", "option", "Correo"
	if err := st.ResolveQuestion(ctx, "org-a", q); err != nil {
		t.Fatal(err)
	}
	if err := st.ResolveQuestion(ctx, "org-a", q); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second resolution: %v", err)
	}
	open, _ := st.ListQuestions(ctx, "org-a", application.QuestionOpen)
	byTask, _ := st.ListQuestionsByTask(ctx, "org-a", "t1")
	if len(open) != 0 || len(byTask) != 1 || byTask[0].Answer != "Correo" {
		t.Fatalf("open=%d byTask=%+v", len(open), byTask)
	}
}
