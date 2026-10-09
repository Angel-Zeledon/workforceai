package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/policy"
)

// Approvals manages human-in-the-loop decisions. The orchestrator registers a
// waiter before publishing the approval; Decide wakes it up.
type Approvals struct {
	cfg     Config
	store   Store
	rec     *Recorder
	mu      sync.Mutex
	waiters map[string]chan domain.Approval
	// decideMu serializes decisions so that the count of approvals of a
	// double-approval request cannot be raced.
	decideMu sync.Mutex
	// Observer (optional) is told about rejected approvals (anomaly detection).
	Observer AnomalyObserver
}

func NewApprovals(cfg Config, store Store, rec *Recorder) *Approvals {
	return &Approvals{cfg: cfg, store: store, rec: rec, waiters: map[string]chan domain.Approval{}}
}

// Request stores a pending approval, registers its waiter and emits
// approval.requested. ap.ID is kept when set (a caller that must bind context
// to the id before the approval becomes visible); otherwise a new one is made.
func (a *Approvals) Request(ctx context.Context, ap domain.Approval) (domain.Approval, <-chan domain.Approval, error) {
	if ap.ID == "" {
		ap.ID = newID()
	}
	ap.Status, ap.CreatedAt = domain.ApprovalPending, time.Now().UTC()
	ap.RequiredApprovals = max(ap.RequiredApprovals, 1)
	ap.Decisions = []domain.ApprovalDecision{}
	// decideMu: nobody can decide the approval before its "requested" entry is
	// in the audit trail (the row is visible as soon as it is stored).
	a.decideMu.Lock()
	defer a.decideMu.Unlock()
	if err := a.store.CreateApproval(ctx, a.org(ctx), ap); err != nil {
		return ap, nil, err
	}
	ch := make(chan domain.Approval, 1)
	a.mu.Lock()
	a.waiters[ap.ID] = ch
	a.mu.Unlock()
	a.rec.Emit(ctx, Action{Type: domain.EvApprovalRequest, AgentID: ap.AgentID, Entity: "approval", EntityID: ap.ID,
		Payload: map[string]any{"approval": ap},
		Text:    fmt.Sprintf("Aprobación requerida: %s", ap.Title)})
	return ap, ch, nil
}

// Attach registers a waiter for an approval that already exists (a task
// resumed after a restart). If the approval was decided meanwhile, the channel
// already holds the decision.
func (a *Approvals) Attach(ctx context.Context, id string) (domain.Approval, <-chan domain.Approval, error) {
	// decideMu: a decision cannot slip between reading the status and registering the waiter.
	a.decideMu.Lock()
	defer a.decideMu.Unlock()
	ap, err := a.store.GetApproval(ctx, a.org(ctx), id)
	if err != nil {
		return ap, nil, err
	}
	ch := make(chan domain.Approval, 1)
	if ap.Status != domain.ApprovalPending {
		ch <- ap
		return ap, ch, nil
	}
	a.mu.Lock()
	a.waiters[id] = ch
	a.mu.Unlock()
	return ap, ch, nil
}

// Deadline is when a pending approval is auto-rejected. It derives from the
// creation time, so a restart never extends (or resets) the wait.
func (a *Approvals) Deadline(ap domain.Approval) time.Time {
	return ap.CreatedAt.Add(a.cfg.ApprovalTimeout)
}

// Supersede rejects a pending approval as the system because the action it
// guarded will be asked again (its task restarts from scratch after a restart).
func (a *Approvals) Supersede(ctx context.Context, id, note string) (domain.Approval, error) {
	return a.resolve(ctx, id, domain.ApprovalRejected, note, "system")
}

// Wait blocks until the approval is resolved, the timeout expires (auto-reject)
// or ctx is cancelled.
func (a *Approvals) Wait(ctx context.Context, ch <-chan domain.Approval, id string) (domain.Approval, error) {
	return a.WaitUntil(ctx, ch, id, time.Now().Add(a.cfg.ApprovalTimeout))
}

// WaitUntil is Wait with an absolute deadline (already past: auto-reject now).
func (a *Approvals) WaitUntil(ctx context.Context, ch <-chan domain.Approval, id string, deadline time.Time) (domain.Approval, error) {
	select {
	case ap := <-ch: // decided before the wait started (Attach)
		return ap, nil
	default:
	}
	timer := time.NewTimer(max(time.Until(deadline), 0))
	defer timer.Stop()
	select {
	case ap := <-ch:
		return ap, nil
	case <-timer.C:
		ap, err := a.resolve(ctx, id, domain.ApprovalRejected, "sin respuesta: tiempo de espera agotado", "system")
		if err != nil {
			// Someone resolved it concurrently; use that decision.
			select {
			case ap2 := <-ch:
				return ap2, nil
			default:
			}
		}
		return ap, err
	case <-ctx.Done():
		a.forget(id)
		return domain.Approval{}, ctx.Err()
	}
}

// WaitReminding is the wait of a project approval (W2): it never auto-rejects
// on the interactive 30-minute deadline. Every `every` it emits
// approval.reminder (also pushed to the subscriptions of the organization) and
// it keeps waiting until a human decides or ctx is cancelled. timeout > 0
// restores an auto-reject after that long since the approval was created.
// The reminder cadence follows the approval's age, so a restart does not
// reset it.
func (a *Approvals) WaitReminding(ctx context.Context, ch <-chan domain.Approval, ap domain.Approval, every, timeout time.Duration) (domain.Approval, error) {
	select {
	case res := <-ch: // decided before the wait started (Attach)
		return res, nil
	default:
	}
	created := ap.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	var expire <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(max(time.Until(created.Add(timeout)), 0))
		defer t.Stop()
		expire = t.C
	}
	var tick <-chan time.Time
	var timer *time.Timer
	next := func() {
		d := every - (time.Since(created) % every)
		if timer == nil {
			timer = time.NewTimer(d)
		} else {
			timer.Reset(d)
		}
		tick = timer.C
	}
	if every > 0 {
		next()
		defer timer.Stop()
	}
	n := 0
	for {
		select {
		case res := <-ch:
			return res, nil
		case <-tick:
			n++
			a.remind(ctx, ap, n, time.Since(created))
			next()
		case <-expire:
			res, err := a.resolve(ctx, ap.ID, domain.ApprovalRejected, "sin respuesta: tiempo de espera agotado", "system")
			if err != nil {
				select {
				case res2 := <-ch:
					return res2, nil
				default:
				}
			}
			return res, err
		case <-ctx.Done():
			a.forget(ap.ID)
			return domain.Approval{}, ctx.Err()
		}
	}
}

// remind emits approval.reminder for a still pending approval. It is a system
// notification, not an audited decision (SkipAudit): the approval itself keeps
// its audit trail.
func (a *Approvals) remind(ctx context.Context, ap domain.Approval, n int, waited time.Duration) {
	cur, err := a.store.GetApproval(ctx, a.org(ctx), ap.ID)
	if err == nil {
		if cur.Status != domain.ApprovalPending {
			return // decided meanwhile
		}
		ap = cur
	}
	a.rec.Emit(ctx, Action{Type: domain.EvApprovalReminder, AgentID: ap.AgentID, Entity: "approval", EntityID: ap.ID, SkipAudit: true,
		Payload: map[string]any{"approval": ap, "reminder": n, "waiting_seconds": int(waited.Seconds())},
		Text:    fmt.Sprintf("Recordatorio: aprobación pendiente desde hace %s: %s", waited.Round(time.Minute), ap.Title)})
}

// Decide applies a human decision ("approve" | "reject").
//
// Governance rules enforced here (docs/architecture/06-permisos-autonomia.md 3.2):
//   - only humans decide: an agent or system principal never can;
//   - every approver needs at least the approval's required role;
//   - the requester cannot approve when double approval or maker-checker applies;
//   - with double approval the first approval only records itself: the approval
//     stays pending until a second, different human approves. One rejection by
//     any authorized human is final.
func (a *Approvals) Decide(ctx context.Context, id, decision, note string) (domain.Approval, error) {
	var st domain.ApprovalStatus
	switch strings.ToLower(decision) {
	case "approve":
		st = domain.ApprovalApproved
	case "reject":
		st = domain.ApprovalRejected
	default:
		return domain.Approval{}, fmt.Errorf("%w: decision must be approve or reject", domain.ErrInvalid)
	}
	actor := ActorFrom(ctx, "user")
	a.decideMu.Lock()
	defer a.decideMu.Unlock()
	ap, err := a.store.GetApproval(ctx, a.org(ctx), id)
	if err != nil {
		return ap, err
	}
	if ap.Status != domain.ApprovalPending {
		return ap, fmt.Errorf("%w: approval already %s", domain.ErrConflict, ap.Status)
	}
	if isAgentPrincipal(actor, ap.AgentID) {
		a.deny(ctx, ap, actor, "agents_cannot_decide")
		return ap, fmt.Errorf("%w: agents and system principals can never decide approvals", domain.ErrForbidden)
	}
	if st == domain.ApprovalRejected {
		return a.resolveLocked(ctx, ap, st, note, actor)
	}
	role := ActorRoleFrom(ctx, policy.RoleOwner) // without auth the demo acts as owner
	if policy.RoleRank(role) < policy.RoleRank(ap.RequiredRole) || policy.RoleRank(role) == 0 {
		a.deny(ctx, ap, actor, "role_too_low")
		return ap, fmt.Errorf("%w: this approval needs the %s role or higher", domain.ErrForbidden, ap.RequiredRole)
	}
	if ap.NoSelfApproval && ap.RequestedBy != "" && actor == ap.RequestedBy {
		a.deny(ctx, ap, actor, "self_approval")
		return ap, fmt.Errorf("%w: the person who requested the action cannot approve it", domain.ErrForbidden)
	}
	for _, d := range ap.Decisions {
		if d.By == actor {
			return ap, fmt.Errorf("%w: you already approved this request; a different person must give the next approval", domain.ErrConflict)
		}
	}
	now := time.Now().UTC()
	ap.Decisions = append(append([]domain.ApprovalDecision{}, ap.Decisions...), domain.ApprovalDecision{By: actor, Role: role, Note: note, TS: now})
	if len(ap.Decisions) < max(ap.RequiredApprovals, 1) {
		if err := a.store.UpdateApproval(ctx, a.org(ctx), ap); err != nil {
			return ap, err
		}
		a.rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: "approval.partial", Entity: "approval", EntityID: id, RequestID: a.requestOf(ctx, ap),
			Details: map[string]any{"task_id": ap.TaskID, "action": ap.Action, "received": len(ap.Decisions), "required": ap.RequiredApprovals,
				"required_role": ap.RequiredRole, "policy_rule": ap.PolicyRule}})
		a.rec.Emit(ctx, Action{Type: domain.EvApprovalProgress, AgentID: ap.AgentID, Entity: "approval", EntityID: id, SkipAudit: true,
			Payload: map[string]any{"approval": ap},
			Text:    fmt.Sprintf("Aprobación %d/%d: %s", len(ap.Decisions), ap.RequiredApprovals, ap.Title)})
		return ap, nil
	}
	return a.resolveLocked(ctx, ap, st, note, actor)
}

// deny audits a refused decision attempt (who tried, and why it was refused).
func (a *Approvals) deny(ctx context.Context, ap domain.Approval, actor, why string) {
	a.rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: "approval.decision_refused", Entity: "approval", EntityID: ap.ID, RequestID: a.requestOf(ctx, ap),
		Details: map[string]any{"reason": why, "task_id": ap.TaskID, "action": ap.Action, "required_role": ap.RequiredRole}})
}

// isAgentPrincipal: agents never receive the "approve" capability. Their
// principals ("agent:<id>", an agent id) and the system cannot decide, even if
// a bug or a forged context tried to.
func isAgentPrincipal(actor, agentID string) bool {
	actor = strings.ToLower(strings.TrimSpace(actor))
	return actor == "" || actor == "system" || actor == "orchestrator" || strings.HasPrefix(actor, "agent:") ||
		(agentID != "" && actor == strings.ToLower(agentID))
}

// requestOf finds the request an approval belongs to (via its task).
func (a *Approvals) requestOf(ctx context.Context, ap domain.Approval) string {
	if t, err := a.store.GetTask(ctx, a.org(ctx), ap.TaskID); err == nil {
		return t.RequestID
	}
	return ""
}

// resolve finalizes a pending approval (timeouts and cancellations).
func (a *Approvals) resolve(ctx context.Context, id string, st domain.ApprovalStatus, note, actor string) (domain.Approval, error) {
	a.decideMu.Lock()
	defer a.decideMu.Unlock()
	ap, err := a.store.GetApproval(ctx, a.org(ctx), id)
	if err != nil {
		return ap, err
	}
	if ap.Status != domain.ApprovalPending {
		return ap, fmt.Errorf("%w: approval already %s", domain.ErrConflict, ap.Status)
	}
	return a.resolveLocked(ctx, ap, st, note, actor)
}

func (a *Approvals) resolveLocked(ctx context.Context, ap domain.Approval, st domain.ApprovalStatus, note, actor string) (domain.Approval, error) {
	id := ap.ID
	now := time.Now().UTC()
	ap.Status, ap.Note, ap.ResolvedAt = st, note, &now
	if err := a.store.UpdateApproval(ctx, a.org(ctx), ap); err != nil {
		return ap, err
	}
	approvers := make([]string, 0, len(ap.Decisions))
	for _, d := range ap.Decisions {
		approvers = append(approvers, d.By)
	}
	a.rec.Audit(ctx, domain.AuditLog{Actor: actor, Action: "approval." + string(st), Entity: "approval", EntityID: id, RequestID: a.requestOf(ctx, ap),
		Details: map[string]any{"note": note, "task_id": ap.TaskID, "action": ap.Action, "risk": ap.Risk, "approvers": approvers,
			"required_approvals": ap.RequiredApprovals, "required_role": ap.RequiredRole, "requested_by": ap.RequestedBy, "policy_rule": ap.PolicyRule}})
	if st == domain.ApprovalRejected && a.Observer != nil {
		a.Observer.ObserveApprovalRejected(ctx, a.org(ctx), ap.AgentID)
	}
	a.rec.Emit(ctx, Action{Type: domain.EvApprovalResolved, AgentID: ap.AgentID, Entity: "approval", EntityID: id, SkipAudit: true,
		Payload: map[string]any{"approval": ap},
		Text:    fmt.Sprintf("Aprobación %s: %s", spanishStatus(st), ap.Title)})

	a.mu.Lock()
	ch := a.waiters[id]
	delete(a.waiters, id)
	a.mu.Unlock()
	if ch != nil {
		ch <- ap // buffered(1), single send
	}
	return ap, nil
}

func (a *Approvals) forget(id string) {
	a.mu.Lock()
	delete(a.waiters, id)
	a.mu.Unlock()
}

func spanishStatus(s domain.ApprovalStatus) string {
	if s == domain.ApprovalApproved {
		return "aprobada"
	}
	return "rechazada"
}
