package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// maxApplyNow bounds how many times one task is restarted by "apply now".
const maxApplyNow = 3

// v2State is the optional V2 wiring of the orchestrator. Zero value = disabled.
type v2State struct {
	store    V2Store
	profiles *ProfileService
	tracer   *Tracer

	mu       sync.Mutex
	inflight map[string]*inflightCall // task id -> its current runtime call
	restarts map[string]int
}

// inflightCall is the runtime call of a task that can be cancelled by "apply now".
type inflightCall struct {
	cancel  context.CancelFunc
	done    bool
	restart bool
}

// SetV2 installs the work profiles, the thought audit and the intervention store.
func (o *Orchestrator) SetV2(store V2Store, profiles *ProfileService, tracer *Tracer) {
	o.v2.store, o.v2.profiles, o.v2.tracer = store, profiles, tracer
}

func (o *Orchestrator) traceEvent(ctx context.Context, taskID, typ string, data map[string]any) {
	if o.v2.tracer != nil {
		o.v2.tracer.Event(context.WithoutCancel(ctx), o.org(ctx), taskID, typ, data)
	}
}

// decorateRun adds the work profile and the human steer notes to a runtime request.
func (o *Orchestrator) decorateRun(ctx context.Context, in *RunTaskRequest) {
	if o.v2.store == nil {
		return
	}
	if o.v2.profiles != nil {
		if p, ok := o.v2.profiles.RunProfileOf(ctx, in.Agent.ID); ok {
			in.Agent.Instructions, in.Agent.Creativity, in.Agent.Skills = p.Instructions, p.Creativity, p.Skills
		}
	}
	o.applyGuidance(ctx, in)
	if o.v2.tracer != nil && o.v2.tracer.StorePrompts(ctx, o.org(ctx)) {
		in.WantTrace = true
	}
}

// applyGuidance (re)loads the steer notes of the task: they ride on every
// runtime call of the task, so the next call, a retry and a rework all see them.
func (o *Orchestrator) applyGuidance(ctx context.Context, in *RunTaskRequest) {
	if o.v2.store == nil {
		return
	}
	notes, err := o.v2.store.ListSteerNotes(ctx, o.org(ctx), in.Task.ID)
	if err != nil || len(notes) == 0 {
		in.HumanGuidance = nil
		return
	}
	if len(notes) > MaxSteerNotesSent {
		notes = notes[len(notes)-MaxSteerNotesSent:]
	}
	in.HumanGuidance = make([]string, 0, len(notes))
	var ids []string
	for _, n := range notes {
		in.HumanGuidance = append(in.HumanGuidance, n.Note)
		if n.DeliveredAt == nil {
			ids = append(ids, n.ID)
		}
	}
	if len(ids) > 0 {
		_ = o.v2.store.MarkSteerDelivered(ctx, o.org(ctx), ids, time.Now().UTC())
	}
}

// rtRunTask is the single place where run-task calls go through, so that every
// call is traced (success or failure) and the trace never leaks to callers.
func (o *Orchestrator) rtRunTask(ctx context.Context, rs *run, kind string, in RunTaskRequest) (RunTaskResponse, error) {
	t0 := time.Now()
	resp, err := o.rt.RunTask(ctx, in)
	if o.v2.tracer != nil {
		c := RunCall{AgentID: in.Agent.ID, TaskID: in.Task.ID, RequestID: rs.req.ID, Kind: kind, In: in, Err: err, Latency: time.Since(t0)}
		if err == nil {
			c.Resp = &resp
		}
		o.v2.tracer.Record(context.WithoutCancel(ctx), o.org(ctx), c)
	}
	resp.Trace = nil
	return resp, err
}

// beginInflight registers the cancellable runtime call of a task.
func (o *Orchestrator) beginInflight(ctx context.Context, taskID string) (context.Context, func() (restart bool)) {
	cctx, cancel := context.WithCancel(ctx)
	call := &inflightCall{cancel: cancel}
	o.v2.mu.Lock()
	if o.v2.inflight == nil {
		o.v2.inflight = map[string]*inflightCall{}
	}
	o.v2.inflight[taskID] = call
	o.v2.mu.Unlock()
	return cctx, func() bool {
		o.v2.mu.Lock()
		call.done = true
		if o.v2.inflight[taskID] == call {
			delete(o.v2.inflight, taskID)
		}
		r := call.restart
		o.v2.mu.Unlock()
		cancel()
		return r
	}
}

// SteerResult is the outcome of POST /tasks/{id}/steer.
type SteerResult struct {
	Note              SteerNote `json:"note"`
	ApplyNowStatus    string    `json:"apply_now_status"` // ""|restarted|not_mid_call|limit_reached
	EstimatedRerunUSD float64   `json:"estimated_rerun_usd"`
}

func steerable(st domain.TaskStatus) bool {
	return st == domain.TaskPending || st == domain.TaskRunning || st == domain.TaskBlocked || st == domain.TaskAwaitingApproval
}

// Steer stores a human note for a task and, when asked, restarts its in-flight
// runtime call so the note applies now. The restart goes through the same guards
// as any call (kill switch, pause, budget reservation); it never touches a tool
// execution, because tools only run after the call it cancels.
func (o *Orchestrator) Steer(ctx context.Context, taskID, note string, applyNow bool) (SteerResult, error) {
	if o.v2.store == nil {
		return SteerResult{}, fmt.Errorf("%w: intervention is not enabled", domain.ErrInvalid)
	}
	note = SanitizeChatText(note)
	if note == "" {
		return SteerResult{}, fmt.Errorf("%w: note is required", domain.ErrInvalid)
	}
	if len([]rune(note)) > MaxSteerNoteRunes {
		return SteerResult{}, fmt.Errorf("%w: note is too long (max %d characters)", domain.ErrInvalid, MaxSteerNoteRunes)
	}
	if o.v2.tracer != nil && o.v2.tracer.redact(note) != note {
		return SteerResult{}, fmt.Errorf("%w: the note seems to contain a secret; it is sent to the model provider", domain.ErrInvalid)
	}
	org := o.org(ctx)
	t, err := o.store.GetTask(ctx, org, taskID)
	if err != nil {
		return SteerResult{}, err
	}
	if !steerable(t.Status) {
		return SteerResult{}, fmt.Errorf("%w: the task is already %s", domain.ErrConflict, t.Status)
	}
	actor := ActorFrom(ctx, "user")
	n := SteerNote{ID: newID(), TaskID: t.ID, AgentID: t.AgentID, Note: note, Author: actor, CreatedAt: time.Now().UTC()}
	if err := o.v2.store.AddSteerNote(ctx, org, n); err != nil {
		return SteerResult{}, err
	}
	res := SteerResult{Note: n, EstimatedRerunUSD: o.reserveFor(ctx, t.RequestID, domain.UsageRunTask, t.ID)}
	o.rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: "task.steered", Entity: "task", EntityID: t.ID,
		Details: map[string]any{"note_id": n.ID, "chars": len([]rune(note)), "apply_now": applyNow, "agent_id": t.AgentID}})
	o.traceEvent(ctx, t.ID, "steer", map[string]any{"note_id": n.ID, "author": actor})
	o.rec.Emit(ctx, Action{Type: domain.EvTaskSteered, AgentID: t.AgentID, Entity: "task", EntityID: t.ID, SkipAudit: true,
		Payload: map[string]any{"task_id": t.ID, "note_id": n.ID}, Text: "Nota de guía para: " + t.Title})
	if applyNow {
		res.ApplyNowStatus = o.applyNow(ctx, t.ID, actor)
	}
	return res, nil
}

func (o *Orchestrator) applyNow(ctx context.Context, taskID, actor string) string {
	o.v2.mu.Lock()
	defer o.v2.mu.Unlock()
	call := o.v2.inflight[taskID]
	if call == nil || call.done {
		return "not_mid_call"
	}
	if o.v2.restarts[taskID] >= maxApplyNow {
		return "limit_reached"
	}
	if o.v2.restarts == nil {
		o.v2.restarts = map[string]int{}
	}
	o.v2.restarts[taskID]++
	call.restart = true
	call.cancel()
	o.rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: "task.apply_now", Entity: "task", EntityID: taskID,
		Details: map[string]any{"restart": o.v2.restarts[taskID], "max": maxApplyNow}})
	return "restarted"
}

// Candidate is an agent a task could be reassigned to.
type Candidate struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	Title string `json:"title"`
}

// Intervention is what GET /tasks/{id}/intervention returns.
type Intervention struct {
	TaskStatus        string      `json:"task_status"`
	SteerNotes        []SteerNote `json:"steer_notes"`
	CanSteer          bool        `json:"can_steer"`
	CanApplyNow       bool        `json:"can_apply_now"`
	CanReassign       bool        `json:"can_reassign"`
	EstimatedRerunUSD float64     `json:"estimated_rerun_usd"`
	Candidates        []Candidate `json:"reassign_candidates"`
}

// Intervention describes what a human can do with a task right now.
func (o *Orchestrator) Intervention(ctx context.Context, taskID string) (Intervention, error) {
	org := o.org(ctx)
	t, err := o.store.GetTask(ctx, org, taskID)
	if err != nil {
		return Intervention{}, err
	}
	out := Intervention{TaskStatus: string(t.Status), SteerNotes: []SteerNote{}, Candidates: []Candidate{},
		CanSteer: o.v2.store != nil && steerable(t.Status), CanReassign: t.Status == domain.TaskPending,
		EstimatedRerunUSD: o.reserveFor(ctx, t.RequestID, domain.UsageRunTask, t.ID)}
	if o.v2.store != nil {
		if out.SteerNotes, err = o.v2.store.ListSteerNotes(ctx, org, taskID); err != nil {
			return out, err
		}
	}
	o.v2.mu.Lock()
	if c := o.v2.inflight[taskID]; c != nil && !c.done && o.v2.restarts[taskID] < maxApplyNow {
		out.CanApplyNow = true
	}
	o.v2.mu.Unlock()
	if out.CanReassign {
		agents, _ := o.store.ListAgents(ctx, org)
		for _, a := range agents {
			if o.canTake(ctx, t, a) == nil {
				out.Candidates = append(out.Candidates, Candidate{ID: a.ID, Name: a.Name, Role: a.Role, Title: a.Title})
			}
		}
	}
	return out, nil
}

// canTake validates that an agent may take over a pending task.
func (o *Orchestrator) canTake(ctx context.Context, t domain.Task, a domain.Agent) error {
	if a.ID == t.AgentID {
		return fmt.Errorf("%w: the task already belongs to this agent", domain.ErrInvalid)
	}
	if a.Role == AuditorRole { // the internal auditor keeps its independence: it only audits
		cur, _ := o.store.GetAgent(ctx, o.org(ctx), t.AgentID)
		if cur.Role != AuditorRole {
			return fmt.Errorf("%w: the internal auditor does not take regular work", domain.ErrInvalid)
		}
	}
	if g := o.conn.guard; g != nil {
		if v := g.Admit(ctx, o.org(ctx), a.ID); !v.Allowed {
			return fmt.Errorf("%w: the agent is unavailable (%s)", domain.ErrConflict, v.Code)
		}
	}
	return nil
}

// Reassign moves a pending task to another agent (human-only, audited).
func (o *Orchestrator) Reassign(ctx context.Context, taskID, agentID string) (domain.Task, error) {
	org := o.org(ctx)
	t, err := o.store.GetTask(ctx, org, taskID)
	if err != nil {
		return t, err
	}
	if t.Status != domain.TaskPending {
		return t, fmt.Errorf("%w: only pending tasks can be reassigned (this one is %s)", domain.ErrConflict, t.Status)
	}
	a, err := o.store.GetAgent(ctx, org, strings.TrimSpace(agentID))
	if err != nil {
		return t, fmt.Errorf("%w: unknown agent", domain.ErrInvalid)
	}
	if err := o.canTake(ctx, t, a); err != nil {
		return t, err
	}
	from := t.AgentID
	actor := ActorFrom(ctx, "user")
	t.AgentID = a.ID
	t.AssignedReason = "Reasignada por " + actor
	if err := o.store.UpdateTask(ctx, org, t); err != nil {
		return t, err
	}
	for _, lr := range o.liveRuns(t.RequestID) { // the scheduler of the request runs the new owner
		if lr.has(t.ID) {
			lt := lr.task(t.ID)
			lt.AgentID, lt.AssignedReason = a.ID, t.AssignedReason
			lr.put(lt)
		}
	}
	o.rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: "task.reassigned", Entity: "task", EntityID: t.ID,
		Details: map[string]any{"from": from, "to": a.ID}})
	o.rec.Emit(ctx, Action{Type: domain.EvTaskReassigned, AgentID: a.ID, Entity: "task", EntityID: t.ID, SkipAudit: true,
		Payload: map[string]any{"task": t, "from": from, "to": a.ID}, Text: fmt.Sprintf("Tarea reasignada a %s: %s", a.Name, t.Title)})
	return t, nil
}

// skillsByAgent returns the custom skill tags per agent (routing hints only).
func (o *Orchestrator) skillsByAgent(ctx context.Context) map[string][]string {
	if o.v2.profiles == nil {
		return nil
	}
	return o.v2.profiles.SkillsByAgent(ctx)
}

func (o *Orchestrator) withRouteSkills(ctx context.Context, agents []RouteAgent) []RouteAgent {
	skills := o.skillsByAgent(ctx)
	for i := range agents {
		agents[i].Skills = skills[agents[i].ID]
	}
	return agents
}
