package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aiworkforce/backend/internal/domain"
)

// The "needs your help" flow of a task (questions.go has the questions themselves).
//
//	runTask -> helpGate (an open question? wait for it, giving the scheduler slot back) -> runTaskOnce
//	runTaskOnce -> the runtime returned help_request -> askForHelp (question stored, task needs_input) -> runTask loops
//
// Only the asking task and what depends on it wait: the scheduler keeps running every independent branch.

// ObjectiveRef is one objective of the work a request belongs to (a project objective, or the request itself).
type ObjectiveRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// HelpTaskRef names a task in the skip analysis.
type HelpTaskRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// WorkOwner is the optional view of the work a request belongs to (implemented by projects): the objective of
// each task and the group its questions are noticed by. Without it a request is one objective and its own group.
type WorkOwner interface {
	ObjectivesOfRequest(ctx context.Context, org, requestID string) (map[string]ObjectiveRef, bool)
	GroupOfRequest(ctx context.Context, org, requestID string) (projectID, groupKey string)
}

type helpState struct {
	questions *Questions
	owner     WorkOwner
}

// SetQuestions turns the "needs your help" flow on: the agents may ask and the tasks wait for the answers.
func (o *Orchestrator) SetQuestions(q *Questions) { o.help.questions = q }

// Questions returns the questions service (nil when the flow is off).
func (o *Orchestrator) Questions() *Questions { return o.help.questions }

// SetRequestOwner installs the objectives and groups of project requests.
func (o *Orchestrator) SetRequestOwner(r WorkOwner) { o.help.owner = r }

// helpContext tells the runtime the agent may ask, with the answers already given to this task.
func (o *Orchestrator) helpContext(ctx context.Context, in *RunTaskRequest) {
	qs := o.help.questions
	if qs == nil {
		return
	}
	in.HelpAllowed = true
	in.HelpAnswers = nil
	list, err := qs.Store.ListQuestionsByTask(ctx, o.org(ctx), in.Task.ID)
	if err != nil {
		return
	}
	for _, q := range list {
		switch q.Status {
		case QuestionAnswered:
			ans := q.Answer
			if q.FilePath != "" {
				ans = strings.TrimSpace(ans + " (archivo en el drive: " + q.FilePath + ")")
			}
			in.HelpAnswers = append(in.HelpAnswers, HelpAnswer{Question: q.Need, Answer: ans, By: q.ResolvedBy})
		case QuestionReassigned:
			ans := "Una persona te pasó esta tarea para que lo resuelvas tú con tu criterio; si de verdad no puedes, vuelve a preguntar."
			if q.Answer != "" {
				ans += " Nota: " + q.Answer
			}
			in.HelpAnswers = append(in.HelpAnswers, HelpAnswer{Question: q.Need, Answer: ans, By: q.ResolvedBy})
		}
	}
}

func (o *Orchestrator) helpGroup(ctx context.Context, requestID string) (string, string) {
	if o.help.owner != nil {
		if p, g := o.help.owner.GroupOfRequest(ctx, o.org(ctx), requestID); g != "" {
			return p, g
		}
	}
	return "", "request:" + requestID
}

// askForHelp stores the question of the agent and puts the task in needs_input. asked=false means the task failed
// instead (it already asked MaxQuestionsPerTask times, or the question could not be stored).
func (o *Orchestrator) askForHelp(ctx context.Context, rs *run, t domain.Task, agent domain.Agent, hr HelpRequest) (Outcome, bool) {
	qs := o.help.questions
	org := o.org(ctx)
	prev, _ := qs.Store.ListQuestionsByTask(ctx, org, t.ID)
	n := 0
	for _, q := range prev {
		if q.Status != QuestionCancelled {
			n++
		}
	}
	if n >= MaxQuestionsPerTask {
		o.failTask(ctx, rs, t, fmt.Errorf("el agente pidió ayuda %d veces y sigue sin poder avanzar (%s); reintenta, omite o reasigna la tarea", n, truncate(hr.Need, 160)))
		return OutcomeFailed, false
	}
	q := Question{TaskID: t.ID, RequestID: t.RequestID, AgentID: t.AgentID, Kind: hr.Kind, WhatHappened: hr.WhatHappened, Need: hr.Need,
		Options: hr.Options, AskedFor: rs.requestedBy}
	q.ProjectID, q.GroupKey = o.helpGroup(ctx, t.RequestID)
	t.Status = domain.TaskNeedsInput
	if err := o.store.UpdateTask(ctx, org, t); err != nil {
		o.failTask(ctx, rs, t, err)
		return OutcomeFailed, false
	}
	asked, _, err := qs.Ask(ctx, q)
	if err != nil {
		o.failTask(ctx, rs, t, err)
		return OutcomeFailed, false
	}
	tid := t.ID
	o.setState(ctx, t.AgentID, domain.StateWaiting, "Necesita tu ayuda: "+t.Title, &tid, 50)
	o.rec.Emit(ctx, Action{Type: domain.EvTaskNeedsInput, AgentID: t.AgentID, Entity: "task", EntityID: t.ID, SkipAudit: true,
		Payload: map[string]any{"task": t, "question_id": asked.ID}, Text: fmt.Sprintf("%s necesita tu ayuda: %s", agent.Name, t.Title)})
	o.traceEvent(ctx, t.ID, "help_request", map[string]any{"question_id": asked.ID, "kind": asked.Kind})
	o.emitMetrics(ctx)
	return OutcomeDone, true
}

// helpGate waits for the open question of a task (also after a restart) and applies its resolution. handled=false
// means the task runs (again) now, with the answer in its context.
func (o *Orchestrator) helpGate(ctx context.Context, rs *run, t *domain.Task) (Outcome, bool) {
	qs := o.help.questions
	if qs == nil {
		return OutcomeDone, false
	}
	q, ok := qs.OpenFor(ctx, t.ID)
	if !ok {
		return OutcomeDone, false
	}
	_, ch, err := qs.Attach(ctx, q.ID)
	if err != nil {
		o.failTask(ctx, rs, *t, err)
		return OutcomeFailed, true
	}
	org := o.org(ctx)
	if t.Status != domain.TaskNeedsInput {
		t.Status = domain.TaskNeedsInput
		_ = o.store.UpdateTask(ctx, org, *t)
	}
	tid := t.ID
	o.setState(ctx, t.AgentID, domain.StateWaiting, "Necesita tu ayuda: "+t.Title, &tid, 50)
	resume := YieldSlot(ctx) // a task waiting for a person does not hold a scheduler slot
	res, err := qs.Wait(ctx, ch, q)
	resume() // like the approval wait: the slot is taken back before the task goes on (or ends)
	if err != nil {
		return OutcomeFailed, true // shutdown: the question stays open and Recover waits for it again
	}
	switch res.Status {
	case QuestionAnswered:
		t.Status = domain.TaskPending
		_ = o.store.UpdateTask(ctx, org, *t)
		return OutcomeDone, false
	case QuestionReassigned:
		o.helpReassign(ctx, t, res)
		return OutcomeDone, false
	case QuestionSkipped:
		return o.omitTask(ctx, rs, *t, res), true
	}
	// cancelled (its project was cancelled): stop quietly, nothing was decided for it
	fin := time.Now().UTC()
	t.Status, t.FinishedAt = domain.TaskBlocked, &fin
	_ = o.store.UpdateTask(context.WithoutCancel(ctx), org, *t)
	o.setState(ctx, t.AgentID, domain.StateIdle, "", nil, 0)
	o.rec.Emit(ctx, Action{Type: domain.EvTaskBlocked, AgentID: t.AgentID, Entity: "task", EntityID: t.ID,
		Payload: map[string]any{"task": *t, "reason": "cancelled"}, Text: "Tarea detenida (se canceló): " + t.Title})
	return OutcomeBlocked, true
}

func (o *Orchestrator) helpReassign(ctx context.Context, t *domain.Task, q Question) {
	from := t.AgentID
	t.AgentID, t.Status = q.ReassignTo, domain.TaskPending
	t.AssignedReason = "Reasignada por " + q.ResolvedBy + " al responder una pregunta"
	_ = o.store.UpdateTask(ctx, o.org(ctx), *t)
	for _, lr := range o.liveRuns(t.RequestID) {
		if lr.has(t.ID) {
			lt := lr.task(t.ID)
			lt.AgentID, lt.AssignedReason = t.AgentID, t.AssignedReason
			lr.put(lt)
		}
	}
	o.setState(ctx, from, domain.StateIdle, "", nil, 0)
	o.rec.Emit(ctx, Action{Type: domain.EvTaskReassigned, AgentID: t.AgentID, Entity: "task", EntityID: t.ID, SkipAudit: true,
		Payload: map[string]any{"task": *t, "from": from, "to": t.AgentID}, Text: "Tarea reasignada: " + t.Title})
}

// ---- answering (the use case behind POST /questions/{id}/answer) ----

// AnswerQuestion validates and applies a person's resolution. A skip is refused when it would leave the work with no
// result at all, and needs confirm=true otherwise (the caller showed QuestionImpact first).
func (o *Orchestrator) AnswerQuestion(ctx context.Context, id string, r Resolution, confirm bool) (Question, error) {
	qs := o.help.questions
	if qs == nil {
		return Question{}, fmt.Errorf("%w: questions are not enabled", domain.ErrInvalid)
	}
	org := o.org(ctx)
	q, err := qs.Store.GetQuestion(ctx, org, id)
	if err != nil {
		return q, err
	}
	actor := ActorFrom(ctx, "user")
	if isAgentPrincipal(actor, q.AgentID) || strings.HasPrefix(actor, "agent:") {
		return q, fmt.Errorf("%w: only people answer the questions of agents", domain.ErrForbidden)
	}
	if q.Status != QuestionOpen {
		return q, fmt.Errorf("%w: the question is already %s", domain.ErrConflict, q.Status)
	}
	r.Text = strings.TrimSpace(SanitizeChatText(r.Text))
	if len([]rune(r.Text)) > MaxAnswerRunes {
		return q, fmt.Errorf("%w: the answer is too long (max %d characters)", domain.ErrInvalid, MaxAnswerRunes)
	}
	switch r.Kind {
	case AnswerText:
		if r.Text == "" {
			return q, fmt.Errorf("%w: the answer is empty", domain.ErrInvalid)
		}
	case AnswerOption:
		found := false
		for _, opt := range q.Options {
			if opt == r.Option {
				found = true
			}
		}
		if !found {
			return q, fmt.Errorf("%w: option is not one of the question's options", domain.ErrInvalid)
		}
	case AnswerFile:
		r.FilePath = strings.TrimSpace(r.FilePath)
		if !strings.HasPrefix(r.FilePath, "/") || len(r.FilePath) > 500 || strings.Contains(r.FilePath, "..") {
			return q, fmt.Errorf("%w: file_path must be a drive path", domain.ErrInvalid)
		}
	case AnswerReassign:
		t, err := o.store.GetTask(ctx, org, q.TaskID)
		if err != nil {
			return q, err
		}
		a, err := o.store.GetAgent(ctx, org, strings.TrimSpace(r.AgentID))
		if err != nil {
			return q, fmt.Errorf("%w: unknown agent", domain.ErrInvalid)
		}
		if err := o.canTake(ctx, t, a); err != nil {
			return q, err
		}
		if _, err := o.enforceDelegation(ctx, ActorFrom(ctx, ""), o.agentMap(ctx), a.ID, "task", t.ID, true); err != nil {
			return q, err
		}
		r.AgentID = a.ID
	case AnswerSkip:
		imp, err := o.QuestionImpact(ctx, id)
		if err != nil {
			return q, err
		}
		if !imp.Allowed {
			return q, fmt.Errorf("%w: skip_blocks_result: without this task the work cannot be finished; answer, reassign or cancel it", domain.ErrConflict)
		}
		if !confirm {
			return q, fmt.Errorf("%w: skip_needs_confirm: confirm the steps and objectives that will be left undone", domain.ErrConflict)
		}
	default:
		return q, fmt.Errorf("%w: kind must be text, option, file, reassign or skip", domain.ErrInvalid)
	}
	return qs.Resolve(ctx, id, actor, r)
}

// CancelQuestions closes the open questions of a request (its project was cancelled); their tasks stop.
func (o *Orchestrator) CancelQuestions(ctx context.Context, requestID, note string) int {
	if o.help.questions == nil {
		return 0
	}
	return o.help.questions.CancelRequest(ctx, requestID, note)
}
