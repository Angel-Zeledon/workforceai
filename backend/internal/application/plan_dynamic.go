package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
)

// Mid-flight plan amendments (Q2). A launched request keeps its tasks in the
// store; AmendRun changes them while the request is running: it adds tasks,
// re-points the dependencies of tasks that have not started, drops pending
// tasks and re-queues blocked ones. The running Scheduler takes the change
// through its LiveHandle (scheduler_dynamic.go); when no scheduler is open
// (the request finished or failed) a re-open run executes the new tasks, the
// same mechanism as the manual retry (recovery.go). Everything is persisted
// as ordinary tasks, so a restart resumes the amended plan.
//
// AmendRun itself does not decide whether a change is allowed: the projects
// layer only calls it for plan changes a human approved.

// liveRun is one scheduler of a request that still accepts amendments.
type liveRun struct {
	reqID  string
	rs     *run
	main   bool // the original run (it also writes the final report)
	scoped bool // dependencies outside the run's task set count as done (re-open runs)
	h      *LiveHandle

	amendMu sync.Mutex // one amendment at a time; also orders them against unregisterLive
	dmu     sync.RWMutex
	byID    map[string]domain.Task
	order   []domain.Task // tasks in creation order (what finish() reports on)
}

func (lr *liveRun) task(id string) domain.Task {
	lr.dmu.RLock()
	defer lr.dmu.RUnlock()
	return lr.byID[id]
}

func (lr *liveRun) has(id string) bool {
	lr.dmu.RLock()
	defer lr.dmu.RUnlock()
	_, ok := lr.byID[id]
	return ok
}

func (lr *liveRun) put(t domain.Task) {
	lr.dmu.Lock()
	defer lr.dmu.Unlock()
	if _, ok := lr.byID[t.ID]; !ok {
		lr.order = append(lr.order, t)
	}
	lr.byID[t.ID] = t
}

// nodes are the scheduler nodes of tasks: a re-open run only waits for the
// dependencies it runs itself (the others are already decided).
func (lr *liveRun) nodes(tasks []domain.Task) []Node {
	in := map[string]bool{}
	for _, t := range tasks {
		in[t.ID] = true
	}
	out := make([]Node, 0, len(tasks))
	for _, t := range tasks {
		deps := t.DependsOn
		if lr.scoped {
			deps = []string{}
			for _, d := range t.DependsOn {
				if in[d] {
					deps = append(deps, d)
				}
			}
		}
		out = append(out, Node{ID: t.ID, DependsOn: deps})
	}
	return out
}

func (o *Orchestrator) registerLive(rs *run, tasks []domain.Task, main bool) *liveRun {
	lr := &liveRun{reqID: rs.req.ID, rs: rs, main: main, scoped: !main, h: NewLiveHandle(), byID: make(map[string]domain.Task, len(tasks))}
	for _, t := range tasks {
		lr.byID[t.ID] = t
		lr.order = append(lr.order, t)
	}
	d := &o.durable
	d.mu.Lock()
	if d.live == nil {
		d.live = map[string][]*liveRun{}
	}
	d.live[rs.req.ID] = append(d.live[rs.req.ID], lr)
	d.mu.Unlock()
	return lr
}

// unregisterLive is called after the scheduler's Run returned: it returns the
// tasks of the run including those added while it ran.
func (o *Orchestrator) unregisterLive(lr *liveRun) []domain.Task {
	lr.amendMu.Lock() // an amendment in flight finishes (or is refused: the handle is closed)
	defer lr.amendMu.Unlock()
	d := &o.durable
	d.mu.Lock()
	d.live[lr.reqID] = slices.DeleteFunc(d.live[lr.reqID], func(x *liveRun) bool { return x == lr })
	if len(d.live[lr.reqID]) == 0 {
		delete(d.live, lr.reqID)
	}
	d.mu.Unlock()
	lr.dmu.RLock()
	defer lr.dmu.RUnlock()
	out := make([]domain.Task, len(lr.order))
	for i, t := range lr.order {
		out[i] = lr.byID[t.ID]
	}
	return out
}

func (o *Orchestrator) liveRuns(reqID string) []*liveRun {
	d := &o.durable
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.live[reqID])
}

// NewTaskSpec is a task to add. Key is local to the amendment; DependsOn holds
// ids of existing tasks or keys of other new tasks.
type NewTaskSpec struct {
	Key         string
	Title       string
	Description string
	AgentID     string
	DependsOn   []string
}

// RunAmendment is a change of the task set of a running request.
type RunAmendment struct {
	Add []NewTaskSpec
	// Repoint maps a pending or blocked task to its complete new dependency list.
	Repoint map[string][]string
	// Edit changes the title, description or agent of pending (or blocked) tasks.
	Edit map[string]TaskEdit
	// Drop lists pending (or blocked) tasks that must never run.
	Drop []string
	// Requeue lists blocked tasks to run again once their dependencies are done.
	Requeue []string
}

// TaskEdit is a change of the content of a task that has not started.
type TaskEdit struct {
	Title       *string
	Description *string
	AgentID     *string
}

// AmendResult says what AmendRun did.
type AmendResult struct {
	Added    map[string]string // key -> new task id
	Requeued []string          // task ids put back in the queue
	Live     bool              // true: applied to the running scheduler; false: executed by a re-open run
}

// AmendRun applies a RunAmendment to a request. It refuses to touch tasks that
// are running or done, dependency cycles and unknown tasks or agents; nothing
// is changed when it refuses.
func (o *Orchestrator) AmendRun(ctx context.Context, requestID string, a RunAmendment) (AmendResult, error) {
	org := o.org(ctx)
	req, err := o.store.GetRequest(ctx, org, requestID)
	if err != nil {
		return AmendResult{}, err
	}
	all, err := o.store.ListTasksByRequest(ctx, org, requestID)
	if err != nil {
		return AmendResult{}, err
	}
	byID := make(map[string]domain.Task, len(all))
	for _, t := range all {
		byID[t.ID] = t
	}
	// ---- validation (no side effects) ----
	editable := func(id string) error {
		t, ok := byID[id]
		if !ok {
			return fmt.Errorf("%w: task %s", domain.ErrNotFound, id)
		}
		if t.Status != domain.TaskPending && t.Status != domain.TaskBlocked {
			return fmt.Errorf("%w: task %q is %s: only pending or blocked tasks can be changed", domain.ErrConflict, t.Title, t.Status)
		}
		return nil
	}
	for id := range a.Repoint {
		if err := editable(id); err != nil {
			return AmendResult{}, err
		}
	}
	for id := range a.Edit {
		if err := editable(id); err != nil {
			return AmendResult{}, err
		}
	}
	for _, id := range a.Drop {
		if err := editable(id); err != nil {
			return AmendResult{}, err
		}
	}
	for _, id := range a.Requeue {
		t, ok := byID[id]
		if !ok || t.Status != domain.TaskBlocked {
			return AmendResult{}, fmt.Errorf("%w: only a blocked task can be re-queued (%s)", domain.ErrConflict, id)
		}
		if slices.Contains(a.Drop, id) {
			return AmendResult{}, fmt.Errorf("%w: task %s is both dropped and re-queued", domain.ErrInvalid, id)
		}
	}
	newIDs := map[string]string{}
	for _, n := range a.Add {
		if n.Key == "" || strings.TrimSpace(n.Title) == "" {
			return AmendResult{}, fmt.Errorf("%w: a new task needs a key and a title", domain.ErrInvalid)
		}
		if _, dup := newIDs[n.Key]; dup {
			return AmendResult{}, fmt.Errorf("%w: duplicate task key %q", domain.ErrInvalid, n.Key)
		}
		newIDs[n.Key] = newID()
	}
	resolve := func(refs []string) ([]string, error) {
		out := make([]string, 0, len(refs))
		for _, r := range refs {
			if id, ok := newIDs[r]; ok {
				out = append(out, id)
			} else if _, ok := byID[r]; ok {
				out = append(out, r)
			} else {
				return nil, fmt.Errorf("%w: unknown dependency %q", domain.ErrInvalid, r)
			}
		}
		return out, nil
	}
	deps := map[string][]string{} // final dependency list of every task
	for _, t := range all {
		deps[t.ID] = t.DependsOn
	}
	for id, refs := range a.Repoint {
		r, err := resolve(refs)
		if err != nil {
			return AmendResult{}, err
		}
		deps[id] = r
	}
	for _, n := range a.Add {
		r, err := resolve(n.DependsOn)
		if err != nil {
			return AmendResult{}, err
		}
		deps[newIDs[n.Key]] = r
	}
	if cyc := findCycle(deps); cyc != "" {
		return AmendResult{}, fmt.Errorf("%w: the change creates a dependency cycle through %s", domain.ErrInvalid, cyc)
	}
	now := time.Now().UTC()
	depth := func(id string) int { return byID[id].Depth }
	fresh := make([]domain.Task, 0, len(a.Add))
	freshByID := map[string]domain.Task{}
	// Depth is computed in dependency order (new tasks may depend on new tasks).
	pendingSpecs := slices.Clone(a.Add)
	for len(pendingSpecs) > 0 {
		progressed := false
		rest := pendingSpecs[:0:0]
		for _, n := range pendingSpecs {
			id := newIDs[n.Key]
			ready, d := true, 1
			for _, dep := range deps[id] {
				if ft, ok := freshByID[dep]; ok {
					d = max(d, ft.Depth+1)
				} else if _, isNew := idIsNew(newIDs, dep); isNew {
					ready = false
				} else {
					d = max(d, depth(dep)+1)
				}
			}
			if !ready {
				rest = append(rest, n)
				continue
			}
			progressed = true
			agent := n.AgentID
			if agent == "" {
				agent = assistantID
			}
			t := domain.Task{ID: id, RequestID: requestID, Title: strings.TrimSpace(n.Title), Description: n.Description, AgentID: agent,
				Status: domain.TaskPending, DependsOn: deps[id], CreatedAt: now, Depth: d,
				AssignedReason: "Added by an approved plan change"}
			fresh = append(fresh, t)
			freshByID[id] = t
		}
		if !progressed {
			return AmendResult{}, fmt.Errorf("%w: unresolved dependencies among the new tasks", domain.ErrInvalid)
		}
		pendingSpecs = rest
	}
	changed := func(id string) domain.Task { // an existing task with its new dependencies
		t := byID[id]
		t.DependsOn = deps[id]
		return t
	}
	requeue := map[string]bool{}
	for _, id := range a.Requeue {
		requeue[id] = true
	}
	editIDs := map[string]bool{}
	for id := range a.Repoint {
		editIDs[id] = true
	}
	for id := range a.Edit {
		editIDs[id] = true
	}
	for _, id := range a.Requeue {
		editIDs[id] = true
	}
	var edited []domain.Task // existing tasks whose record changes
	for id := range editIDs {
		if slices.Contains(a.Drop, id) {
			continue
		}
		t := changed(id)
		if e, ok := a.Edit[id]; ok {
			if e.Title != nil && strings.TrimSpace(*e.Title) != "" {
				t.Title = strings.TrimSpace(*e.Title)
			}
			if e.Description != nil {
				t.Description = *e.Description
			}
			if e.AgentID != nil && *e.AgentID != "" {
				t.AgentID = *e.AgentID
			}
		}
		edited = append(edited, t)
	}
	slices.SortFunc(edited, func(x, y domain.Task) int { return strings.Compare(x.ID, y.ID) })

	// ---- choose where it runs ----
	required := map[string]bool{} // existing tasks a live run must know
	for _, t := range edited {
		required[t.ID] = true
	}
	for _, id := range a.Drop {
		required[id] = true
	}
	for _, t := range fresh {
		for _, d := range t.DependsOn {
			if ex, ok := byID[d]; ok && (ex.Status == domain.TaskPending || ex.Status == domain.TaskRunning || ex.Status == domain.TaskAwaitingApproval) {
				required[d] = true
			}
		}
	}
	for _, t := range edited {
		for _, d := range t.DependsOn {
			if ex, ok := byID[d]; ok && (ex.Status == domain.TaskPending || ex.Status == domain.TaskRunning || ex.Status == domain.TaskAwaitingApproval) {
				required[d] = true
			}
		}
	}
	live := o.liveRuns(requestID)
	var target *liveRun
	for _, lr := range live { // main run first
		if lr.h.Closed() {
			continue
		}
		okAll := true
		for id := range required {
			if !lr.has(id) {
				okAll = false
				break
			}
		}
		if okAll && (target == nil || lr.main) {
			target = lr
		}
	}
	if target == nil && len(required) > 0 {
		for _, lr := range live {
			if !lr.h.Closed() {
				return AmendResult{}, fmt.Errorf("%w: the affected tasks are spread over several running executions; try again when one of them finished", domain.ErrConflict)
			}
		}
	}
	res := AmendResult{Added: newIDs, Requeued: slices.Clone(a.Requeue)}

	if target != nil {
		ok, err := o.amendLive(ctx, target, a, fresh, edited, requeue, byID)
		if err != nil {
			return AmendResult{}, err
		}
		if ok {
			res.Live = true
			o.announceAmend(ctx, req, fresh, edited, requeue, a)
			return res, nil
		}
		// The scheduler ended meanwhile: fall through to a re-open run.
	}
	return o.amendReopen(ctx, req, a, fresh, edited, requeue, byID, res)
}

func idIsNew(newIDs map[string]string, id string) (string, bool) {
	for k, v := range newIDs {
		if v == id {
			return k, true
		}
	}
	return "", false
}

// storeWrites persists the amendment (new tasks, edited tasks, dropped tasks).
// blocked: ids of new/re-queued tasks that must stay blocked (their input is not done).
func (o *Orchestrator) storeWrites(ctx context.Context, fresh, edited []domain.Task, requeue map[string]bool, drop []string, byID map[string]domain.Task, blocked map[string]bool) error {
	org := o.org(ctx)
	for _, t := range fresh {
		if blocked[t.ID] {
			fin := time.Now().UTC()
			t.Status, t.FinishedAt = domain.TaskBlocked, &fin
		}
		if err := o.store.CreateTask(ctx, org, t); err != nil {
			return err
		}
	}
	for _, t := range edited {
		if requeue[t.ID] && !blocked[t.ID] {
			t.Status, t.StartedAt, t.FinishedAt, t.Output = domain.TaskPending, nil, nil, nil
		}
		if err := o.store.UpdateTask(ctx, org, t); err != nil {
			return err
		}
	}
	fin := time.Now().UTC()
	for _, id := range drop {
		t := byID[id]
		if t.Status == domain.TaskBlocked {
			continue
		}
		t.Status, t.FinishedAt = domain.TaskBlocked, &fin
		if err := o.store.UpdateTask(ctx, org, t); err != nil {
			return err
		}
	}
	return nil
}

// amendLive hands the amendment to a running scheduler. ok=false: the
// scheduler had already ended (nothing was changed).
func (o *Orchestrator) amendLive(ctx context.Context, lr *liveRun, a RunAmendment, fresh, edited []domain.Task, requeue map[string]bool, byID map[string]domain.Task) (bool, error) {
	lr.amendMu.Lock()
	defer lr.amendMu.Unlock()
	for _, t := range fresh {
		if _, known := lr.rs.agents[t.AgentID]; !known {
			return false, fmt.Errorf("%w: agent %q is not part of this run (it was hired after the project started)", domain.ErrInvalid, t.AgentID)
		}
	}
	for _, t := range edited {
		if _, known := lr.rs.agents[t.AgentID]; !known {
			return false, fmt.Errorf("%w: agent %q is not part of this run (it was hired after the project started)", domain.ErrInvalid, t.AgentID)
		}
	}
	var up []domain.Task
	up = append(up, fresh...)
	for _, t := range edited {
		// A blocked task that is not re-queued stays decided; the store keeps its new dependencies.
		if byID[t.ID].Status == domain.TaskBlocked && !requeue[t.ID] {
			continue
		}
		up = append(up, t)
	}
	known := map[string]bool{}
	for _, t := range up {
		known[t.ID] = true
	}
	nodes := make([]Node, 0, len(up))
	for _, t := range up {
		deps := t.DependsOn
		if lr.scoped {
			deps = []string{}
			for _, d := range t.DependsOn {
				if known[d] || lr.has(d) {
					deps = append(deps, d)
				}
			}
		}
		nodes = append(nodes, Node{ID: t.ID, DependsOn: deps})
	}
	err := lr.h.Amend(Amendment{Upsert: nodes, Drop: a.Drop, Commit: func() error {
		// Stored first, then known to the run: the scheduler cannot start a node in between.
		if err := o.storeWrites(ctx, fresh, edited, requeue, nil, byID, nil); err != nil {
			return err
		}
		for _, t := range fresh {
			lr.rs.touch(t.AgentID)
			lr.put(t)
		}
		for _, t := range edited {
			if requeue[t.ID] {
				t.Status, t.StartedAt, t.FinishedAt, t.Output = domain.TaskPending, nil, nil, nil
			}
			cur := lr.task(t.ID)
			t.Depth, t.CostUSD = cur.Depth, cur.CostUSD
			lr.put(t)
		}
		for _, id := range a.Drop { // the skip callback blocks them (skipTask uses this copy)
			lr.put(byID[id])
		}
		return nil
	}})
	if errors.Is(err, ErrSchedulerClosed) {
		return false, nil
	}
	if errors.Is(err, ErrNodeStarted) {
		return false, fmt.Errorf("%w: a task of the change started in the meantime: %v", domain.ErrConflict, err)
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// amendReopen runs the amendment when no scheduler is open: the request had
// finished (or failed). The new and re-queued tasks run in a re-open run; the
// ones whose inputs are not done stay blocked.
func (o *Orchestrator) amendReopen(ctx context.Context, req domain.Request, a RunAmendment, fresh, edited []domain.Task, requeue map[string]bool, byID map[string]domain.Task, res AmendResult) (AmendResult, error) {
	if req.Status == domain.RequestPlanning || req.Status == domain.RequestAwaitingConfirmation {
		return AmendResult{}, fmt.Errorf("%w: the request has not started yet", domain.ErrConflict)
	}
	cand := map[string]domain.Task{}
	for _, t := range fresh {
		cand[t.ID] = t
	}
	for _, t := range edited {
		if requeue[t.ID] {
			cand[t.ID] = t
		}
	}
	for changedSet := true; changedSet; { // drop the candidates whose inputs will not be done
		changedSet = false
		for id, t := range cand {
			for _, d := range t.DependsOn {
				if _, in := cand[d]; in {
					continue
				}
				if ex, ok := byID[d]; ok && ex.Status == domain.TaskDone && !contains(a.Drop, d) {
					continue
				}
				delete(cand, id)
				changedSet = true
				break
			}
		}
	}
	blocked := map[string]bool{}
	for _, t := range fresh {
		if _, ok := cand[t.ID]; !ok {
			blocked[t.ID] = true
		}
	}
	for _, t := range edited {
		if requeue[t.ID] {
			if _, ok := cand[t.ID]; !ok {
				blocked[t.ID] = true
			}
		}
	}
	rs, err := o.rebuildRun(ctx, req)
	if err != nil {
		return AmendResult{}, err
	}
	o.applyMeta(ctx, rs)
	for _, t := range fresh {
		if _, known := rs.agents[t.AgentID]; !known {
			return AmendResult{}, fmt.Errorf("%w: unknown agent %q", domain.ErrInvalid, t.AgentID)
		}
	}
	if err := o.storeWrites(ctx, fresh, edited, requeue, a.Drop, byID, blocked); err != nil {
		return AmendResult{}, err
	}
	res.Requeued = res.Requeued[:0]
	var sub []domain.Task
	for _, t := range cand {
		t.Status, t.StartedAt, t.FinishedAt, t.Output = domain.TaskPending, nil, nil, nil
		sub = append(sub, t)
	}
	slices.SortFunc(sub, func(x, y domain.Task) int {
		if c := x.CreatedAt.Compare(y.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(x.ID, y.ID)
	})
	for _, t := range edited {
		if requeue[t.ID] && !blocked[t.ID] {
			res.Requeued = append(res.Requeued, t.ID)
		}
	}
	o.announceAmend(ctx, req, fresh, edited, requeue, a)
	if len(sub) == 0 {
		return res, nil
	}
	restore := domain.RequestStatus("")
	if req.Status == domain.RequestDone || req.Status == domain.RequestFailed {
		restore = req.Status
		o.setRequestStatus(ctx, rs, domain.RequestRunning)
	}
	o.startReopen(ctx, rs, sub, restore)
	return res, nil
}

func contains(ids []string, id string) bool { return slices.Contains(ids, id) }

func (o *Orchestrator) announceAmend(ctx context.Context, req domain.Request, fresh, edited []domain.Task, requeue map[string]bool, a RunAmendment) {
	for _, t := range fresh {
		o.rec.Emit(ctx, Action{Type: domain.EvTaskCreated, AgentID: t.AgentID, Entity: "task", EntityID: t.ID,
			Payload: map[string]any{"task": t, "assigned_reason": t.AssignedReason}})
	}
	for _, t := range edited {
		if requeue[t.ID] {
			o.rec.Emit(ctx, Action{Type: domain.EvTaskRetrying, AgentID: t.AgentID, Entity: "task", EntityID: t.ID, SkipAudit: true,
				Payload: map[string]any{"task": t, "attempt": 1, "manual": true}, Text: "Tarea puesta de nuevo en cola: " + t.Title})
		}
	}
	ids := make([]string, 0, len(fresh))
	for _, t := range fresh {
		ids = append(ids, t.ID)
	}
	o.rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "system"), Action: "request.plan_amended", Entity: "request", EntityID: req.ID, RequestID: req.ID,
		Details: map[string]any{"added": ids, "repointed": len(a.Repoint), "dropped": a.Drop, "requeued": a.Requeue}})
	o.emitMetrics(ctx)
}

// findCycle returns the id of a task on a dependency cycle ("" when acyclic).
// Dependencies on unknown ids are ignored.
func findCycle(deps map[string][]string) string {
	state := map[string]int{}
	var visit func(id string) string
	visit = func(id string) string {
		switch state[id] {
		case 1:
			return id
		case 2:
			return ""
		}
		state[id] = 1
		for _, d := range deps[id] {
			if _, ok := deps[d]; !ok {
				continue
			}
			if c := visit(d); c != "" {
				return c
			}
		}
		state[id] = 2
		return ""
	}
	ids := make([]string, 0, len(deps))
	for id := range deps {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if c := visit(id); c != "" {
			return c
		}
	}
	return ""
}
