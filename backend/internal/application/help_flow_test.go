package application_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// helpRuntime: the task titled ask asks a person (while it has no answer); every other task just finishes.
type helpLog struct {
	mu    sync.Mutex
	calls map[string]int
	ins   map[string][]application.RunTaskRequest
}

func (l *helpLog) n(title string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls[title]
}

func (l *helpLog) last(title string) application.RunTaskRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	in := l.ins[title]
	return in[len(in)-1]
}

func helpRuntime(plan []application.PlannedTask, ask string, always bool, log *helpLog) *fakeRuntime {
	log.calls, log.ins = map[string]int{}, map[string][]application.RunTaskRequest{}
	return &fakeRuntime{
		plan: func(application.PlanRequest) (application.PlanResponse, error) {
			return application.PlanResponse{Tasks: plan}, nil
		},
		runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
			log.mu.Lock()
			log.calls[in.Task.Title]++
			log.ins[in.Task.Title] = append(log.ins[in.Task.Title], in)
			log.mu.Unlock()
			r := okResult("hecho: " + in.Task.Title)
			if in.Task.Title == ask && in.HelpAllowed && (always || len(in.HelpAnswers) == 0) {
				r.HelpRequest = &application.HelpRequest{Kind: "data", WhatHappened: "No hay teléfono", Need: "¿Cómo contacto al cliente?",
					Options: []string{"Usa el correo", "Omite la llamada"}}
			}
			return r, nil
		},
	}
}

func withQuestions(h *harness) *application.Questions {
	rec := &application.Recorder{OrgID: domain.DemoOrgID, Store: h.store, Pub: h.pub, Log: discardLog()}
	qs := &application.Questions{Store: h.store, Rec: rec, NoticeWindow: time.Millisecond}
	h.orch.SetQuestions(qs)
	return qs
}

func (h *harness) openQuestion() application.Question {
	h.t.Helper()
	var q application.Question
	h.waitFor("open question", func() bool {
		qs, _ := h.store.ListQuestions(context.Background(), domain.DemoOrgID, application.QuestionOpen)
		if len(qs) == 0 {
			return false
		}
		q = qs[0]
		t, err := h.store.GetTask(context.Background(), domain.DemoOrgID, q.TaskID)
		return err == nil && t.Status == domain.TaskNeedsInput
	})
	return q
}

func helpPlan() []application.PlannedTask {
	return []application.PlannedTask{
		{Key: "a", Title: "Llamar", AgentID: "sales"},
		{Key: "b", Title: "Resumir", AgentID: "analyst", DependsOn: []string{"a"}},
		{Key: "c", Title: "Independiente", AgentID: "analyst"},
	}
}

func TestAgentAsksAndContinuesWithAnswer(t *testing.T) {
	var log helpLog
	h := newHarness(t, helpRuntime(helpPlan(), "Llamar", false, &log), func(c *application.Config) { c.MaxParallel = 1 })
	withQuestions(h)
	ctx := application.WithActor(context.Background(), "ana")
	reqID, _ := h.orch.Submit(ctx, "contactar clientes")
	q := h.openQuestion()
	if q.AskedFor != "ana" || q.Need != "¿Cómo contacto al cliente?" || len(q.Options) != 2 || q.GroupKey != "request:"+reqID {
		t.Fatalf("question: %+v", q)
	}
	// the independent branch keeps running with MaxParallel=1 (the waiting task gave its slot back)
	h.waitFor("independent task done", func() bool { return h.tasks(reqID)["Independiente"].Status == domain.TaskDone })
	time.Sleep(30 * time.Millisecond)
	if log.n("Llamar") != 1 || log.n("Resumir") != 0 {
		t.Fatalf("no model calls while waiting: Llamar=%d Resumir=%d", log.n("Llamar"), log.n("Resumir"))
	}
	if h.pub.count(domain.EvTaskNeedsInput) != 1 || h.pub.count(domain.EvHelpNotice) < 1 {
		t.Fatal("the task must announce it needs help and notify")
	}
	if _, err := h.orch.AnswerQuestion(ctx, q.ID, application.Resolution{Kind: application.AnswerOption, Option: "Inventada"}, false); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown option: %v", err)
	}
	if _, err := h.orch.AnswerQuestion(ctx, q.ID, application.Resolution{Kind: application.AnswerOption, Option: "Usa el correo"}, false); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	ts := h.tasks(reqID)
	for _, title := range []string{"Llamar", "Resumir", "Independiente"} {
		if ts[title].Status != domain.TaskDone {
			t.Fatalf("%s = %s", title, ts[title].Status)
		}
	}
	in := log.last("Llamar")
	if log.n("Llamar") != 2 || len(in.HelpAnswers) != 1 || in.HelpAnswers[0].Answer != "Usa el correo" || in.HelpAnswers[0].By != "ana" {
		t.Fatalf("second call: calls=%d answers=%+v", log.n("Llamar"), in.HelpAnswers)
	}
	if r := h.request(reqID); r.Status != domain.RequestDone {
		t.Fatalf("request = %s", r.Status)
	}
	if !h.hasAudit("question.answered") {
		t.Fatal("who answered must stay in the history")
	}
}

func TestReassignAnswerRunsWithOtherAgent(t *testing.T) {
	var log helpLog
	h := newHarness(t, helpRuntime(helpPlan()[:1], "Llamar", false, &log), nil)
	withQuestions(h)
	reqID, _ := h.orch.Submit(context.Background(), "contactar")
	q := h.openQuestion()
	if _, err := h.orch.AnswerQuestion(context.Background(), q.ID, application.Resolution{Kind: application.AnswerReassign, AgentID: "sales"}, false); err == nil {
		t.Fatal("reassigning to the same agent must be refused")
	}
	if _, err := h.orch.AnswerQuestion(context.Background(), q.ID, application.Resolution{Kind: application.AnswerReassign, AgentID: "analyst"}, false); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	task := h.tasks(reqID)["Llamar"]
	if task.Status != domain.TaskDone || task.AgentID != "analyst" || log.last("Llamar").Agent.ID != "analyst" {
		t.Fatalf("task %s agent %s last call by %s", task.Status, task.AgentID, log.last("Llamar").Agent.ID)
	}
}

func TestFourthQuestionFailsTask(t *testing.T) {
	var log helpLog
	h := newHarness(t, helpRuntime(helpPlan()[:1], "Llamar", true, &log), nil)
	withQuestions(h)
	reqID, _ := h.orch.Submit(context.Background(), "contactar")
	for i := 0; i < application.MaxQuestionsPerTask; i++ {
		q := h.openQuestion()
		if _, err := h.orch.AnswerQuestion(context.Background(), q.ID, application.Resolution{Kind: application.AnswerText, Text: "sigue"}, false); err != nil {
			t.Fatal(err)
		}
	}
	h.orch.Wait()
	if st := h.tasks(reqID)["Llamar"].Status; st != domain.TaskFailed {
		t.Fatalf("task = %s, want failed after %d questions", st, application.MaxQuestionsPerTask)
	}
}

func TestNoQuestionsServiceKeepsOldBehavior(t *testing.T) {
	var log helpLog
	h := newHarness(t, helpRuntime(helpPlan()[:1], "Llamar", true, &log), nil)
	reqID, _ := h.orch.Submit(context.Background(), "contactar")
	h.orch.Wait()
	if h.tasks(reqID)["Llamar"].Status != domain.TaskDone || log.last("Llamar").HelpAllowed {
		t.Fatal("without the questions service nothing changes and the agent is not offered to ask")
	}
}

func TestOnlyPeopleAnswer(t *testing.T) {
	var log helpLog
	h := newHarness(t, helpRuntime(helpPlan()[:1], "Llamar", false, &log), nil)
	withQuestions(h)
	h.orch.Submit(context.Background(), "contactar")
	q := h.openQuestion()
	actx := application.WithActor(context.Background(), "agent:sales")
	if _, err := h.orch.AnswerQuestion(actx, q.ID, application.Resolution{Kind: application.AnswerText, Text: "x"}, false); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("an agent answered: %v", err)
	}
	_, _ = h.orch.AnswerQuestion(context.Background(), q.ID, application.Resolution{Kind: application.AnswerText, Text: "ok"}, false)
	h.orch.Wait()
}

// ---- skip with sense ----

func skipPlan() []application.PlannedTask {
	return []application.PlannedTask{
		{Key: "a", Title: "Llamar", AgentID: "sales"},
		{Key: "b", Title: "Registrar", AgentID: "analyst", DependsOn: []string{"a"}},
		{Key: "c", Title: "Informe", AgentID: "analyst"},
	}
}

func TestSkipMarksDependentsOmitted(t *testing.T) {
	var log helpLog
	h := newHarness(t, helpRuntime(skipPlan(), "Llamar", false, &log), nil)
	withQuestions(h)
	reqID, _ := h.orch.Submit(context.Background(), "contactar")
	q := h.openQuestion()
	imp, err := h.orch.QuestionImpact(context.Background(), q.ID)
	if err != nil || !imp.Allowed || len(imp.Skipped) != 1 || imp.Skipped[0].Title != "Registrar" {
		t.Fatalf("impact = %+v %v", imp, err)
	}
	if _, err := h.orch.AnswerQuestion(context.Background(), q.ID, application.Resolution{Kind: application.AnswerSkip}, false); !errors.Is(err, domain.ErrConflict) ||
		!strings.Contains(err.Error(), "skip_needs_confirm") {
		t.Fatalf("skip without confirm: %v", err)
	}
	if _, err := h.orch.AnswerQuestion(context.Background(), q.ID, application.Resolution{Kind: application.AnswerSkip, Text: "no hace falta"}, true); err != nil {
		t.Fatal(err)
	}
	h.orch.Wait()
	ts := h.tasks(reqID)
	if ts["Llamar"].Status != domain.TaskDone || ts["Llamar"].Output.Metrics["skipped"] != true {
		t.Fatalf("Llamar = %s %+v", ts["Llamar"].Status, ts["Llamar"].Output)
	}
	if ts["Registrar"].Status != domain.TaskBlocked || ts["Registrar"].Output == nil || ts["Registrar"].Output.Metrics["omitted"] != true {
		t.Fatalf("Registrar = %s %+v", ts["Registrar"].Status, ts["Registrar"].Output)
	}
	if log.n("Registrar") != 0 || ts["Informe"].Status != domain.TaskDone {
		t.Fatal("the omitted dependent never runs; the rest finishes")
	}
}

func TestSkipRefusedWhenAllObjectivesLost(t *testing.T) {
	var log helpLog
	plan := []application.PlannedTask{{Key: "a", Title: "Llamar", AgentID: "sales"}, {Key: "b", Title: "Registrar", AgentID: "analyst", DependsOn: []string{"a"}}}
	h := newHarness(t, helpRuntime(plan, "Llamar", false, &log), nil)
	withQuestions(h)
	h.orch.Submit(context.Background(), "contactar")
	q := h.openQuestion()
	imp, _ := h.orch.QuestionImpact(context.Background(), q.ID)
	if imp.Allowed || !imp.AllLost {
		t.Fatalf("impact = %+v", imp)
	}
	if _, err := h.orch.AnswerQuestion(context.Background(), q.ID, application.Resolution{Kind: application.AnswerSkip}, true); !errors.Is(err, domain.ErrConflict) ||
		!strings.Contains(err.Error(), "skip_blocks_result") {
		t.Fatalf("skip of the only path: %v", err)
	}
	_, _ = h.orch.AnswerQuestion(context.Background(), q.ID, application.Resolution{Kind: application.AnswerText, Text: "usa el correo"}, false)
	h.orch.Wait()
}

func TestSkipImpactSomeObjectivesLost(t *testing.T) {
	var log helpLog
	h := newHarness(t, helpRuntime(skipPlan(), "Llamar", false, &log), nil)
	withQuestions(h)
	reqID, _ := h.orch.Submit(context.Background(), "contactar")
	q := h.openQuestion()
	byTitle := h.tasks(reqID)
	objOf := map[string]application.ObjectiveRef{
		byTitle["Llamar"].ID:    {ID: "o1", Title: "Contactar"},
		byTitle["Registrar"].ID: {ID: "o1", Title: "Contactar"},
		byTitle["Informe"].ID:   {ID: "o2", Title: "Informar"},
	}
	h.orch.SetRequestOwner(fixedOwner{objOf})
	imp, err := h.orch.QuestionImpact(context.Background(), q.ID)
	if err != nil || !imp.Allowed || imp.AllLost || imp.Objectives != 2 || len(imp.ObjectivesLost) != 1 || imp.ObjectivesLost[0].Title != "Contactar" {
		t.Fatalf("impact = %+v %v", imp, err)
	}
	_, _ = h.orch.AnswerQuestion(context.Background(), q.ID, application.Resolution{Kind: application.AnswerText, Text: "ok"}, false)
	h.orch.Wait()
}

type fixedOwner struct {
	m map[string]application.ObjectiveRef
}

func (f fixedOwner) ObjectivesOfRequest(context.Context, string, string) (map[string]application.ObjectiveRef, bool) {
	return f.m, true
}
func (fixedOwner) GroupOfRequest(context.Context, string, string) (string, string) {
	return "p1", "project:p1"
}

func TestCancelQuestionsStopsWaitingTask(t *testing.T) {
	var log helpLog
	h := newHarness(t, helpRuntime(helpPlan()[:1], "Llamar", false, &log), nil)
	withQuestions(h)
	reqID, _ := h.orch.Submit(context.Background(), "contactar")
	h.openQuestion()
	if n := h.orch.CancelQuestions(context.Background(), reqID, "proyecto cancelado"); n != 1 {
		t.Fatalf("cancelled = %d", n)
	}
	h.orch.Wait()
	if st := h.tasks(reqID)["Llamar"].Status; st != domain.TaskBlocked || log.n("Llamar") != 1 {
		t.Fatalf("task = %s calls = %d", st, log.n("Llamar"))
	}
}

func TestQuestionSurvivesRestart(t *testing.T) {
	store, pub := newStore(t), &capture{}
	var log1, log2 helpLog
	p1 := startProcess(t, store, pub, helpRuntime(helpPlan()[:2], "Llamar", false, &log1), nil)
	withQuestions(p1.harness)
	reqID, _ := p1.orch.Submit(application.WithActor(context.Background(), "ana"), "contactar")
	q := p1.openQuestion()
	p1.stop()

	p2 := startProcess(t, store, pub, helpRuntime(helpPlan()[:2], "Llamar", false, &log2), nil)
	withQuestions(p2.harness)
	if n, err := p2.orch.Recover(context.Background(), nil); err != nil || n != 1 {
		t.Fatalf("recover = %d, %v", n, err)
	}
	time.Sleep(50 * time.Millisecond)
	if log2.n("Llamar") != 0 {
		t.Fatal("after the restart the task waits for the same question: no new model call, no second question")
	}
	if qs, _ := store.ListQuestions(context.Background(), domain.DemoOrgID, ""); len(qs) != 1 {
		t.Fatalf("questions = %d, want 1", len(qs))
	}
	if _, err := p2.orch.AnswerQuestion(context.Background(), q.ID, application.Resolution{Kind: application.AnswerText, Text: "usa el correo"}, false); err != nil {
		t.Fatal(err)
	}
	p2.orch.Wait()
	if r := p2.request(reqID); r.Status != domain.RequestDone || log2.n("Llamar") != 1 || log2.last("Llamar").HelpAnswers[0].Answer != "usa el correo" {
		t.Fatalf("request = %s calls = %d", r.Status, log2.n("Llamar"))
	}
}

// Part C: the files a task wrote stay with its result, so its project node can show them.
func TestTaskKeepsTheFilesItWrote(t *testing.T) {
	var h *harness
	rt := &fakeRuntime{plan: singleTaskPlan(), runTask: func(in application.RunTaskRequest) (application.RunTaskResponse, error) {
		h.orch.NoteTaskFile(in.Task.ID, "write", "/Agents/analyst/informe.docx")
		h.orch.NoteTaskFile(in.Task.ID, "write", "/Agents/analyst/datos.xlsx")
		h.orch.NoteTaskFile(in.Task.ID, "read", "/Shared/ignorado.txt")
		return okResult("listo"), nil
	}}
	h = newHarness(t, rt, nil)
	reqID, _ := h.orch.Submit(context.Background(), "analiza")
	h.orch.Wait()
	out := h.tasks(reqID)["Analizar"].Output
	if out == nil || strings.Join(out.Files, ",") != "/Agents/analyst/informe.docx,/Agents/analyst/datos.xlsx" {
		t.Fatalf("files = %+v", out)
	}
}
