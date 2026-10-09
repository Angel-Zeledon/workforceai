package projects

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// ---- launch ----

func describeNode(r Record, n NodeDef) string {
	objective := ""
	for _, o := range r.Objectives {
		if o.ID == n.ObjectiveID {
			objective = displayTitle(o.Title, o.TitleKey, o.TitleParams, r.Locale)
		}
	}
	if n.Description != "" {
		return n.Description
	}
	if r.Locale == "en" {
		return fmt.Sprintf("Project: %s. Objective: %s. Step: %s.", r.Name, objective, n.Title)
	}
	return fmt.Sprintf("Proyecto: %s. Objetivo: %s. Paso: %s.", r.Name, objective, n.Title)
}

// planOf turns the leaf nodes into the plan the orchestrator executes. Humans'
// nodes (gates, milestones, waits) become tasks of the assistant that the gate
// completes without a runtime call.
func planOf(r Record) application.PlanResponse {
	order := topoOrder(leaves(r.Nodes))
	known := map[string]bool{}
	for _, n := range order {
		known[n.ID] = true
	}
	plan := application.PlanResponse{Objectives: []string{r.Goal}, ClarifyingQuestions: []string{}}
	maxLevel := 0
	for _, n := range order {
		agent := "assistant"
		if n.AgentID != nil && *n.AgentID != "" && !n.human() {
			agent = *n.AgentID
		}
		deps := make([]string, 0, len(n.DependsOn))
		for _, d := range n.DependsOn {
			if known[d] {
				deps = append(deps, d)
			}
		}
		plan.Tasks = append(plan.Tasks, application.PlannedTask{Key: n.ID, Title: n.Title,
			Description: describeNode(r, n) + "\n\n" + nodeMarker(n.ID), AgentID: agent, DependsOn: deps})
		maxLevel = max(maxLevel, n.DagLevel)
	}
	plan.MaxDepth = maxLevel + 2
	return plan
}

// checkActiveLimit refuses a launch when the organization already has
// MaxActiveProjects launched, unfinished projects.
func (s *Service) checkActiveLimit(ctx context.Context, org, self string) error {
	recs, err := s.cfg.Store.List(ctx, org)
	if err != nil {
		return err
	}
	active := 0
	for _, r := range recs {
		if r.ID != self && r.Status != StatusDraft && !terminalStatus(r.Status) {
			active++
		}
	}
	if active >= s.cfg.Limits.MaxActiveProjects {
		return limitError(domain.ErrConflict, LimitMaxActive, s.cfg.Limits.MaxActiveProjects, fmt.Sprintf("the organization has %d active projects", active))
	}
	return nil
}

// Launch validates and estimates the draft and submits it as one orchestrator
// request capped at the approved budget. Tasks run in parallel wherever the
// dependencies allow it.
func (s *Service) Launch(ctx context.Context, id string, b LaunchBody) (Summary, error) {
	org := s.org(ctx)
	m := s.lockFor(org, id)
	m.Lock()
	defer m.Unlock()
	rec, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return Summary{}, err
	}
	if rec.Status != StatusDraft {
		return Summary{}, fmt.Errorf("%w: the project is not a draft", domain.ErrConflict)
	}
	if b.ApprovedBudgetUSD <= 0 || math.IsNaN(b.ApprovedBudgetUSD) || math.IsInf(b.ApprovedBudgetUSD, 0) || b.ApprovedBudgetUSD > 1e6 {
		return Summary{}, fmt.Errorf("%w: approved_budget_usd must be between 0 and 1000000", domain.ErrInvalid)
	}
	if rec.Planner != nil && rec.Planner.Status == PlannerRunning && s.jobOf(org, id) != nil {
		return Summary{}, fmt.Errorf("%w: planning_in_progress: the planner is still building this draft", domain.ErrConflict)
	}
	if err := s.cfg.Limits.checkSize(rec.Nodes); err != nil {
		return Summary{}, err
	}
	// MaxActiveProjects: the check and the launch are one step (launchMu), or two launches could both pass.
	s.launchMu.Lock()
	defer s.launchMu.Unlock()
	if err := s.checkActiveLimit(ctx, org, id); err != nil {
		return Summary{}, err
	}
	ids, err := s.agentIDs(ctx)
	if err != nil {
		return Summary{}, err
	}
	if issues := validatePlan(rec.Nodes, ids); hasErrors(issues) {
		codes := make([]string, 0, len(issues))
		for _, i := range issues {
			if i.Severity == "error" {
				codes = append(codes, i.Code)
			}
		}
		return Summary{}, fmt.Errorf("%w: invalid_plan (%s)", domain.ErrInvalid, strings.Join(slices.Compact(codes), ", "))
	}
	est := estimatePlan(rec.Nodes, rec.Objectives)
	if b.ApprovedBudgetUSD < est.Total.P50USD && !b.AcknowledgeUnderbudget {
		return Summary{}, fmt.Errorf("%w: underbudget: the approved budget $%.4f is below the P50 estimate $%.4f", domain.ErrConflict, b.ApprovedBudgetUSD, est.Total.P50USD)
	}
	if b.MaxParallel < 0 || b.MaxParallel > 64 {
		return Summary{}, fmt.Errorf("%w: max_parallel must be between 1 and 64", domain.ErrInvalid)
	}

	// Planned schedule (offsets from the start) and a deadline with 35% slack.
	sim := simulate(simNodes(rec.Nodes))
	for i := range rec.Nodes {
		if rec.Nodes[i].isGroup() {
			continue
		}
		st, en := int64(sim.Start[rec.Nodes[i].ID]*1000), int64(sim.End[rec.Nodes[i].ID]*1000)
		rec.Nodes[i].PlanStartMS, rec.Nodes[i].PlanEndMS = &st, &en
	}
	now := s.now()
	deadline := now.Add(time.Duration(sim.Makespan * 1.35 * float64(time.Second)))
	rec.Estimate, rec.BudgetUSD, rec.MaxParallel = est, b.ApprovedBudgetUSD, orInt(b.MaxParallel, rec.MaxParallel)
	rec.Status, rec.Control, rec.StartedAt, rec.DeadlineAt = StatusRunning, ControlActive, &now, &deadline
	rec.StructureVersion++
	rec.Decisions = map[string]string{}

	s.launching.Add(1)
	defer s.launching.Add(-1)
	reqID, err := s.cfg.Orch.SubmitPlan(application.WithRunParallel(application.WithWorkPriority(application.WithBudgetCap(ctx, rec.BudgetUSD), application.PriorityProject), rec.MaxParallel), rec.Goal, planOf(rec))
	if err != nil {
		return Summary{}, err
	}
	rec.RequestID = reqID
	lp := newLive(rec)
	s.mu.Lock()
	s.live[org+"|"+id] = lp
	s.byReq[org+"|"+reqID] = lp
	s.mu.Unlock()
	if err := s.cfg.Store.Put(ctx, rec); err != nil {
		return Summary{}, err
	}
	s.audit(ctx, "project.launched", id, map[string]any{"request_id": reqID, "budget_usd": rec.BudgetUSD, "tasks": len(leaves(rec.Nodes)),
		"p50_usd": est.Total.P50USD, "underbudget_acknowledged": b.AcknowledgeUnderbudget})
	d, _ := s.detailOf(ctx, rec)
	d.Project.Status = StatusRunning
	s.emit(ctx, "project.status_changed", id, map[string]any{"project": d.Project, "nodes": d.Nodes, "structure_version": rec.StructureVersion})
	go s.watch(lp)
	return d.Project, nil
}

// newLive builds the in-memory state of a launched project from its record.
func newLive(rec Record) *liveProject {
	lp := &liveProject{org: rec.OrgID, id: rec.ID, requestID: rec.RequestID, control: rec.Control, locale: rec.Locale, name: rec.Name,
		nodes: map[string]NodeDef{}, decisions: map[string]string{}, waitSince: map[string]time.Time{}, rev: map[string]int{}, lastSig: map[string]string{}, poke: make(chan struct{}, 1)}
	if lp.control == "" {
		lp.control = ControlActive
	}
	for _, n := range rec.Nodes {
		lp.nodes[n.ID] = n
	}
	for k, v := range rec.Decisions {
		lp.decisions[k] = v
	}
	return lp
}

// ---- restart recovery (A1, application/durable.go) ----

// ResumeRequest implements application.RequestOwner: before the orchestrator
// resumes the request of a launched project after a restart, the project is
// re-attached (gate decisions, pause/cancel, the pending budget extension) and
// its monitor restarts. It returns false when the project cannot continue.
func (s *Service) ResumeRequest(ctx context.Context, org, requestID string) bool {
	rec, err := s.cfg.Store.ByRequest(application.WithOrg(ctx, org), org, requestID)
	if err != nil || terminalStatus(rec.Status) {
		return false
	}
	s.adopt(application.WithOrg(ctx, org), rec)
	return true
}

// Recover re-attaches, after a restart, the launched projects that are not
// finished: the ones whose request the orchestrator resumed are already live
// (ResumeRequest); the others (their request finished or was failed while the
// process stopped) get a monitor that settles their final status. Call it
// after the orchestrator's Recover. The default organization is always included.
func (s *Service) Recover(ctx context.Context, orgs []string) int {
	orgs = application.WithDefaultOrg(orgs, s.cfg.OrgID)
	n := 0
	for _, org := range orgs {
		octx := application.WithOrg(ctx, org)
		recs, err := s.cfg.Store.List(octx, org)
		if err != nil {
			s.cfg.Log.Warn("recover projects", "org", org, "err", err)
			continue
		}
		for _, rec := range recs {
			if rec.RequestID == "" || terminalStatus(rec.Status) {
				continue
			}
			if s.adopt(octx, rec) {
				n++
			}
		}
	}
	s.recovered.Store(true)
	return n
}

// adopt registers a launched project as live and starts its monitor. It
// returns false when it already was live.
func (s *Service) adopt(ctx context.Context, rec Record) bool {
	org := rec.OrgID
	if org == "" {
		org = s.org(ctx)
		rec.OrgID = org
	}
	lp := newLive(rec)
	if snap, err := s.loadSnapshot(ctx, rec); err == nil {
		for _, a := range snap.approvals {
			if a.TaskID == budgetTaskID(rec.ID) && a.Action == ActionExtendBudget && a.Status == domain.ApprovalPending {
				lp.budgetApp = a.ID // its decision is applied by the monitor, as before the restart
			}
		}
	}
	s.mu.Lock()
	if s.live[org+"|"+rec.ID] != nil {
		s.mu.Unlock()
		return false
	}
	s.live[org+"|"+rec.ID] = lp
	s.byReq[org+"|"+rec.RequestID] = lp
	s.mu.Unlock()
	s.audit(application.WithActor(ctx, "system"), "project.resumed", rec.ID, map[string]any{"request_id": rec.RequestID})
	go s.watch(lp)
	return true
}

func orInt(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

// ---- pause / resume / cancel ----

// Control pauses, resumes or cancels a project. Pausing stops new work from
// starting (running calls finish); cancelling also rejects the pending approvals.
func (s *Service) Control(ctx context.Context, id, action string) (Summary, error) {
	var rejectPending bool
	rec, err := s.update(ctx, id, func(r *Record) error {
		st := r.Status
		// The engine persists a terminal status on its next tick; until then the
		// derived status is the truth (the snapshot already reports it).
		if r.RequestID != "" && !terminalStatus(st) && (action == "pause" || action == "cancel") {
			if d, err := s.detailOf(ctx, *r); err == nil && terminalStatus(d.Project.Status) {
				st = d.Project.Status
			}
		}
		switch action {
		case "pause":
			if r.RequestID == "" || terminalStatus(st) {
				return fmt.Errorf("%w: cannot pause a project that is %s", domain.ErrConflict, st)
			}
			r.Control = ControlPaused
		case "resume":
			if r.Control != ControlPaused || terminalStatus(st) {
				return fmt.Errorf("%w: the project is not paused", domain.ErrConflict)
			}
			r.Control = ControlActive
		case "cancel":
			if terminalStatus(st) {
				return fmt.Errorf("%w: the project already finished (%s)", domain.ErrConflict, st)
			}
			now := s.now()
			r.Control, r.Status, r.FinishedAt = ControlCancelled, StatusCancelled, &now
			rejectPending = r.RequestID != ""
		default:
			return fmt.Errorf("%w: action must be pause, resume or cancel", domain.ErrInvalid)
		}
		return nil
	})
	if err != nil {
		return Summary{}, err
	}
	if lp := s.liveOf(rec.OrgID, id); lp != nil {
		lp.mu.Lock()
		lp.control = rec.Control
		lp.mu.Unlock()
		lp.wake()
	}
	s.audit(ctx, "project."+map[string]string{"pause": "paused", "resume": "resumed", "cancel": "cancelled"}[action], id, map[string]any{"request_id": rec.RequestID})
	if rejectPending {
		s.rejectPending(ctx, rec)
	}
	d, err := s.detailOf(ctx, rec)
	if err != nil {
		return Summary{}, err
	}
	s.emit(ctx, "project.status_changed", id, map[string]any{"project": d.Project, "nodes": d.Nodes, "approvals": d.Approvals, "structure_version": rec.StructureVersion})
	return d.Project, nil
}

// rejectPending rejects the pending approvals of a cancelled project (as the
// person who cancelled it), which also releases the tasks waiting on them.
func (s *Service) rejectPending(ctx context.Context, rec Record) {
	snap, err := s.loadSnapshot(ctx, rec)
	if err != nil {
		return
	}
	for _, a := range snap.approvals {
		if a.Status == domain.ApprovalPending {
			if _, err := s.cfg.Approvals.Decide(ctx, a.ID, "reject", "project cancelled"); err != nil {
				s.cfg.Log.Warn("reject approval of cancelled project", "approval", a.ID, "err", err)
			}
		}
	}
}

// SetBudget changes the budget of a project. On a launched project it raises
// (or lowers) the hard cap of its request and wakes the calls paused on it.
func (s *Service) SetBudget(ctx context.Context, id string, usd float64) (Summary, error) {
	if usd < 0 || math.IsNaN(usd) || math.IsInf(usd, 0) || usd > 1e6 {
		return Summary{}, fmt.Errorf("%w: budget_usd must be between 0 and 1000000", domain.ErrInvalid)
	}
	var prev float64
	rec, err := s.update(ctx, id, func(r *Record) error {
		if terminalStatus(r.Status) {
			return fmt.Errorf("%w: the project already finished (%s)", domain.ErrConflict, r.Status)
		}
		prev, r.BudgetUSD = r.BudgetUSD, usd
		if r.RequestID != "" {
			if usd <= 0 {
				return fmt.Errorf("%w: a launched project needs a budget above zero", domain.ErrInvalid)
			}
			return s.cfg.Orch.Budget().SetCap(ctx, domain.ScopeRequest, r.RequestID, usd)
		}
		return nil
	})
	if err != nil {
		return Summary{}, err
	}
	s.liveOf(rec.OrgID, id).wake()
	s.audit(ctx, "project.budget_changed", id, map[string]any{"from_usd": prev, "to_usd": usd})
	d, err := s.detailOf(ctx, rec)
	if err != nil {
		return Summary{}, err
	}
	s.emit(ctx, "project.delta", id, map[string]any{"project": d.Project})
	return d.Project, nil
}

// ---- approvals by batch ----

// BatchResult is the answer of POST /approvals/batch.
type BatchResult struct {
	OK      bool `json:"ok"`
	Count   int  `json:"count"`
	Decided int  `json:"decided"`
}

// DecideBatch approves or rejects every pending approval of one action in a
// project. The caller states how many it expects to be deciding (so a late
// arrival is never approved blind) and high-risk groups need an explicit flag.
// Each decision goes through Approvals.Decide: roles, the "agents never
// approve" rule, double approval and the audit trail all apply.
func (s *Service) DecideBatch(ctx context.Context, b BatchDecision) (BatchResult, error) {
	if b.Filter.ProjectID == "" || b.Filter.Action == "" {
		return BatchResult{}, fmt.Errorf("%w: filter.project_id and filter.action are required", domain.ErrInvalid)
	}
	if b.Decision != "approve" && b.Decision != "reject" {
		return BatchResult{}, fmt.Errorf("%w: decision must be approve or reject", domain.ErrInvalid)
	}
	rec, err := s.get(ctx, b.Filter.ProjectID)
	if err != nil {
		return BatchResult{}, err
	}
	snap, err := s.loadSnapshot(ctx, rec)
	if err != nil {
		return BatchResult{}, err
	}
	var group []domain.Approval
	for _, a := range snap.approvals {
		if a.Status == domain.ApprovalPending && a.Action == b.Filter.Action {
			group = append(group, a)
		}
	}
	if len(group) != b.ExpectedCount {
		return BatchResult{Count: len(group)}, fmt.Errorf("%w: count_mismatch: expected %d pending approvals, found %d", domain.ErrConflict, b.ExpectedCount, len(group))
	}
	for _, a := range group {
		if a.Risk == "high" && !b.IncludeHigh {
			return BatchResult{Count: len(group)}, fmt.Errorf("%w: high_risk_needs_confirmation", domain.ErrConflict)
		}
	}
	res := BatchResult{Count: len(group)}
	for _, a := range group {
		if _, err := s.cfg.Approvals.Decide(ctx, a.ID, b.Decision, "batch decision"); err != nil {
			s.audit(ctx, "project.approvals_batch", rec.ID, map[string]any{"action": b.Filter.Action, "decision": b.Decision, "decided": res.Decided, "error": err.Error()})
			return res, err
		}
		res.Decided++
	}
	res.OK = true
	s.audit(ctx, "project.approvals_batch", rec.ID, map[string]any{"action": b.Filter.Action, "decision": b.Decision, "decided": res.Decided})
	return res, nil
}

// ---- the task gate (application.TaskGate) ----

var (
	_ application.TaskGate     = (*Service)(nil)
	_ application.RequestOwner = (*Service)(nil)
)

func (s *Service) lookupReq(org, requestID string) *liveProject {
	s.mu.Lock()
	lp := s.byReq[org+"|"+requestID]
	s.mu.Unlock()
	return lp
}

// OwnsRequest tells the orchestrator's restart recovery that a request belongs
// to a project: it is resumed only after ResumeRequest re-attached the project,
// so no task runs without its gate.
func (s *Service) OwnsRequest(ctx context.Context, org, requestID string) bool {
	if s.lookupReq(org, requestID) != nil {
		return true
	}
	_, err := s.cfg.Store.ByRequest(application.WithOrg(ctx, org), org, requestID)
	return err == nil
}

// GateTask decides what happens to a task of a launched project: it holds new
// work while the project is paused, refuses it once cancelled, completes
// milestones and timers, and asks a human before gates and approval steps.
func (s *Service) GateTask(ctx context.Context, org string, t domain.Task) application.GateDecision {
	lp := s.lookupReq(org, t.RequestID)
	for wait := 0; lp == nil && s.launching.Load() > 0 && wait < 2500 && ctx.Err() == nil; wait++ {
		// A project being launched registers right after SubmitPlan returned: give it a moment.
		time.Sleep(2 * time.Millisecond)
		lp = s.lookupReq(org, t.RequestID)
	}
	if lp == nil {
		return application.GateDecision{}
	}
	lp.mu.Lock()
	defer lp.mu.Unlock()
	def, ok := lp.nodes[nodeIDOf(t)]
	if !ok {
		return application.GateDecision{}
	}
	switch {
	case lp.control == ControlCancelled:
		return application.GateDecision{Action: application.GateAbort, Reason: "project cancelled"}
	case lp.decisions[t.ID] == "rejected":
		return application.GateDecision{Action: application.GateAbort, Reason: "rejected"}
	case lp.control == ControlPaused:
		return application.GateDecision{Action: application.GateHold, Reason: "project_paused"}
	}
	needsApproval := def.Kind == KindGate || def.ApprovalAction != ""
	switch def.Kind {
	case KindMilestone:
		return application.GateDecision{Action: application.GateComplete, Summary: def.Title}
	case KindWait:
		since, seen := lp.waitSince[t.ID]
		if !seen {
			since = s.now()
			lp.waitSince[t.ID] = since
		}
		if s.now().Sub(since).Seconds() < def.EstSeconds {
			return application.GateDecision{Action: application.GateHold, Reason: "waiting"}
		}
		return application.GateDecision{Action: application.GateComplete, Summary: def.Title}
	}
	if needsApproval && lp.decisions[t.ID] != "approved" {
		action := def.ApprovalAction
		if action == "" {
			action = "approve_plan"
		}
		return application.GateDecision{Action: application.GateApproval, Approval: application.GateApprovalSpec{
			Action: action, Title: actionTitle(lp.locale, action, def.Title), Details: tr(lp.locale, "pv.approval.details", map[string]string{"node": def.Title}),
			Risk: orDefault(def.ApprovalRisk, "medium")}}
	}
	if def.Kind == KindGate {
		return application.GateDecision{Action: application.GateComplete, Summary: def.Title}
	}
	return application.GateDecision{}
}

// GateResolved records the answer to a gate approval (kept in the project record).
func (s *Service) GateResolved(ctx context.Context, org string, t domain.Task, _ string, approved bool) {
	lp := s.lookupReq(org, t.RequestID)
	if lp == nil {
		return
	}
	verdict := map[bool]string{true: "approved", false: "rejected"}[approved]
	lp.mu.Lock()
	lp.decisions[t.ID] = verdict
	lp.mu.Unlock()
	defer lp.wake()
	if _, err := s.update(application.WithOrg(ctx, org), lp.id, func(r *Record) error {
		if r.Decisions == nil {
			r.Decisions = map[string]string{}
		}
		r.Decisions[t.ID] = verdict
		return nil
	}); err != nil {
		s.cfg.Log.Warn("persist gate decision", "project", lp.id, "err", err)
	}
}

// ---- monitor ----

// watch follows a launched project until it and its request finished: it
// confirms the cost estimate the user already approved, raises the
// extend_budget approval when the cap pauses the work, applies its answer and
// publishes project.* events when something changed.
func (s *Service) watch(lp *liveProject) {
	ctx := application.WithOrg(s.root, lp.org)
	for {
		done, changed := s.step(ctx, lp)
		if done {
			s.mu.Lock()
			delete(s.live, lp.org+"|"+lp.id)
			delete(s.byReq, lp.org+"|"+lp.requestID)
			s.mu.Unlock()
			return
		}
		delay := s.nextDelay(lp, changed)
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-lp.poke:
			lp.idleTicks = 0
		case <-t.C:
		}
		t.Stop()
	}
}

// step runs one monitor iteration. It returns done=true when everything
// finished and changed=true when the project view changed (it resets the
// polling backoff). An unchanged snapshot skips the rebuild and the publish
// (monitor.go).
func (s *Service) step(ctx context.Context, lp *liveProject) (done, changed bool) {
	rec, err := s.cfg.Store.Get(ctx, lp.org, lp.id)
	if err != nil {
		return ctx.Err() != nil, false
	}
	snap, err := s.loadSnapshot(ctx, rec)
	if err != nil {
		return false, false
	}
	if snap.req == nil {
		if !errors.Is(snap.reqErr, domain.ErrNotFound) {
			return false, false // transient store error: try again next tick
		}
		if !terminalStatus(rec.Status) {
			// The request disappeared (demo reset): the project cannot continue.
			now := s.now()
			_, _ = s.update(ctx, rec.ID, func(r *Record) error {
				r.Status, r.Control, r.FinishedAt, r.Error = StatusFailed, ControlCancelled, &now, "the request was removed"
				return nil
			})
		}
		return true, true
	}
	reqDone := snap.req.Status == domain.RequestDone || snap.req.Status == domain.RequestFailed
	if !s.snapshotChanged(lp, snap) {
		return terminalStatus(rec.Status) && reqDone, false
	}
	if snap.req.Status == domain.RequestAwaitingConfirmation {
		// The budget was approved at launch: the cost confirmation of the request is implicit.
		_ = s.cfg.Orch.Budget().Confirm(ctx, rec.RequestID, true, 0)
	}
	s.handleBudget(ctx, lp, &rec, snap)
	d := s.build(ctx, snap)
	changed = s.publish(ctx, lp, &rec, snap, d)
	if s.checkBudgetAlert(ctx, &rec, d) {
		changed = true
	}
	return terminalStatus(rec.Status) && reqDone, changed
}

// handleBudget turns a budget pause into an approval and applies its decision.
func (s *Service) handleBudget(ctx context.Context, lp *liveProject, rec *Record, snap snapshot) {
	bid := budgetTaskID(rec.ID)
	var pending *domain.Approval
	for i := range snap.approvals {
		a := snap.approvals[i]
		if a.TaskID == bid && a.Action == ActionExtendBudget && a.Status == domain.ApprovalPending {
			pending = &a
		}
	}
	// Apply the decisions taken since the last tick.
	lp.mu.Lock()
	watching := lp.budgetApp
	lp.mu.Unlock()
	if watching != "" {
		for _, a := range snap.approvals {
			if a.ID != watching || a.Status == domain.ApprovalPending {
				continue
			}
			lp.mu.Lock()
			lp.budgetApp = ""
			lp.mu.Unlock()
			if a.Status == domain.ApprovalApproved {
				s.extendBudget(ctx, rec)
			} else if _, err := s.Control(application.WithActor(ctx, "system"), rec.ID, "cancel"); err != nil {
				s.cfg.Log.Warn("cancel project after rejected budget extension", "project", rec.ID, "err", err)
			}
		}
	}
	if snap.req.Status != domain.RequestPaused || pending != nil || terminalStatus(rec.Status) {
		return
	}
	ap := domain.Approval{TaskID: bid, AgentID: "assistant", Action: ActionExtendBudget, Risk: "medium",
		Title: tr(lp.locale, "pv.action.extend_budget", nil), Details: tr(lp.locale, "pv.approval.budgetDetails", nil)}
	created, _, err := s.cfg.Approvals.Request(ctx, ap)
	if err != nil {
		s.cfg.Log.Warn("budget approval", "project", rec.ID, "err", err)
		return
	}
	lp.mu.Lock()
	lp.budgetApp = created.ID
	lp.mu.Unlock()
}

// extendBudget raises the request cap by 25% over what the blocked call needs.
func (s *Service) extendBudget(ctx context.Context, rec *Record) {
	cap := rec.BudgetUSD
	if st, err := s.cfg.Orch.Budget().Status(ctx); err == nil {
		for _, p := range st.Pauses {
			if p.Scope == domain.ScopeRequest && p.ScopeID == rec.RequestID {
				cap = math.Max(cap, p.NeededUSD)
			}
		}
	}
	cap = math.Round(cap*1.25*1e6) / 1e6
	actx := application.WithActor(ctx, "system")
	if _, err := s.SetBudget(actx, rec.ID, cap); err != nil {
		s.cfg.Log.Warn("extend project budget", "project", rec.ID, "err", err)
		return
	}
	rec.BudgetUSD = cap
}

// publish emits the project.* events for what changed since the last tick,
// persists the terminal status and feeds the workspace sink.
func (s *Service) publish(ctx context.Context, lp *liveProject, rec *Record, snap snapshot, d Detail) bool {
	lp.mu.Lock()
	var changed []Node
	for i := range d.Nodes {
		n := &d.Nodes[i]
		sg := sig(*n)
		if lp.lastSig[n.ID] == sg {
			continue
		}
		lp.lastSig[n.ID] = sg
		lp.rev[n.ID]++
		n.Rev = lp.rev[n.ID]
		changed = append(changed, *n)
	}
	lp.mu.Unlock()

	lp.mu.Lock()
	statusChanged := d.Project.Status != lp.lastStatus
	lp.lastStatus = d.Project.Status
	lp.mu.Unlock()
	if terminalStatus(d.Project.Status) && !terminalStatus(rec.Status) {
		now := s.now()
		fin := &now
		if d.Project.Status == StatusDone && rec.FinishedAt == nil {
			rec.FinishedAt = fin
		}
		upd, err := s.update(ctx, rec.ID, func(r *Record) error {
			if terminalStatus(r.Status) {
				return nil
			}
			r.Status = d.Project.Status
			if r.FinishedAt == nil {
				r.FinishedAt = fin
			}
			return nil
		})
		if err == nil {
			*rec = upd
			d.Project.FinishedAt = upd.FinishedAt
		}
		s.audit(application.WithActor(ctx, "system"), "project."+d.Project.Status, rec.ID, map[string]any{"request_id": rec.RequestID,
			"done": d.Project.TasksDone, "total": d.Project.TasksTotal, "spent_usd": d.Project.SpentUSD})
	}
	if len(changed) > 0 || statusChanged {
		typ := "project.delta"
		if statusChanged {
			typ = "project.status_changed"
		}
		s.emit(ctx, typ, rec.ID, map[string]any{"project": d.Project, "nodes": changed, "approvals": d.Approvals, "structure_version": d.StructureVersion})
	}
	if s.cfg.Sink == nil {
		return len(changed) > 0 || statusChanged
	}
	for _, n := range changed {
		if n.Kind == KindGroup || n.AgentID == nil || n.Kind == KindGate || n.Kind == KindMilestone || n.Kind == KindWait {
			continue
		}
		ev := NodeEvent{ProjectID: rec.ID, ProjectName: rec.Name, NodeID: n.ID, ObjectiveID: n.ObjectiveID, Kind: n.Kind, Title: n.Title,
			AgentID: *n.AgentID, State: n.State, Progress: n.Progress, TaskID: n.TaskID, RequestID: rec.RequestID}
		if t, ok := snap.tasks[n.ID]; ok && n.State == StateDone {
			ev.Output = t.Output
		}
		s.cfg.Sink.NodeChanged(ctx, ev)
	}
	return len(changed) > 0 || statusChanged
}
