package projects

import (
	"context"
	"fmt"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// W2 manual recovery of project nodes (docs/plans/large-workflows.md): a human
// re-queues a failed/blocked node (RetryNode) or skips a failed one so its
// dependents proceed (SkipNode). The work itself is the orchestrator's
// (RetryTask / SkipTask); the project only validates its own state, forgets the
// gate decisions of the re-queued nodes and wakes its monitor up again.

// RecoveryResult is the answer of the node recovery endpoints.
type RecoveryResult struct {
	Node     Node     `json:"node"`
	Project  Summary  `json:"project"`
	Requeued []string `json:"requeued"` // node ids put back in the queue
}

// RetryNode re-queues a failed or blocked node and the nodes blocked behind it.
func (s *Service) RetryNode(ctx context.Context, projectID, nodeID string) (RecoveryResult, error) {
	return s.recoverNode(ctx, projectID, nodeID, false, "")
}

// SkipNode marks a failed node as skipped with a human reason: its dependents
// run with a note that its input is missing.
func (s *Service) SkipNode(ctx context.Context, projectID, nodeID, reason string) (RecoveryResult, error) {
	return s.recoverNode(ctx, projectID, nodeID, true, reason)
}

func (s *Service) recoverNode(ctx context.Context, projectID, nodeID string, skip bool, reason string) (RecoveryResult, error) {
	if s.cfg.Orch == nil {
		return RecoveryResult{}, fmt.Errorf("%w: recovery is not available", domain.ErrConflict)
	}
	rec, err := s.get(ctx, projectID)
	if err != nil {
		return RecoveryResult{}, err
	}
	if rec.RequestID == "" {
		return RecoveryResult{}, fmt.Errorf("%w: the project has not been launched", domain.ErrConflict)
	}
	if rec.Status == StatusCancelled || rec.Control == ControlCancelled {
		return RecoveryResult{}, fmt.Errorf("%w: the project is cancelled", domain.ErrConflict)
	}
	snap, err := s.loadSnapshot(ctx, rec)
	if err != nil {
		return RecoveryResult{}, err
	}
	def, ok := findNode(rec.Nodes, nodeID)
	if !ok || def.isGroup() {
		return RecoveryResult{}, fmt.Errorf("%w: node %s", domain.ErrNotFound, nodeID)
	}
	t, ok := snap.tasks[nodeID]
	if !ok {
		return RecoveryResult{}, fmt.Errorf("%w: the node has no task yet", domain.ErrConflict)
	}
	var res application.RecoveryResult
	if skip {
		res, err = s.cfg.Orch.SkipTask(ctx, t.ID, reason)
	} else {
		res, err = s.cfg.Orch.RetryTask(ctx, t.ID)
	}
	if err != nil {
		return RecoveryResult{}, err
	}
	requeued := map[string]bool{}
	for _, id := range res.Requeued {
		requeued[id] = true
	}
	// The request is already revived here. Tell the monitor before the record
	// changes: a tick that judged the project finished from a stale view must
	// neither exit nor persist the terminal status (see watch/publish). When
	// there is no monitor (it exited, or after a restart) a new one is adopted.
	live := s.revive(rec.OrgID, projectID)
	// A re-queued gate asks its human again: its old answer is forgotten. The
	// project is running again (its terminal status is re-derived by the monitor).
	upd, err := s.update(ctx, projectID, func(r *Record) error {
		for id := range requeued {
			delete(r.Decisions, id)
		}
		if r.Status == StatusFailed {
			r.Status, r.FinishedAt, r.Error = StatusRunning, nil, ""
		}
		return nil
	})
	if err != nil {
		return RecoveryResult{}, err
	}
	if lp := s.liveOf(upd.OrgID, projectID); lp != nil && live {
		lp.mu.Lock()
		for id := range requeued {
			delete(lp.decisions, id)
		}
		lp.mu.Unlock()
		lp.wake()
	} else {
		s.adopt(application.WithOrg(ctx, upd.OrgID), upd)
	}
	nodeIDs := make([]string, 0, len(res.Requeued))
	for _, id := range res.Requeued {
		if nid, ok := snap.taskNode[id]; ok {
			nodeIDs = append(nodeIDs, nid)
		}
	}
	action := "project.node_retried"
	details := map[string]any{"node_id": nodeID, "task_id": t.ID, "requeued_nodes": nodeIDs}
	if skip {
		action = "project.node_skipped"
		details["reason"] = reason
	}
	s.audit(ctx, action, projectID, details)
	snap2, err := s.loadSnapshot(ctx, upd)
	if err != nil {
		return RecoveryResult{}, err
	}
	d := s.build(ctx, snap2)
	s.emit(ctx, "project.status_changed", projectID, map[string]any{"project": d.Project, "nodes": d.Nodes, "approvals": d.Approvals, "structure_version": upd.StructureVersion})
	out := RecoveryResult{Project: d.Project, Requeued: nodeIDs}
	for _, n := range d.Nodes {
		if n.ID == nodeID {
			out.Node = n
		}
	}
	return out, nil
}

func findNode(nodes []NodeDef, id string) (NodeDef, bool) {
	for _, n := range nodes {
		if n.ID == id {
			return n, true
		}
	}
	return NodeDef{}, false
}
