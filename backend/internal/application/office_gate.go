package application

import (
	"context"
	"time"

	"aiworkforce/backend/internal/domain"
)

// officeGateReason is shown on a task that cannot start because its agent stopped working for the office of its request.
const officeGateReason = "the agent no longer works for the office this request was asked from"

// officeGate blocks a task that has not started when its agent no longer works for the office of its request (it was
// taken out of that office meanwhile). The agent's own base-office requests are never affected.
func (o *Orchestrator) officeGate(ctx context.Context, rs *run, t domain.Task) (Outcome, bool) {
	office := rs.req.OfficeID
	if office == "" || o.officeScope == nil || t.AgentID == assistantID {
		return OutcomeDone, false
	}
	if ok, err := o.officeScope.WorksFor(ctx, o.org(ctx), t.AgentID, office); err == nil && ok {
		return OutcomeDone, false
	}
	t.Output = &domain.StructuredOutput{Summary: officeGateReason}
	t.Output.Normalize()
	o.blockTask(ctx, rs, t, officeGateReason)
	return OutcomeBlocked, true
}

// BlockOfficeTasks blocks the tasks of the agent that have not started and belong to requests asked from the office
// (called when the agent is taken out of that office): they stay blocked with a clear reason instead of running with
// the office's context. Returns how many.
func (o *Orchestrator) BlockOfficeTasks(ctx context.Context, org, agentID, office string) (int, error) {
	ctx = WithOrg(ctx, org)
	tasks, err := o.store.ListTasks(ctx, org, agentID, string(domain.TaskPending))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range tasks {
		if t.OfficeID == "" || messageOffice(t.OfficeID) != messageOffice(office) {
			continue
		}
		fin := time.Now().UTC()
		t.Status, t.FinishedAt = domain.TaskBlocked, &fin
		t.Output = &domain.StructuredOutput{Summary: officeGateReason}
		t.Output.Normalize()
		if err := o.store.UpdateTask(ctx, org, t); err != nil {
			return n, err
		}
		o.rec.Emit(ctx, Action{Type: domain.EvTaskBlocked, AgentID: t.AgentID, Entity: "task", EntityID: t.ID,
			Payload: map[string]any{"task": t, "reason": officeGateReason}, Text: "Tarea bloqueada (" + officeGateReason + "): " + t.Title})
		n++
	}
	return n, nil
}
