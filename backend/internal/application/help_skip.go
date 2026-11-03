package application

import (
	"context"
	"fmt"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Skipping a task that asked for help, with sense (spec part 2.6): before skipping, the app works out what depends on
// it. Everything that depends on it (transitively) is left undone too. An objective is lost when ALL its final tasks
// (the ones no other task of the same objective uses) would be left undone. Losing every objective is refused (the
// work could not produce anything); losing some is allowed with a confirmation that names them, and the project ends
// "partly done".

// SkipImpact is what skipping the task of a question would leave undone.
type SkipImpact struct {
	Task           HelpTaskRef    `json:"task"`
	Skipped        []HelpTaskRef  `json:"skipped"`         // the tasks that depend on it (they will not run)
	ObjectivesLost []ObjectiveRef `json:"objectives_lost"` // objectives that would end without any result
	Objectives     int            `json:"objectives"`      // how many objectives the work has
	AllLost        bool           `json:"all_lost"`
	Allowed        bool           `json:"allowed"`
}

// QuestionImpact analyses the skip of the task behind a question.
func (o *Orchestrator) QuestionImpact(ctx context.Context, questionID string) (SkipImpact, error) {
	if o.help.questions == nil {
		return SkipImpact{}, fmt.Errorf("%w: questions are not enabled", domain.ErrInvalid)
	}
	q, err := o.help.questions.Store.GetQuestion(ctx, o.org(ctx), questionID)
	if err != nil {
		return SkipImpact{}, err
	}
	return o.skipImpact(ctx, q.RequestID, q.TaskID)
}

func (o *Orchestrator) skipImpact(ctx context.Context, requestID, taskID string) (SkipImpact, error) {
	org := o.org(ctx)
	all, err := o.store.ListTasksByRequest(ctx, org, requestID)
	if err != nil {
		return SkipImpact{}, err
	}
	var objOf map[string]ObjectiveRef
	if o.help.owner != nil {
		objOf, _ = o.help.owner.ObjectivesOfRequest(ctx, org, requestID)
	}
	return analyseSkip(all, taskID, objOf), nil
}

// analyseSkip is the pure part of the analysis. objOf maps task id -> objective (nil or missing: one objective for
// the whole request). Tasks that already finished are never left undone.
func analyseSkip(all []domain.Task, taskID string, objOf map[string]ObjectiveRef) SkipImpact {
	byID := make(map[string]domain.Task, len(all))
	for _, t := range all {
		byID[t.ID] = t
	}
	imp := SkipImpact{Task: HelpTaskRef{ID: taskID, Title: byID[taskID].Title}, Skipped: []HelpTaskRef{}, ObjectivesLost: []ObjectiveRef{}}
	undone := map[string]bool{taskID: true}
	for changed := true; changed; {
		changed = false
		for _, t := range all {
			if undone[t.ID] || t.Status == domain.TaskDone {
				continue
			}
			for _, d := range t.DependsOn {
				if undone[d] {
					undone[t.ID], changed = true, true
					imp.Skipped = append(imp.Skipped, HelpTaskRef{ID: t.ID, Title: t.Title})
					break
				}
			}
		}
	}
	objective := func(id string) ObjectiveRef {
		if r, ok := objOf[id]; ok && r.ID != "" {
			return r
		}
		return ObjectiveRef{ID: "request"}
	}
	members := map[string][]domain.Task{}
	var order []ObjectiveRef
	for _, t := range all {
		ob := objective(t.ID)
		if _, seen := members[ob.ID]; !seen {
			order = append(order, ob)
		}
		members[ob.ID] = append(members[ob.ID], t)
	}
	imp.Objectives = len(order)
	for _, ob := range order {
		ts := members[ob.ID]
		used := map[string]bool{}
		for _, t := range ts {
			for _, d := range t.DependsOn {
				if objective(d).ID == ob.ID {
					used[d] = true
				}
			}
		}
		lost := true
		for _, t := range ts {
			if !used[t.ID] && !undone[t.ID] { // a final task that still delivers
				lost = false
				break
			}
		}
		if lost {
			imp.ObjectivesLost = append(imp.ObjectivesLost, ob)
		}
	}
	imp.AllLost = imp.Objectives > 0 && len(imp.ObjectivesLost) == imp.Objectives
	imp.Allowed = !imp.AllLost
	return imp
}

// omitTask skips the task of a question: it counts as finished-without-output and everything that depends on it is
// left undone (marked omitted when the scheduler reaches it, see skipTask).
func (o *Orchestrator) omitTask(ctx context.Context, rs *run, t domain.Task, q Question) Outcome {
	org := o.org(ctx)
	imp, err := o.skipImpact(ctx, t.RequestID, t.ID)
	if err == nil {
		rs.mu.Lock()
		if rs.omitted == nil {
			rs.omitted = map[string]string{}
		}
		for _, d := range imp.Skipped {
			rs.omitted[d.ID] = t.Title
		}
		rs.mu.Unlock()
	}
	reason := q.Answer
	if reason == "" {
		reason = "omitida al responder una pregunta"
	}
	fin := time.Now().UTC()
	t.Status, t.StartedAt, t.FinishedAt = domain.TaskDone, nil, &fin
	t.Output = skippedOutput(t, q.ResolvedBy, reason)
	t.Output.Metrics["omitted_dependents"] = len(imp.Skipped)
	if len(imp.ObjectivesLost) > 0 {
		names := make([]string, 0, len(imp.ObjectivesLost))
		for _, ob := range imp.ObjectivesLost {
			names = append(names, ob.Title)
		}
		t.Output.Metrics["objectives_lost"] = names
	}
	if err := o.store.UpdateTask(ctx, org, t); err != nil {
		o.log.Warn("update task", "err", err)
	}
	o.dropCheckpoint(ctx, t.ID)
	o.setState(ctx, t.AgentID, domain.StateIdle, "", nil, 0)
	o.rec.Emit(ctx, Action{Type: domain.EvTaskCompleted, AgentID: t.AgentID, Entity: "task", EntityID: t.ID, SkipAudit: true,
		Payload: map[string]any{"task": t, "skipped": true}, Text: "Tarea omitida por una persona: " + t.Title})
	return OutcomeSkipped
}

// omittedBy is the title of the skipped task a dependent was left undone because of ("" = not omitted).
func (rs *run) omittedBy(taskID string) string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.omitted[taskID]
}

func omittedOutput(by string) *domain.StructuredOutput {
	out := &domain.StructuredOutput{
		Summary: fmt.Sprintf("Omitida: dependía de %q, que una persona omitió.", by),
		Metrics: map[string]any{"omitted": true, "skipped": true, "skip_reason": "depende de una tarea omitida: " + by},
	}
	out.Normalize()
	return out
}
