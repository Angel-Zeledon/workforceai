package application

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Stalled-work watchdog (F6, docs/architecture/watchdog.md). A periodic sweep, without any model call, finds the work
// that stopped making progress (Orchestrator.Stalled) and the owning agent raises a suggestion ("the task X has made
// no progress for 40 min: shall I retry it, hand it to someone else or cancel it?") through InitiativeService.Raise,
// so the initiative budget and the 24 h dedupe apply. A person then retries, reassigns or cancels
// (Orchestrator.RecoverStalled); accepting the suggestion is a retry, never a new request.

// SignalStalledTask is the initiative signal of a stalled task.
const SignalStalledTask = "stalled_task"

// EvTaskStalled (additive WS event): a task stopped making progress (once per episode and process).
const EvTaskStalled = "task.stalled"

// Defaults and ranges of the "no progress" thresholds (minutes).
const (
	DefaultStallTaskMinutes    = 30
	DefaultStallProjectMinutes = 120

	minStallTaskMinutes, maxStallTaskMinutes       = 5, 1440
	minStallProjectMinutes, maxStallProjectMinutes = 15, 4320

	// stallDedupe: the same stall (episode) is not suggested again inside it.
	stallDedupe = 24 * time.Hour
	// stallSeenKeep bounds the memory of the episodes already announced.
	stallSeenKeep = 48 * time.Hour
)

// StalledWorkSignal is the per-organization configuration of the watchdog (signals.stalled_work). The project
// threshold is longer: project tasks queue behind interactive work and a large project chains many of them.
type StalledWorkSignal struct {
	Enabled        bool `json:"enabled"`
	TaskMinutes    int  `json:"task_minutes"`
	ProjectMinutes int  `json:"project_minutes"`
}

// DefaultStalledWork: on, 30 min for a task, 120 min for a task of a project.
func DefaultStalledWork() StalledWorkSignal {
	return StalledWorkSignal{Enabled: true, TaskMinutes: DefaultStallTaskMinutes, ProjectMinutes: DefaultStallProjectMinutes}
}

// Policy turns the thresholds into a detection policy at a moment.
func (s StalledWorkSignal) Policy(now time.Time) StallPolicy {
	return StallPolicy{Task: time.Duration(s.TaskMinutes) * time.Minute, Project: time.Duration(s.ProjectMinutes) * time.Minute, Now: now}
}

// Watchdog sweeps for stalled work and serves the recovery of a stalled task.
type Watchdog struct {
	Orch *Orchestrator
	// Initiative holds the thresholds (signals.stalled_work) and raises the suggestions. nil: the defaults, no suggestions.
	Initiative *InitiativeService
	Rec        *Recorder
	Cfg        Config
	Now        func() time.Time
	// Locale is the organization's language (es/en/pt) for the stored text of a suggestion. nil: es.
	Locale func(ctx context.Context) string

	mu   sync.Mutex
	seen map[string]time.Time // org|episode -> when it was announced (event + audit once per episode)
}

func (w *Watchdog) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}

// settings returns the thresholds of the organization of ctx.
func (w *Watchdog) settings(ctx context.Context) (StalledWorkSignal, error) {
	if w.Initiative == nil || w.Initiative.Store == nil {
		return DefaultStalledWork(), nil
	}
	st, err := w.Initiative.Settings(ctx)
	if err != nil {
		return StalledWorkSignal{}, err
	}
	return st.Signals.StalledWork, nil
}

// Policy is the detection policy of the organization of ctx now (the thresholds apply even when the sweep is off,
// so a task detail can still offer its recovery).
func (w *Watchdog) Policy(ctx context.Context) (StallPolicy, error) {
	s, err := w.settings(ctx)
	if err != nil {
		return StallPolicy{}, err
	}
	return s.Policy(w.now()), nil
}

// List returns the stalled tasks of the organization of ctx.
func (w *Watchdog) List(ctx context.Context) ([]StalledTask, error) {
	p, err := w.Policy(ctx)
	if err != nil {
		return nil, err
	}
	v, err := w.Orch.Stalled(ctx, p)
	if v == nil {
		v = []StalledTask{}
	}
	return v, err
}

// StallScanResult is what the sweep did with one stalled task.
type StallScanResult struct {
	StalledTask
	Announced    bool   `json:"announced"` // first time this episode was seen: event and audit written
	Raised       bool   `json:"raised"`
	Reason       string `json:"reason,omitempty"` // why no suggestion: disabled | duplicate | daily_limit | ... | error
	InitiativeID string `json:"initiative_id,omitempty"`
}

// Scan runs one sweep over the organization of ctx: new episodes are announced (event task.stalled, audit) and
// suggested; pending suggestions whose task moved again are closed.
func (w *Watchdog) Scan(ctx context.Context) ([]StallScanResult, error) {
	cfg, err := w.settings(ctx)
	if err != nil {
		return nil, err
	}
	out := []StallScanResult{}
	if !cfg.Enabled {
		return out, nil
	}
	stalled, err := w.Orch.Stalled(ctx, cfg.Policy(w.now()))
	if err != nil {
		return nil, err
	}
	org := w.Orch.org(ctx)
	open := map[string]bool{}
	for _, st := range stalled {
		open[st.TaskID] = true
		r := StallScanResult{StalledTask: st}
		if w.firstSeen(org, st.Episode) {
			r.Announced = true
			w.announce(ctx, st)
		}
		if w.Initiative != nil {
			out, err := w.Initiative.Raise(ctx, w.suggestion(ctx, st))
			switch {
			case err != nil:
				r.Reason = "error"
			case out.Raised:
				r.Raised, r.InitiativeID = true, out.Initiative.ID
			default:
				r.Reason = out.Reason
			}
		}
		out = append(out, r)
	}
	w.closeResolved(ctx, open, "system", "no_longer_stalled", "")
	return out, nil
}

func (w *Watchdog) firstSeen(org, episode string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	if w.seen == nil {
		w.seen = map[string]time.Time{}
	}
	for k, at := range w.seen {
		if now.Sub(at) > stallSeenKeep {
			delete(w.seen, k)
		}
	}
	k := org + "|" + episode
	if _, ok := w.seen[k]; ok {
		return false
	}
	w.seen[k] = now
	return true
}

func (w *Watchdog) announce(ctx context.Context, st StalledTask) {
	if w.Rec == nil {
		return
	}
	details := map[string]any{"task_id": st.TaskID, "request_id": st.RequestID, "agent_id": st.AgentID, "title": st.Title,
		"kind": st.Kind, "minutes": st.Minutes, "project": st.Project, "last_progress": st.LastProgress}
	w.Rec.Audit(ctx, domain.AuditLog{Actor: "system", Action: "task.stalled", Entity: "task", EntityID: st.TaskID, RequestID: st.RequestID, Details: details})
	w.Rec.Emit(ctx, Action{Type: EvTaskStalled, AgentID: st.AgentID, Entity: "task", EntityID: st.TaskID, SkipAudit: true,
		Payload: details, Text: fmt.Sprintf("La tarea %q lleva %d min sin avanzar", truncate(st.Title, 80), st.Minutes)})
}

// stallTexts are the stored texts of a stalled-task suggestion per organization language (the UI renders params).
var stallTexts = map[string][3]string{
	"es": {"La tarea %q lleva %d min sin avanzar", "¿La reintento, se la paso a otro o la cancelo?", "Reintentar la tarea atascada %q por el camino normal."},
	"en": {"The task %q has made no progress for %d min", "Shall I retry it, hand it to someone else or cancel it?", "Retry the stalled task %q through the normal path."},
	"pt": {"A tarefa %q está há %d min sem avançar", "Tento de novo, passo para outro ou cancelo?", "Tentar de novo a tarefa parada %q pelo caminho normal."},
}

func (w *Watchdog) suggestion(ctx context.Context, st StalledTask) RaiseInput {
	loc := "es"
	if w.Locale != nil {
		if l := w.Locale(ctx); stallTexts[l][0] != "" {
			loc = l
		}
	}
	tx := stallTexts[loc]
	title := truncate(st.Title, 100)
	return RaiseInput{AgentID: st.AgentID, Signal: SignalStalledTask, SignalKey: st.Episode, Dedupe: stallDedupe,
		Title: fmt.Sprintf(tx[0], title, st.Minutes), Body: tx[1], Prompt: fmt.Sprintf(tx[2], title),
		Params: map[string]string{"task_id": st.TaskID, "request_id": st.RequestID, "task": title, "minutes": strconv.Itoa(st.Minutes),
			"kind": st.Kind, "project": strconv.FormatBool(st.Project)}}
}

// closeResolved closes the pending stalled-task suggestions whose task is not in open (it moved again, or a person
// recovered it): accepted by the person who recovered it, dismissed by the system when it moved on its own.
func (w *Watchdog) closeResolved(ctx context.Context, open map[string]bool, by, reason, requestID string) {
	if w.Initiative == nil || w.Initiative.Store == nil {
		return
	}
	org := w.Orch.org(ctx)
	its, err := w.Initiative.Store.ListInitiatives(ctx, org, InitiativePending, 500)
	if err != nil {
		return
	}
	to := InitiativeDismissed
	if by != "system" {
		to = InitiativeAccepted
	}
	for _, it := range its {
		if it.Signal != SignalStalledTask || it.Params["task_id"] == "" || open[it.Params["task_id"]] {
			continue
		}
		if _, err := w.Initiative.Store.ResolveInitiative(ctx, org, it.ID, InitiativePending, to, by, requestID, w.now()); err == nil {
			w.Initiative.audit(ctx, by, "initiative.closed", it.ID, map[string]any{"agent_id": it.AgentID, "signal": it.Signal,
				"task_id": it.Params["task_id"], "reason": reason, "status": to})
		}
	}
}

// Detail describes what a person may do with a task (stalled or not).
func (w *Watchdog) Detail(ctx context.Context, taskID string) (StallDetail, error) {
	p, err := w.Policy(ctx)
	if err != nil {
		return StallDetail{}, err
	}
	return w.Orch.StallDetail(ctx, taskID, p)
}

// Recover retries, reassigns or cancels a stalled task; the pending suggestion about it is closed as accepted by
// the person.
func (w *Watchdog) Recover(ctx context.Context, taskID string, in StallRecoverInput) (StallRecoverResult, error) {
	p, err := w.Policy(ctx)
	if err != nil {
		return StallRecoverResult{}, err
	}
	res, err := w.Orch.RecoverStalled(ctx, taskID, p, in)
	if err != nil {
		return res, err
	}
	w.closeResolved(ctx, map[string]bool{}, ActorFrom(ctx, "user"), "recovered:"+in.Action, res.Task.RequestID)
	return res, nil
}

// acceptStall is the action of a stalled-task suggestion: accepting it retries the task (InitiativeService.Actions).
func (w *Watchdog) acceptStall(ctx context.Context, it Initiative) (string, error) {
	id := it.Params["task_id"]
	if id == "" {
		return "", fmt.Errorf("%w: the suggestion names no task", domain.ErrInvalid)
	}
	p, err := w.Policy(ctx)
	if err != nil {
		return "", err
	}
	res, err := w.Orch.RecoverStalled(ctx, id, p, StallRecoverInput{Action: StallRetry})
	if err != nil {
		return "", err
	}
	return res.Task.RequestID, nil
}

// Install registers the retry as the action of stalled-task suggestions.
func (w *Watchdog) Install() {
	if w.Initiative != nil {
		w.Initiative.SetAction(SignalStalledTask, w.acceptStall)
	}
}
