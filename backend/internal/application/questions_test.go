package application_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
)

func newQuestions(t *testing.T, window time.Duration) (*application.Questions, *capture, *memory.Store) {
	t.Helper()
	store := memory.New()
	pub := &capture{}
	rec := &application.Recorder{OrgID: domain.DemoOrgID, Store: store, Pub: pub, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return &application.Questions{Store: store, Rec: rec, NoticeWindow: window, ReminderBase: time.Hour}, pub, store
}

func TestNextReminderCadence(t *testing.T) {
	c := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	want := []time.Duration{day, 2 * day, 3 * day, 4 * day, 11 * day, 18 * day}
	for i, w := range want {
		if got := application.NextReminder(c, i+1, 0).Sub(c); got != w {
			t.Fatalf("reminder %d at %v, want %v", i+1, got, w)
		}
	}
}

func TestAnswerWakesWaiter(t *testing.T) {
	qs, pub, _ := newQuestions(t, time.Hour)
	ctx := context.Background()
	q, ch, err := qs.Ask(ctx, application.Question{TaskID: "t1", RequestID: "r1", AgentID: "sales", Kind: "data",
		WhatHappened: "No hay teléfono", Need: "¿Cómo lo contacto?", Options: []string{"Correo", "", "Omitir"}})
	if err != nil {
		t.Fatal(err)
	}
	if q.Status != application.QuestionOpen || len(q.Options) != 2 || q.GroupKey != "request:r1" {
		t.Fatalf("asked: %+v", q)
	}
	if pub.count(domain.EvQuestionAsked) != 1 || pub.count(domain.EvHelpNotice) != 1 {
		t.Fatal("asking must announce the question and notify once")
	}
	done := make(chan application.Question, 1)
	go func() {
		res, _ := qs.Wait(ctx, ch, q)
		done <- res
	}()
	if _, err := qs.Resolve(ctx, q.ID, "ana", application.Resolution{Kind: application.AnswerOption, Option: "Correo"}); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-done:
		if res.Status != application.QuestionAnswered || res.Answer != "Correo" || res.ResolvedBy != "ana" {
			t.Fatalf("resolved: %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiter was not woken")
	}
}

func TestAnswerTwiceConflicts(t *testing.T) {
	qs, _, _ := newQuestions(t, time.Hour)
	ctx := context.Background()
	q, _, _ := qs.Ask(ctx, application.Question{TaskID: "t1", RequestID: "r1", Need: "x"})
	if _, err := qs.Resolve(ctx, q.ID, "ana", application.Resolution{Kind: application.AnswerText, Text: "sí"}); err != nil {
		t.Fatal(err)
	}
	if _, err := qs.Resolve(ctx, q.ID, "bob", application.Resolution{Kind: application.AnswerText, Text: "no"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second answer: %v, want conflict", err)
	}
	if _, err := qs.Resolve(ctx, "nope", "bob", application.Resolution{Kind: application.AnswerText, Text: "no"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown question: %v", err)
	}
}

func TestAttachAnsweredReturnsAtOnce(t *testing.T) {
	qs, _, _ := newQuestions(t, time.Hour)
	ctx := context.Background()
	q, _, _ := qs.Ask(ctx, application.Question{TaskID: "t1", RequestID: "r1", Need: "x"})
	if _, ok := qs.OpenFor(ctx, "t1"); !ok {
		t.Fatal("the open question must be found by task")
	}
	_, _ = qs.Resolve(ctx, q.ID, "ana", application.Resolution{Kind: application.AnswerText, Text: "sí"})
	_, ch, err := qs.Attach(ctx, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := qs.Wait(ctx, ch, q)
	if err != nil || res.Answer != "sí" {
		t.Fatalf("attach after answer: %+v %v", res, err)
	}
	if _, ok := qs.OpenFor(ctx, "t1"); ok {
		t.Fatal("no open question after the answer")
	}
}

func TestHelpNoticesCoalesceWithinWindow(t *testing.T) {
	qs, pub, _ := newQuestions(t, 80*time.Millisecond)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, _, _ = qs.Ask(ctx, application.Question{TaskID: "t", RequestID: "r1", GroupKey: "batch:b1", AskedFor: "ana", Need: "x"})
	}
	_, _, _ = qs.Ask(ctx, application.Question{TaskID: "t", RequestID: "r2", GroupKey: "batch:b2", AskedFor: "ana", Need: "y"})
	if n := pub.count(domain.EvHelpNotice); n != 2 {
		t.Fatalf("notices right away = %d, want 2 (one per group)", n)
	}
	time.Sleep(250 * time.Millisecond)
	if n := pub.count(domain.EvHelpNotice); n != 3 {
		t.Fatalf("notices after the window = %d, want 3 (the 2 gathered ones in one)", n)
	}
	var last map[string]any
	pub.mu.Lock()
	for _, e := range pub.events {
		if e.Type == domain.EvHelpNotice {
			last, _ = e.Payload.(map[string]any)
		}
	}
	pub.mu.Unlock()
	if last["count"] != 2 || last["group"] != "batch:b1" || last["asked_for"] != "ana" {
		t.Fatalf("summary notice: %+v", last)
	}
}

func TestOpenQuestionReminds(t *testing.T) {
	qs, pub, _ := newQuestions(t, time.Millisecond)
	qs.ReminderBase = 30 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	q, ch, _ := qs.Ask(ctx, application.Question{TaskID: "t1", RequestID: "r1", Need: "x"})
	if _, err := qs.Wait(ctx, ch, q); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait: %v", err)
	}
	if pub.count(domain.EvQuestionReminder) < 2 {
		t.Fatalf("reminders = %d", pub.count(domain.EvQuestionReminder))
	}
	cur, _ := qs.Store.GetQuestion(context.Background(), domain.DemoOrgID, q.ID)
	if cur.Status != application.QuestionOpen || cur.Reminders < 2 {
		t.Fatalf("a question never closes by itself: %+v", cur)
	}
}
