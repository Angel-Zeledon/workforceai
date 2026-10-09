package projects

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/google/uuid"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// changeSpec is what creates a proposal.
type changeSpec struct {
	proposedBy, actor, source, sourceNode, reason string
	ops                                           []PlanChangeOp
	approvalAgent                                 string // agent id on the approval (the proposing agent)
}

func (s *Service) emitChange(ctx context.Context, projectID string, c PlanChange) {
	s.emit(ctx, "project.plan_change", projectID, map[string]any{"project_id": projectID, "change": c})
}

func (s *Service) changeState(ctx context.Context, rec Record) (Detail, map[string]string, error) {
	snap, err := s.loadSnapshot(ctx, rec)
	if err != nil {
		return Detail{}, nil, err
	}
	d := s.build(ctx, snap)
	st := make(map[string]string, len(d.Nodes))
	for _, n := range d.Nodes {
		st[n.ID] = n.State
	}
	return d, st, nil
}

func (s *Service) changeable(rec Record, d Detail) error {
	if rec.RequestID == "" {
		return fmt.Errorf("%w: the project has not been launched: edit its draft instead", domain.ErrConflict)
	}
	if rec.Control == ControlCancelled || rec.Status == StatusCancelled || d.Project.Status == StatusCancelled {
		return fmt.Errorf("%w: the project is cancelled", domain.ErrConflict)
	}
	if rec.Status == StatusDone || d.Project.Status == StatusDone {
		return fmt.Errorf("%w: the project already finished", domain.ErrConflict)
	}
	if s.cfg.Orch == nil {
		return fmt.Errorf("%w: plan changes are not available", domain.ErrConflict)
	}
	return nil
}

// ListChanges returns the plan changes of a project (oldest first). Decisions
// taken in the approvals inbox are picked up here.
func (s *Service) ListChanges(ctx context.Context, id string) ([]PlanChange, error) {
	rec, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	s.syncChanges(ctx, rec.ID)
	out, err := s.cfg.Changes.ListChanges(ctx, rec.OrgID, id)
	if out == nil {
		out = []PlanChange{}
	}
	return out, err
}

// ProposeChange records a change a person makes directly from the project
// view. It is auto-approved by that person (unless the policy asks for double
// approval) and applied at once.
func (s *Service) ProposeChange(ctx context.Context, id string, in NewPlanChange) (PlanChange, error) {
	actor := application.ActorFrom(ctx, "user")
	if application.IsAgentPrincipal(actor) {
		return PlanChange{}, fmt.Errorf("%w: only humans can change the plan directly; agents propose", domain.ErrForbidden)
	}
	return s.newChange(ctx, id, changeSpec{proposedBy: ProposerHuman, actor: actor, source: SourceManual,
		reason: cleanText(in.Reason, maxChangeReason), ops: in.Ops})
}

func (s *Service) newChange(ctx context.Context, id string, sp changeSpec) (PlanChange, error) {
	rec0, err := s.get(ctx, id)
	if err != nil {
		return PlanChange{}, err
	}
	org := rec0.OrgID
	m := s.lockFor(org, id)
	m.Lock()
	defer m.Unlock()
	rec, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return PlanChange{}, err
	}
	d, states, err := s.changeState(ctx, rec)
	if err != nil {
		return PlanChange{}, err
	}
	if err := s.changeable(rec, d); err != nil {
		return PlanChange{}, err
	}
	agents, err := s.agentIDs(ctx)
	if err != nil {
		return PlanChange{}, err
	}
	existing, err := s.cfg.Changes.ListChanges(ctx, org, id)
	if err != nil {
		return PlanChange{}, err
	}
	now := s.now()
	ch := PlanChange{ID: "pc-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12], ProjectID: id, OrgID: org, ProposedBy: sp.proposedBy, Actor: sp.actor,
		Source: sp.source, SourceNodeID: sp.sourceNode, Reason: sp.reason, Ops: sp.ops, Status: ChangePending, CreatedAt: now, UpdatedAt: now}
	plan, err := s.planChange(rec, states, agents, d.Project.SpentUSD, ch)
	if err != nil {
		return PlanChange{}, err
	}
	ch.Impact, ch.Risk = plan.impact, riskOf(plan.impact)
	agent := sp.approvalAgent
	if agent == "" {
		agent = "assistant"
	}
	required, role, noSelf, rule := 1, "", false, ""
	if s.cfg.Orch != nil {
		required, role, noSelf, rule = s.cfg.Orch.ApprovalGovernance(ctx, agent, "projects", ActionPlanChange, ch.Risk, math.Abs(ch.Impact.EstCostDeltaUSD))
	}
	if sp.proposedBy == ProposerHuman && required <= 1 {
		// A person's own change: recorded and auto-approved by that person.
		ch.Status, ch.DecidedBy, ch.DecidedAt = ChangeApproved, sp.actor, &now
		if err := s.cfg.Changes.PutChange(ctx, ch); err != nil {
			return PlanChange{}, err
		}
		s.audit(ctx, "project.plan_change_proposed", id, s.changeAudit(ch))
		s.audit(ctx, "project.plan_change_approved", id, map[string]any{"change_id": ch.ID, "by": sp.actor, "auto": true})
		return s.applyLocked(ctx, rec, ch)
	}
	open := 0
	for _, c := range existing {
		if c.open() {
			open++
		}
	}
	if open >= s.cfg.Limits.MaxOpenPlanChanges {
		return PlanChange{}, limitError(domain.ErrConflict, LimitOpenChanges, s.cfg.Limits.MaxOpenPlanChanges, fmt.Sprintf("the project has %d open plan changes: decide them first", open))
	}
	if s.cfg.Approvals == nil {
		return PlanChange{}, fmt.Errorf("%w: approvals are not available", domain.ErrConflict)
	}
	requestedBy := ""
	if sp.proposedBy == ProposerHuman {
		requestedBy = sp.actor
	}
	ap := domain.Approval{TaskID: changeTaskIDPrefix + ch.ID, AgentID: agent, Action: ActionPlanChange, Risk: ch.Risk,
		Title:   tr(rec.Locale, "pv.action."+ActionPlanChange, map[string]string{"node": truncRunes(ch.Reason, 120)}),
		Details: tr(rec.Locale, "pv.planchange.details", map[string]string{"summary": summaryOf(ch), "cost": fmt.Sprintf("%+.4f", ch.Impact.EstCostDeltaUSD)}),
		Context: map[string]any{"project_id": id, approvalPlanChangeOf: ch.ID}, RequiredApprovals: required, RequiredRole: role, NoSelfApproval: noSelf,
		PolicyRule: rule, RequestedBy: requestedBy}
	created, _, err := s.cfg.Approvals.Request(ctx, ap)
	if err != nil {
		return PlanChange{}, err
	}
	ch.ApprovalID, ch.RequiredApprovals = created.ID, created.RequiredApprovals
	if err := s.cfg.Changes.PutChange(ctx, ch); err != nil {
		return PlanChange{}, err
	}
	if lp := s.liveOf(org, id); lp != nil {
		lp.mu.Lock()
		lp.openChanges = true
		lp.mu.Unlock()
	}
	s.audit(ctx, "project.plan_change_proposed", id, s.changeAudit(ch))
	s.emitChange(ctx, id, ch)
	return ch, nil
}

func summaryOf(c PlanChange) string {
	im := c.Impact
	return fmt.Sprintf("+%d / ~%d / -%d / edit %d", im.Added, im.Replaced, im.Removed, im.Updated)
}

func (s *Service) changeAudit(c PlanChange) map[string]any {
	return map[string]any{"change_id": c.ID, "proposed_by": c.ProposedBy, "actor": c.Actor, "source": c.Source, "source_node_id": c.SourceNodeID,
		"status": c.Status, "risk": c.Risk, "approval_id": c.ApprovalID, "impact": c.Impact, "ops": len(c.Ops), "reason": c.Reason}
}

// DecideChange approves or rejects a proposal. The decision goes through the
// approvals mechanism (Approvals.Decide): roles, double approval, the "agents
// never approve" rule and the audit trail apply. An approved change is applied.
func (s *Service) DecideChange(ctx context.Context, id, changeID, decision, note string) (PlanChange, error) {
	rec, err := s.get(ctx, id)
	if err != nil {
		return PlanChange{}, err
	}
	ch, err := s.cfg.Changes.GetChange(ctx, rec.OrgID, id, changeID)
	if err != nil {
		return PlanChange{}, err
	}
	if !ch.open() || ch.ApprovalID == "" {
		return ch, fmt.Errorf("%w: the plan change is %s", domain.ErrConflict, ch.Status)
	}
	if s.cfg.Approvals == nil {
		return ch, fmt.Errorf("%w: approvals are not available", domain.ErrConflict)
	}
	if _, err := s.cfg.Approvals.Decide(ctx, ch.ApprovalID, decision, note); err != nil {
		return ch, err
	}
	s.syncChanges(ctx, id)
	return s.cfg.Changes.GetChange(ctx, rec.OrgID, id, changeID)
}

// syncChanges reads the decisions of the open proposals (taken here or in the
// approvals inbox) and applies the approved ones; it also finishes a change
// that was approved but not applied before a restart.
func (s *Service) syncChanges(ctx context.Context, id string) (applied bool) {
	org := s.org(ctx)
	m := s.lockFor(org, id)
	m.Lock()
	defer m.Unlock()
	rec, err := s.cfg.Store.Get(ctx, org, id)
	if err != nil {
		return false
	}
	list, err := s.cfg.Changes.ListChanges(ctx, org, id)
	if err != nil {
		return false
	}
	stillOpen := false
	for _, ch := range list {
		switch {
		case ch.Status == ChangePending && ch.ApprovalID != "":
			ap, err := s.cfg.Core.GetApproval(ctx, org, ch.ApprovalID)
			if err != nil {
				stillOpen = true
				continue
			}
			now := s.now()
			switch ap.Status {
			case domain.ApprovalPending:
				stillOpen = true
				if n := len(ap.Decisions); n != ch.Approvals {
					ch.Approvals, ch.UpdatedAt = n, now
					_ = s.cfg.Changes.PutChange(ctx, ch)
					s.emitChange(ctx, id, ch)
				}
			case domain.ApprovalApproved:
				ch.Status, ch.DecidedAt, ch.UpdatedAt, ch.Approvals = ChangeApproved, &now, now, len(ap.Decisions)
				if k := len(ap.Decisions); k > 0 {
					ch.DecidedBy = ap.Decisions[k-1].By
				}
				_ = s.cfg.Changes.PutChange(ctx, ch)
				s.audit(ctx, "project.plan_change_approved", id, map[string]any{"change_id": ch.ID, "by": ch.DecidedBy, "approval_id": ch.ApprovalID})
				if _, err := s.applyLocked(ctx, rec, ch); err == nil {
					applied = true
				}
				if r, err := s.cfg.Store.Get(ctx, org, id); err == nil {
					rec = r
				}
			default:
				ch.Status, ch.DecidedAt, ch.UpdatedAt = ChangeRejected, &now, now
				if k := len(ap.Decisions); k > 0 {
					ch.DecidedBy = ap.Decisions[k-1].By
				}
				_ = s.cfg.Changes.PutChange(ctx, ch)
				s.audit(ctx, "project.plan_change_rejected", id, map[string]any{"change_id": ch.ID, "approval_id": ch.ApprovalID})
				s.emitChange(ctx, id, ch)
			}
		case ch.Status == ChangeApproved:
			if _, err := s.applyLocked(ctx, rec, ch); err == nil {
				applied = true
			}
			if r, err := s.cfg.Store.Get(ctx, org, id); err == nil {
				rec = r
			}
		}
	}
	if lp := s.liveOf(org, id); lp != nil {
		lp.mu.Lock()
		lp.openChanges = stillOpen
		lp.mu.Unlock()
	}
	return applied
}

// rejectChange marks an approved change as not applicable.
func (s *Service) failChange(ctx context.Context, ch PlanChange, why error) PlanChange {
	ch.Status, ch.Error, ch.UpdatedAt = ChangeRejected, clip(why.Error(), 300), s.now()
	_ = s.cfg.Changes.PutChange(ctx, ch)
	s.audit(ctx, "project.plan_change_failed", ch.ProjectID, map[string]any{"change_id": ch.ID, "error": ch.Error})
	s.emitChange(ctx, ch.ProjectID, ch)
	return ch
}

// applyLocked applies an approved change to the running project. The caller
// holds the project lock. The plan is validated again against the live DAG;
// the record is persisted first (a restart finds the nodes), then the tasks
// and the running scheduler are amended; if that is refused the record is
// restored and the change is rejected with the reason.
func (s *Service) applyLocked(ctx context.Context, rec Record, ch PlanChange) (PlanChange, error) {
	org := rec.OrgID
	if ch.Status != ChangeApproved {
		return ch, fmt.Errorf("%w: the plan change is %s", domain.ErrConflict, ch.Status)
	}
	d, states, err := s.changeState(ctx, rec)
	if err != nil {
		return ch, err
	}
	if err := s.changeable(rec, d); err != nil {
		return s.failChange(ctx, ch, err), err
	}
	already := slices.ContainsFunc(rec.Nodes, func(n NodeDef) bool { return n.PlanChangeID == ch.ID || n.SupersededBy == ch.ID })
	prev := cloneRecord(rec)
	if !already {
		agents, err := s.agentIDs(ctx)
		if err != nil {
			return ch, err
		}
		plan, err := s.planChange(rec, states, agents, d.Project.SpentUSD, ch)
		if err != nil {
			return s.failChange(ctx, ch, err), err
		}
		s.revive(org, rec.ID) // a monitor tick that judged the project finished must not exit
		rec.Nodes = plan.nodes
		rec.StructureVersion++
		rec.Estimate = estimatePlan(rec.Nodes, rec.Objectives)
		if rec.Status == StatusFailed {
			rec.Status, rec.FinishedAt, rec.Error = StatusRunning, nil, ""
		}
		if err := s.cfg.Store.Put(ctx, rec); err != nil {
			return ch, err
		}
	}
	// The gate must know the new nodes before their tasks can start.
	if lp := s.liveOf(org, rec.ID); lp != nil {
		lp.mu.Lock()
		lp.nodes = map[string]NodeDef{}
		for _, n := range rec.Nodes {
			lp.nodes[n.ID] = n
		}
		lp.mu.Unlock()
	} else {
		s.adopt(application.WithOrg(ctx, org), rec)
	}
	snap, err := s.loadSnapshot(ctx, rec)
	if err != nil {
		return ch, err
	}
	am := s.buildAmendment(rec, snap, ch)
	res, err := s.cfg.Orch.AmendRun(ctx, rec.RequestID, am)
	if err != nil {
		if !already {
			_ = s.cfg.Store.Put(ctx, prev)
			if lp := s.liveOf(org, rec.ID); lp != nil {
				lp.mu.Lock()
				lp.nodes = map[string]NodeDef{}
				for _, n := range prev.Nodes {
					lp.nodes[n.ID] = n
				}
				lp.mu.Unlock()
			}
		}
		return s.failChange(ctx, ch, err), err
	}
	if len(res.Requeued) > 0 {
		requeued := map[string]bool{}
		for _, t := range res.Requeued {
			requeued[t] = true
		}
		if upd, err := s.cfg.Store.Get(ctx, org, rec.ID); err == nil {
			for id := range requeued {
				delete(upd.Decisions, id)
			}
			_ = s.cfg.Store.Put(ctx, upd)
			rec = upd
		}
		if lp := s.liveOf(org, rec.ID); lp != nil {
			lp.mu.Lock()
			for id := range requeued {
				delete(lp.decisions, id)
			}
			lp.mu.Unlock()
		}
	}
	now := s.now()
	ch.Status, ch.AppliedAt, ch.UpdatedAt, ch.Error = ChangeApplied, &now, now, ""
	if err := s.cfg.Changes.PutChange(ctx, ch); err != nil {
		return ch, err
	}
	s.audit(ctx, "project.plan_change_applied", rec.ID, map[string]any{"change_id": ch.ID, "live": res.Live, "added": len(res.Added), "requeued": len(res.Requeued),
		"structure_version": rec.StructureVersion})
	if lp := s.liveOf(org, rec.ID); lp != nil {
		lp.wake()
	}
	if d2, err := s.detailOf(ctx, rec); err == nil {
		s.emit(ctx, "project.status_changed", rec.ID, map[string]any{"project": d2.Project, "nodes": d2.Nodes, "approvals": d2.Approvals, "structure_version": rec.StructureVersion})
	}
	s.emitChange(ctx, rec.ID, ch)
	return ch, nil
}

// buildAmendment compares the plan (record) with the tasks and says what the
// orchestrator has to do: tasks for nodes that have none, new dependencies,
// edited content, tasks of nodes taken out of the plan, and the blocked tasks
// downstream of the change that can run again. Re-running it after a restart
// is safe: it only asks for what is still missing.
func (s *Service) buildAmendment(rec Record, snap snapshot, ch PlanChange) application.RunAmendment {
	am := application.RunAmendment{Repoint: map[string][]string{}, Edit: map[string]application.TaskEdit{}}
	byID := map[string]NodeDef{}
	for _, n := range rec.Nodes {
		byID[n.ID] = n
	}
	mapDeps := func(n NodeDef) []string {
		out := make([]string, 0, len(n.DependsOn))
		for _, d := range n.DependsOn {
			if dn, ok := byID[d]; !ok || dn.isGroup() {
				continue
			}
			if t, ok := snap.tasks[d]; ok {
				out = append(out, t.ID)
			} else {
				out = append(out, d) // a new node: its key
			}
		}
		return out
	}
	editable := func(t domain.Task) bool { return t.Status == domain.TaskPending || t.Status == domain.TaskBlocked }
	seeds := map[string]bool{}
	updated := map[string]bool{}
	for _, op := range ch.Ops {
		if op.Op == OpUpdatePending {
			updated[op.NodeID] = true
		}
	}
	for _, n := range rec.Nodes {
		if n.isGroup() {
			continue
		}
		t, has := snap.tasks[n.ID]
		if n.Superseded != "" {
			if has && t.Status == domain.TaskPending {
				am.Drop = append(am.Drop, t.ID)
			}
			continue
		}
		if !has {
			agent := "assistant"
			if n.AgentID != nil && *n.AgentID != "" && !n.human() {
				agent = *n.AgentID
			}
			am.Add = append(am.Add, application.NewTaskSpec{Key: n.ID, Title: n.Title, Description: describeNode(rec, n) + "\n\n" + nodeMarker(n.ID),
				AgentID: agent, DependsOn: mapDeps(n)})
			seeds[n.ID] = true
			continue
		}
		if !editable(t) {
			continue
		}
		want := mapDeps(n)
		if !sameSet(want, t.DependsOn) {
			am.Repoint[t.ID] = want
			seeds[n.ID] = true
		}
		if updated[n.ID] {
			title, desc := n.Title, describeNode(rec, n)+"\n\n"+nodeMarker(n.ID)
			e := application.TaskEdit{Title: &title, Description: &desc}
			if n.AgentID != nil {
				e.AgentID = n.AgentID
			}
			am.Edit[t.ID] = e
		}
	}
	// Blocked tasks downstream of the change that can run again.
	dependents := map[string][]string{}
	for _, n := range rec.Nodes {
		if n.isGroup() || n.Superseded != "" {
			continue
		}
		for _, d := range n.DependsOn {
			dependents[d] = append(dependents[d], n.ID)
		}
	}
	closure := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if closure[id] {
			return
		}
		closure[id] = true
		for _, c := range dependents[id] {
			walk(c)
		}
	}
	for id := range seeds {
		walk(id)
	}
	cand := map[string]bool{}
	for id := range closure {
		if t, ok := snap.tasks[id]; ok && t.Status == domain.TaskBlocked && byID[id].Superseded == "" && snap.rec.Decisions[t.ID] != "rejected" {
			cand[id] = true
		}
	}
	for changedSet := true; changedSet; {
		changedSet = false
		for id := range cand {
			for _, d := range byID[id].DependsOn {
				dn, ok := byID[d]
				if !ok || dn.isGroup() {
					continue
				}
				if t, has := snap.tasks[d]; !has || t.Status == domain.TaskDone || cand[d] {
					continue
				}
				delete(cand, id)
				changedSet = true
				break
			}
		}
	}
	for id := range cand {
		t := snap.tasks[id]
		am.Requeue = append(am.Requeue, t.ID)
		if _, ok := am.Repoint[t.ID]; !ok && !sameSet(mapDeps(byID[id]), t.DependsOn) {
			am.Repoint[t.ID] = mapDeps(byID[id])
		}
	}
	slices.Sort(am.Requeue)
	slices.Sort(am.Drop)
	return am
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		// tolerate duplicates
		a, b = uniq(a), uniq(b)
		if len(a) != len(b) {
			return false
		}
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

func uniq(in []string) []string {
	out := []string{}
	for _, x := range in {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// ---- sources of proposals ----

// planChangeTick runs in the monitor: it turns the suggested_tasks of finished
// nodes into proposals and applies the decisions taken on the open ones. It
// returns true when it changed the plan.
func (s *Service) planChangeTick(ctx context.Context, lp *liveProject, rec Record, snap snapshot) bool {
	if s.cfg.Changes == nil || s.cfg.Orch == nil {
		return false
	}
	s.consumeSuggestions(ctx, lp, rec, snap)
	lp.mu.Lock()
	open := lp.openChanges
	lp.mu.Unlock()
	if !open {
		return false
	}
	return s.syncChanges(ctx, rec.ID)
}

// consumeSuggestions turns the suggested_tasks of finished nodes into ONE
// pending proposal per node: deduplicated against the plan and the other
// proposals, bounded (maxSuggestedPerNode tasks; the open-proposal limit of the
// project). Agents only propose: a human approves.
func (s *Service) consumeSuggestions(ctx context.Context, lp *liveProject, rec Record, snap snapshot) {
	if terminalStatus(rec.Status) || rec.Control == ControlCancelled {
		return
	}
	if !lp.sugLoaded {
		list, err := s.cfg.Changes.ListChanges(ctx, rec.OrgID, rec.ID)
		if err != nil {
			return
		}
		lp.mu.Lock()
		if lp.sugSeen == nil {
			lp.sugSeen = map[string]bool{}
		}
		for _, c := range list {
			if c.Source == SourceSuggested && c.SourceNodeID != "" {
				lp.sugSeen[c.SourceNodeID] = true
			}
			if c.open() {
				lp.openChanges = true
			}
		}
		lp.sugLoaded = true
		lp.mu.Unlock()
	}
	var todo []string
	lp.mu.Lock()
	for nodeID, t := range snap.tasks {
		if t.Status != domain.TaskDone || t.Output == nil || len(t.Output.SuggestedTasks) == 0 || lp.sugSeen[nodeID] {
			continue
		}
		if sk, _ := t.Output.Metrics["skipped"].(bool); sk {
			continue
		}
		todo = append(todo, nodeID)
	}
	for _, id := range todo {
		lp.sugSeen[id] = true
	}
	lp.mu.Unlock()
	slices.Sort(todo)
	for _, nodeID := range todo {
		s.proposeSuggestions(ctx, rec, snap.tasks[nodeID], nodeID)
	}
}

func (s *Service) proposeSuggestions(ctx context.Context, rec Record, t domain.Task, nodeID string) {
	var src NodeDef
	for _, n := range rec.Nodes {
		if n.ID == nodeID {
			src = n
		}
	}
	if src.ID == "" || src.AgentID == nil || src.human() {
		return
	}
	known := map[string]bool{}
	for _, n := range rec.Nodes {
		if !n.isGroup() && n.Superseded == "" {
			known[normTitle(n.Title)] = true
		}
	}
	if list, err := s.cfg.Changes.ListChanges(ctx, rec.OrgID, rec.ID); err == nil {
		for _, c := range list {
			if c.Status == ChangeRejected {
				continue
			}
			for _, op := range c.Ops {
				for _, pt := range op.Tasks {
					known[normTitle(pt.Title)] = true
				}
			}
		}
	}
	var tasks []ProposedTask
	for _, title := range t.Output.SuggestedTasks {
		title = cleanText(title, maxProposedTitleLen)
		if title == "" || known[normTitle(title)] || len(tasks) >= maxSuggestedPerNode {
			continue
		}
		known[normTitle(title)] = true
		tasks = append(tasks, ProposedTask{Key: fmt.Sprintf("s%d", len(tasks)+1), Title: title, AgentID: *src.AgentID, Complexity: "M", DependsOn: []string{nodeID}})
	}
	if len(tasks) == 0 {
		return
	}
	actor := *src.AgentID
	_, err := s.newChange(application.WithActor(ctx, actor), rec.ID, changeSpec{proposedBy: ProposerAgent, actor: actor, source: SourceSuggested, sourceNode: nodeID,
		reason: tr(rec.Locale, "pv.suggest.reason", map[string]string{"node": src.Title}), ops: []PlanChangeOp{{Op: OpAddTask, Tasks: tasks}}, approvalAgent: actor})
	if err != nil {
		s.cfg.Log.Info("suggested tasks not proposed", "project", rec.ID, "node", nodeID, "err", err)
		s.audit(application.WithActor(ctx, "system"), "project.plan_change_suppressed", rec.ID, map[string]any{"source_node_id": nodeID, "reason": clip(err.Error(), 200)})
	}
}

// ProposeReplan asks the runtime for a replacement sub-plan for a failed node
// and records it as a pending proposal (agents propose; a human approves).
// Without a runtime replanner (or when the planner budget is exhausted) a
// deterministic one-task replacement is proposed.
func (s *Service) ProposeReplan(ctx context.Context, id, nodeID string) (PlanChange, error) {
	actor := application.ActorFrom(ctx, "user")
	if application.IsAgentPrincipal(actor) {
		return PlanChange{}, fmt.Errorf("%w: only humans can ask for a replan", domain.ErrForbidden)
	}
	rec, err := s.get(ctx, id)
	if err != nil {
		return PlanChange{}, err
	}
	d, states, err := s.changeState(ctx, rec)
	if err != nil {
		return PlanChange{}, err
	}
	if err := s.changeable(rec, d); err != nil {
		return PlanChange{}, err
	}
	var def NodeDef
	for _, n := range rec.Nodes {
		if n.ID == nodeID {
			def = n
		}
	}
	if def.ID == "" || def.isGroup() || def.Superseded != "" {
		return PlanChange{}, fmt.Errorf("%w: node %s", domain.ErrNotFound, nodeID)
	}
	if states[nodeID] != StateFailed {
		return PlanChange{}, fmt.Errorf("%w: only a failed node can be replanned (it is %s)", domain.ErrConflict, states[nodeID])
	}
	if list, err := s.cfg.Changes.ListChanges(ctx, rec.OrgID, id); err == nil {
		for _, c := range list {
			if c.open() && c.Source == SourceReplan && c.SourceNodeID == nodeID {
				return c, fmt.Errorf("%w: a replan of this node is already waiting for a decision", domain.ErrConflict)
			}
		}
	}
	agentID := ""
	if def.AgentID != nil {
		agentID = *def.AgentID
	}
	tasks := s.replanTasks(ctx, rec, d, def, agentID)
	return s.newChange(application.WithActor(ctx, actor), id, changeSpec{proposedBy: ProposerAgent, actor: actor, source: SourceReplan, sourceNode: nodeID,
		reason: tr(rec.Locale, "pv.replan.reason", map[string]string{"node": def.Title}), ops: []PlanChangeOp{{Op: OpReplaceTask, NodeID: nodeID, Tasks: tasks}},
		approvalAgent: agentID})
}

const maxReplanTasks = 6

func (s *Service) replanTasks(ctx context.Context, rec Record, d Detail, def NodeDef, agentID string) []ProposedTask {
	fallback := []ProposedTask{{Key: "r1", Title: cleanText(tr(rec.Locale, "pv.replan.fallbackTitle", map[string]string{"node": def.Title}), maxProposedTitleLen),
		Description: def.Description, AgentID: agentID, Complexity: orDefault(def.Complexity, "M")}}
	rp, ok := s.cfg.Runtime.(application.Replanner)
	if !ok || s.cfg.Runtime == nil || s.cfg.Orch == nil {
		return fallback
	}
	ags, err := s.cfg.Core.ListAgents(ctx, rec.OrgID)
	if err != nil {
		return fallback
	}
	known := map[string]bool{}
	pa := make([]application.PlanAgent, 0, len(ags))
	for _, a := range ags {
		known[a.ID] = true
		pa = append(pa, application.NewPlanAgent(a, rec.Locale))
	}
	req := application.ReplanRequest{ProjectGoal: rec.Goal, Failed: application.ReplanNode{ID: def.ID, Title: def.Title, Description: def.Description, AgentID: agentID, Error: "failed after retries"},
		Agents: pa, MaxTasks: maxReplanTasks, BudgetUSD: rec.BudgetUSD, Locale: rec.Locale}
	byNode := map[string]Node{}
	for _, n := range d.Nodes {
		byNode[n.ID] = n
	}
	snap, _ := s.loadSnapshot(ctx, rec)
	for _, dep := range def.DependsOn {
		if n, ok := byNode[dep]; ok && n.State == StateDone {
			in := application.ReplanNode{ID: n.ID, Title: n.Title}
			if t, ok := snap.tasks[dep]; ok && t.Output != nil {
				in.Summary = clip(t.Output.Summary, 400)
			}
			req.Inputs = append(req.Inputs, in)
		}
	}
	for _, n := range rec.Nodes {
		if !n.isGroup() && n.Superseded == "" && slices.Contains(n.DependsOn, def.ID) {
			req.Dependents = append(req.Dependents, application.ReplanNode{ID: n.ID, Title: n.Title})
		}
	}
	res, ex := s.cfg.Orch.ReservePlanner(ctx)
	if ex != nil {
		return fallback
	}
	resp, err := rp.Replan(ctx, req)
	usage := application.Usage{}
	if resp.Usage != nil {
		usage = *resp.Usage
	}
	s.cfg.Orch.RecordPlannerUsage(ctx, usage, res)
	if err != nil || len(resp.Tasks) == 0 {
		return fallback
	}
	var out []ProposedTask
	keys := map[string]bool{}
	for i, t := range resp.Tasks {
		if i >= maxReplanTasks {
			break
		}
		k := t.Key
		if !changeKeyRe.MatchString(k) || keys[k] {
			k = fmt.Sprintf("r%d", i+1)
		}
		keys[k] = true
		agent := t.AgentID
		if !known[agent] {
			agent = agentID
		}
		out = append(out, ProposedTask{Key: k, Title: cleanText(t.Title, maxProposedTitleLen), Description: cleanText(t.Description, maxProposedDescLen),
			AgentID: agent, Complexity: strings.ToUpper(t.Complexity), DependsOn: t.DependsOn})
	}
	for i := range out { // only dependencies on the replacement's own tasks survive
		var deps []string
		for _, dp := range out[i].DependsOn {
			if keys[dp] && dp != out[i].Key {
				deps = append(deps, dp)
			}
		}
		out[i].DependsOn = deps
	}
	for _, t := range out {
		if t.Title == "" {
			return fallback
		}
	}
	return out
}
