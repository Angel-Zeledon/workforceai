package projects_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
)

// helpProject launches the generic template; its first node asks a person once.
func helpProject(t *testing.T) (*env, string, *application.Questions) {
	t.Helper()
	rt := newRT()
	rt.help = func(in application.RunTaskRequest) *application.HelpRequest {
		if in.Task.Title == failingNode && in.HelpAllowed && len(in.HelpAnswers) == 0 {
			return &application.HelpRequest{Kind: "data", WhatHappened: "Falta un dato", Need: "¿Qué periodo uso?", Options: []string{"Q3", "Q4"}}
		}
		return nil
	}
	e := newEnv(t, rt, nil, nil)
	rec := &application.Recorder{OrgID: domain.DemoOrgID, Store: e.store, Pub: nopPub{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	qs := &application.Questions{Store: e.store, Rec: rec, NoticeWindow: time.Millisecond}
	e.orch.SetQuestions(qs)
	e.orch.SetRequestOwner(e.svc)
	id := e.draft("tpl-generic", 0)
	e.launch(id, budgetBig)
	e.waitFor("node needs input", func() bool {
		return nodeByTitle(e.detail(id), failingNode).State == projects.StateNeedsInput
	})
	return e, id, qs
}

func openQ(t *testing.T, e *env) application.Question {
	t.Helper()
	qs, _ := e.store.ListQuestions(context.Background(), domain.DemoOrgID, application.QuestionOpen)
	if len(qs) != 1 {
		t.Fatalf("open questions = %d", len(qs))
	}
	return qs[0]
}

func TestNodeNeedsInputState(t *testing.T) {
	e, id, _ := helpProject(t)
	d := e.detail(id)
	if d.Project.NeedsInput != 1 || d.Project.Status != projects.StatusWaitingHuman {
		t.Fatalf("project: needs_input=%d status=%s", d.Project.NeedsInput, d.Project.Status)
	}
	q := openQ(t, e)
	if q.ProjectID != id || q.GroupKey != "project:"+id {
		t.Fatalf("question group: %+v", q)
	}
	if _, err := e.orch.AnswerQuestion(e.ctx, q.ID, application.Resolution{Kind: application.AnswerOption, Option: "Q4"}, false); err != nil {
		t.Fatal(err)
	}
	e.approveAll(id, func(d projects.Detail) bool { return d.Project.Status == projects.StatusDone })
}

func TestProjectCancelClosesQuestions(t *testing.T) {
	e, id, _ := helpProject(t)
	q := openQ(t, e)
	if _, err := e.svc.Control(e.ctx, id, "cancel"); err != nil {
		t.Fatal(err)
	}
	e.waitFor("question closed", func() bool {
		cur, _ := e.store.GetQuestion(context.Background(), domain.DemoOrgID, q.ID)
		return cur.Status == application.QuestionCancelled
	})
	e.waitFor("node stopped", func() bool {
		st := nodeByTitle(e.detail(id), failingNode).State
		return st == projects.StateCancelled || st == projects.StateBlocked
	})
	if e.detail(id).Project.Status != projects.StatusCancelled {
		t.Fatal("the project stays cancelled")
	}
}

func TestSkipImpactUsesProjectObjectives(t *testing.T) {
	e, _, _ := helpProject(t)
	q := openQ(t, e)
	imp, err := e.orch.QuestionImpact(e.ctx, q.ID)
	if err != nil || imp.Objectives < 1 {
		t.Fatalf("impact = %+v %v", imp, err)
	}
	if imp.Allowed && len(imp.ObjectivesLost) > 0 && imp.ObjectivesLost[0].Title == "" {
		t.Fatal("lost objectives are named")
	}
	_, _ = e.orch.AnswerQuestion(e.ctx, q.ID, application.Resolution{Kind: application.AnswerText, Text: "Q4"}, false)
}
