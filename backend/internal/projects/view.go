package projects

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// The live state of a launched project is never stored twice: it is derived at
// read time from the orchestrator (the tasks of its request, the request status
// and the approvals), so the project view can never disagree with the rest of
// the product (budget pauses, kill switch, approvals...).

// A launched leaf node is a task whose description ends with this marker; it is
// how the tasks of the request are matched back to the nodes.
var markerRe = regexp.MustCompile(`\[project-node:([^\]]+)\]`)

func nodeMarker(nodeID string) string { return "[project-node:" + nodeID + "]" }

func nodeIDOf(t domain.Task) string {
	m := markerRe.FindStringSubmatch(t.Description)
	if m == nil {
		return ""
	}
	return m[1]
}

// budgetTaskID is the task id of the extend_budget approval of a project.
func budgetTaskID(projectID string) string { return "project:" + projectID }

type snapshot struct {
	rec       Record
	req       *domain.Request
	reqErr    error                  // why req is nil
	tasks     map[string]domain.Task // by node id
	taskNode  map[string]string      // task id -> node id
	approvals []domain.Approval      // of this project
}

func (s *Service) loadSnapshot(ctx context.Context, rec Record) (snapshot, error) {
	snap := snapshot{rec: rec, tasks: map[string]domain.Task{}, taskNode: map[string]string{}}
	if rec.RequestID == "" {
		return snap, nil
	}
	org := rec.OrgID
	req, err := s.cfg.Core.GetRequest(ctx, org, rec.RequestID)
	if err == nil {
		snap.req = &req
	} else {
		snap.reqErr = err
	}
	tasks, err := s.cfg.Core.ListTasksByRequest(ctx, org, rec.RequestID)
	if err != nil {
		return snap, err
	}
	for _, t := range tasks {
		if id := nodeIDOf(t); id != "" {
			snap.tasks[id] = t
			snap.taskNode[t.ID] = id
		}
	}
	if l, ok := s.cfg.Core.(application.RequestApprovalLister); ok {
		// Filtered at the store: the cost no longer grows with the org's approvals.
		all, err := l.ListApprovalsByRequest(ctx, org, rec.RequestID, []string{budgetTaskID(rec.ID)})
		if err != nil {
			return snap, err
		}
		for _, a := range all {
			if _, ok := snap.taskNode[a.TaskID]; ok || a.TaskID == budgetTaskID(rec.ID) {
				snap.approvals = append(snap.approvals, a)
			}
		}
		return snap, nil
	}
	all, err := s.cfg.Core.ListApprovals(ctx, org, "")
	if err != nil {
		return snap, err
	}
	for _, a := range all {
		if _, ok := snap.taskNode[a.TaskID]; ok || a.TaskID == budgetTaskID(rec.ID) {
			snap.approvals = append(snap.approvals, a)
		}
	}
	return snap, nil
}

func ptrStr(s string) *string { return &s }

// build derives the full Detail of a project from its snapshot.
func (s *Service) build(ctx context.Context, snap snapshot) Detail {
	rec := snap.rec
	d := Detail{Objectives: append([]Objective{}, rec.Objectives...), Nodes: []Node{}, Approvals: []Approval{},
		Budget: rec.Budget, Estimate: rec.Estimate, StructureVersion: rec.StructureVersion, MaxParallel: rec.MaxParallel}
	if d.Budget.WarnAt == nil {
		d.Budget = defaultBudgetPolicy()
	}
	launched := rec.RequestID != ""
	lp := s.liveOf(rec.OrgID, rec.ID)

	// 1) raw state of every leaf from its task
	raw := map[string]string{}
	for _, def := range rec.Nodes {
		if def.isGroup() {
			continue
		}
		raw[def.ID] = rawState(launched, snap.tasks[def.ID], snap.tasks, def.ID)
	}
	// 2) final state with dependencies, pause, guard
	nodes := make([]Node, 0, len(rec.Nodes))
	var spent float64
	for _, def := range rec.Nodes {
		n := Node{ID: def.ID, ProjectID: rec.ID, ObjectiveID: def.ObjectiveID, ParentID: def.ParentID, Kind: def.Kind, Title: def.Title,
			TitleKey: def.TitleKey, TitleParams: def.TitleParams, AgentID: def.AgentID, DependsOn: nonNil(def.DependsOn), DelegationDepth: def.DelegationDepth,
			DelegationChain: nonNil(def.DelegationChain), DagLevel: def.DagLevel, WBSPath: def.WBSPath, Complexity: def.Complexity,
			EstCostUSD: def.EstCostUSD, EstSeconds: def.EstSeconds, PlanStartMS: def.PlanStartMS, PlanEndMS: def.PlanEndMS,
			ApprovalAction: def.ApprovalAction, MaxAttempts: s.cfg.MaxAttempts, State: StateDraft}
		if def.isGroup() {
			nodes = append(nodes, n)
			continue
		}
		if !launched {
			nodes = append(nodes, n)
			continue
		}
		t, has := snap.tasks[def.ID]
		n.State = s.finalState(ctx, rec, def, raw, t.ID)
		if has {
			n.TaskID, n.CostUSD, n.StartedAt, n.FinishedAt = t.ID, t.CostUSD, t.StartedAt, t.FinishedAt
			spent += t.CostUSD
			if t.StartedAt != nil {
				n.Attempt = 1
			}
			if t.Output != nil {
				if a, ok := t.Output.Metrics["attempts"].(float64); ok && a > 1 { // task-level retry (W2)
					n.Attempt = int(a)
				} else if a, ok := t.Output.Metrics["attempts"].(int); ok && a > 1 {
					n.Attempt = a
				}
				if sk, _ := t.Output.Metrics["skipped"].(bool); sk {
					n.Skipped = true
					n.SkipReason, _ = t.Output.Metrics["skip_reason"].(string)
				}
			}
		}
		switch n.State {
		case StateDone:
			n.Progress = 100
		case StateRunning:
			n.Progress = 50
		case StateAwaitingApproval:
			n.Progress = 80
		}
		switch {
		case n.State == StateBlocked && rec.Decisions[n.TaskID] == "rejected":
			n.Error = ptrStr("rejected")
		case n.State == StateBlocked:
			n.Error = ptrStr("dependency")
		case n.State == StateFailed:
			n.Error = ptrStr("failed")
		}
		if lp != nil {
			n.Rev = lp.revOf(n)
		}
		nodes = append(nodes, n)
	}
	// 3) groups roll up their children
	byParent := map[string][]Node{}
	for _, n := range nodes {
		if n.ParentID != nil {
			byParent[*n.ParentID] = append(byParent[*n.ParentID], n)
		}
	}
	for i := range nodes {
		g := &nodes[i]
		if g.Kind != KindGroup {
			continue
		}
		kids := byParent[g.ID]
		if !launched {
			g.State = StateDraft
			continue
		}
		g.State = rollupState(kids)
		done, total := 0, 0
		for _, k := range kids {
			if k.Kind == KindGroup {
				continue
			}
			total++
			if isDone(k.State) {
				done++
			}
		}
		if total > 0 {
			g.Progress = done * 100 / total
		}
	}
	d.Nodes = nodes

	// approvals
	for _, a := range snap.approvals {
		pa := Approval{ID: a.ID, ProjectID: rec.ID, NodeID: snap.taskNode[a.TaskID], Action: a.Action, Title: a.Title, Details: a.Details,
			Risk: a.Risk, Status: string(a.Status), CreatedAt: a.CreatedAt, ResolvedAt: a.ResolvedAt}
		if a.AgentID != "" {
			pa.AgentID = ptrStr(a.AgentID)
		}
		d.Approvals = append(d.Approvals, pa)
	}

	// summary
	c := countStates(nodes)
	sum := Summary{ID: rec.ID, Name: rec.Name, NameKey: rec.NameKey, Goal: rec.Goal, Status: rec.Status, Control: rec.Control, TemplateID: rec.TemplateID,
		BudgetUSD: rec.BudgetUSD, TasksDone: c.done, TasksTotal: c.total, Running: c.running, Awaiting: c.awaiting, Failed: c.failed, Light: "green",
		CreatedAt: rec.CreatedAt, StartedAt: rec.StartedAt, FinishedAt: rec.FinishedAt, DeadlineAt: rec.DeadlineAt, ObjectivesCnt: len(rec.Objectives), RequestID: rec.RequestID, BudgetWarnPct: warnedPct(rec.BudgetWarned)}
	if sum.Control == "" {
		sum.Control = ControlActive
	}
	if launched {
		sum.SpentUSD = spent
		if snap.req != nil && snap.req.CostUSD > spent {
			sum.SpentUSD = snap.req.CostUSD
		}
		if !terminalStatus(rec.Status) {
			sum.Status = deriveStatus(rec, snap.req, nodes, d.Approvals, c)
		}
	}
	d.Project = sum
	if launched && sum.Status != StatusDraft {
		h := computeHealth(d, s.now())
		d.Project.Light = h.Light
		if sum.Status == StatusDone {
			d.Project.Light = "green"
		}
	}
	return d
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func terminalStatus(st string) bool {
	return st == StatusDone || st == StatusFailed || st == StatusCancelled
}

func terminalState(st string) bool {
	return st == StateDone || st == StateFailed || st == StateBlocked || st == StateCancelled
}

// rawState maps the task of a node to a coarse state, ignoring dependencies.
func rawState(launched bool, t domain.Task, tasks map[string]domain.Task, nodeID string) string {
	if !launched {
		return StateDraft
	}
	if _, ok := tasks[nodeID]; !ok {
		return StatePending
	}
	switch t.Status {
	case domain.TaskDone:
		return StateDone
	case domain.TaskFailed:
		return StateFailed
	case domain.TaskBlocked:
		return StateBlocked
	case domain.TaskRunning:
		return StateRunning
	case domain.TaskAwaitingApproval:
		return StateAwaitingApproval
	}
	return StatePending
}

// finalState resolves a pending node against its dependencies, a paused or
// cancelled project and the execution guard (kill switch, agent pause).
func (s *Service) finalState(ctx context.Context, rec Record, def NodeDef, raw map[string]string, taskID string) string {
	st := raw[def.ID]
	cancelled := rec.Control == ControlCancelled
	switch st {
	case StateBlocked:
		if cancelled && rec.Decisions[taskID] != "rejected" {
			return StateCancelled
		}
		return StateBlocked
	case StatePending:
		if cancelled {
			return StateCancelled
		}
		depsDone, depDead := true, false
		for _, dep := range def.DependsOn {
			ds, ok := raw[dep]
			if !ok {
				continue
			}
			if ds != StateDone {
				depsDone = false
			}
			if ds == StateFailed || ds == StateBlocked || ds == StateCancelled {
				depDead = true
			}
		}
		switch {
		case depDead:
			return StateBlocked
		case !depsDone:
			return StatePending
		case rec.Control == ControlPaused:
			return StatePaused
		case def.AgentID != nil && s.cfg.Guard != nil && !s.cfg.Guard.Admit(ctx, rec.OrgID, *def.AgentID).Allowed:
			return StatePaused
		case def.Kind == KindWait:
			return "waiting"
		}
		return StateReady
	}
	return st
}

// deriveStatus is the status of a launched, not yet finished project.
func deriveStatus(rec Record, req *domain.Request, nodes []Node, approvals []Approval, c counts) string {
	if rec.Control == ControlCancelled {
		return StatusCancelled
	}
	if c.total > 0 && c.done == c.total {
		return StatusDone
	}
	allTerminal := c.total > 0
	active, ready := 0, 0
	for _, n := range nodes {
		if n.Kind == KindGroup {
			continue
		}
		if !terminalState(n.State) {
			allTerminal = false
		}
		switch n.State {
		case StateRunning, StateAwaitingApproval:
			active++
		case StateReady, "waiting":
			ready++
		}
	}
	reqFailed := req != nil && req.Status == domain.RequestFailed
	if allTerminal || (reqFailed && active == 0) {
		return StatusFailed
	}
	if rec.Control == ControlPaused {
		return StatusPaused
	}
	pending := 0
	for _, a := range approvals {
		if a.Status == "pending" {
			pending++
		}
	}
	if (req != nil && req.Status == domain.RequestPaused) || (pending > 0 && active == 0 && ready == 0) {
		return StatusWaitingHuman
	}
	return StatusRunning
}

func (s *Service) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now().UTC()
}

// ---- live (in-process) state of a launched project ----

// liveProject is what the gate and the monitor of a launched project share.
type liveProject struct {
	mu         sync.Mutex
	org, id    string
	requestID  string
	control    string
	locale     string
	name       string
	nodes      map[string]NodeDef // by node id
	decisions  map[string]string  // task id -> approved|rejected
	waitSince  map[string]time.Time
	rev        map[string]int
	lastSig    map[string]string
	budgetApp  string // id of the pending extend_budget approval
	lastStatus string
	epoch      int // bumped (under Service.mu) when a finished project is revived; the monitor must not exit across a bump

	// monitor state (only the watch goroutine writes it)
	stepEpoch int           // epoch observed when the current tick started
	poke      chan struct{} // wakes the monitor early (human action, cap change)
	lastFP    string        // fingerprint of the last snapshot that was built
	lastFull  time.Time     // when the last full rebuild ran
	idleTicks int           // consecutive ticks without a change
}

// wake nudges the monitor so it re-reads the project now instead of after the
// idle backoff. Never blocks.
func (lp *liveProject) wake() {
	if lp == nil {
		return
	}
	select {
	case lp.poke <- struct{}{}:
	default:
	}
}

func (lp *liveProject) revOf(n Node) int {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	return lp.rev[n.ID]
}

func sig(n Node) string {
	return strings.Join([]string{n.State, itoa(n.Progress), ftoa(n.CostUSD), itoa(n.Attempt)}, "|")
}

func itoa(i int) string     { return strconv.Itoa(i) }
func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', 6, 64) }
