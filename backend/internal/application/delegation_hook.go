package application

import (
	"context"
	"fmt"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// Delegation enforcement (docs/architecture/organization.md, section 3). The delegation rules live in
// internal/orgchart, which imports this package, so the orchestrator only knows the small port below.
// With no checker installed, or no rule that matches, everything behaves exactly as before.
//
// Where it is consulted (every path that gives work to an agent):
//   - createTasks: every task of a plan (chat requests, /assign, projects, meetings: they all go through
//     Submit / SubmitPlan). A plan with a denied task is refused as a whole, before anything is stored;
//     a preset plan (SubmitPlan) is refused synchronously so the caller gets the error right away.
//   - runTask: a task whose rule requires approval asks the named approver before it starts (durable: the
//     approval is persisted, a restart attaches to it again).
//   - Reassign (human reassignment of a pending task) and handleConsults (agent to agent).
//   - AmendRun: tasks added or re-pointed to another agent by an approved plan change (deny only).
//
// Every decision is audited (delegation.allowed / denied / approval_required / approved / rejected).

// DelegationActor is who delegates: a person (a member id, or "owner") or an agent.
type DelegationActor struct {
	Kind string // "person" | "agent"
	ID   string
}

// DelegationVerdict is the answer of a rule check.
type DelegationVerdict struct {
	Allowed       bool
	NeedsApproval bool
	ApproverKind  string // "person" | "agent" ("" when no approval is needed)
	ApproverID    string
	RuleID        string
	Reason        string // no_rule | self | allowed_by_rule | denied_by_rule | approval_required
}

// DelegationChecker is implemented by internal/orgchart.
type DelegationChecker interface {
	CheckDelegation(ctx context.Context, org string, from DelegationActor, toAgentID string) DelegationVerdict
}

// DelegationError is returned when a rule denies the assignment. It wraps domain.ErrForbidden (HTTP 403)
// and its message says who may not give work to whom.
type DelegationError struct {
	From    DelegationActor
	ToAgent string
	ToName  string
	Verdict DelegationVerdict
}

func (e *DelegationError) Error() string {
	to := e.ToName
	if to == "" {
		to = e.ToAgent
	}
	rule := ""
	if e.Verdict.RuleID != "" {
		rule = " (rule " + e.Verdict.RuleID + ")"
	}
	return fmt.Sprintf("delegation not allowed: %s %s may not assign work to %s%s", e.From.Kind, e.From.ID, to, rule)
}

func (e *DelegationError) Unwrap() error { return domain.ErrForbidden }

// delegationApprovalAction is the Action of the approvals this hook creates.
const delegationApprovalAction = "delegation.assign"

// delegationHooks is the optional state of the orchestrator for this file and hold.go.
type delegationHooks struct {
	deleg DelegationChecker
}

// SetDelegation installs the delegation rule checker (nil removes it).
func (o *Orchestrator) SetDelegation(c DelegationChecker) { o.hooks.deleg = c }

// delegator maps the actor of a request to who delegates. ok=false means "not a delegation by anyone"
// (the system itself, scheduled work, the assistant): those are never checked.
func delegator(requestedBy string, agents map[string]domain.Agent) (DelegationActor, bool) {
	by := strings.TrimSpace(requestedBy)
	switch by {
	case "system", assistantID:
		return DelegationActor{}, false
	case "", "user":
		return DelegationActor{Kind: "person", ID: "owner"}, true
	}
	if _, isAgent := agents[by]; isAgent {
		return DelegationActor{Kind: "agent", ID: by}, true
	}
	return DelegationActor{Kind: "person", ID: by}, true
}

func (o *Orchestrator) auditDelegation(ctx context.Context, action, entity, id string, from DelegationActor, to string, v DelegationVerdict) {
	d := map[string]any{"from_kind": from.Kind, "from": from.ID, "to_agent": to, "rule_id": v.RuleID, "reason": v.Reason}
	if v.NeedsApproval {
		d["approver_kind"], d["approver"] = v.ApproverKind, v.ApproverID
	}
	o.rec.Audit(ctx, domain.AuditLog{Actor: ActorFrom(ctx, "system"), Action: action, Entity: entity, EntityID: id, Details: d})
}

// enforceDelegation checks one assignment and audits the decision. A denial is a *DelegationError; an
// approval requirement is only audited here (runTask asks it right before the task starts).
func (o *Orchestrator) enforceDelegation(ctx context.Context, requestedBy string, agents map[string]domain.Agent,
	toAgent, entity, entityID string, audit bool) (DelegationVerdict, error) {
	c := o.hooks.deleg
	if c == nil || toAgent == "" || toAgent == assistantID {
		return DelegationVerdict{Allowed: true}, nil
	}
	from, ok := delegator(requestedBy, agents)
	if !ok {
		return DelegationVerdict{Allowed: true}, nil
	}
	v := c.CheckDelegation(ctx, o.org(ctx), from, toAgent)
	switch {
	case !v.Allowed:
		o.auditDelegation(ctx, "delegation.denied", entity, entityID, from, toAgent, v)
		return v, &DelegationError{From: from, ToAgent: toAgent, ToName: agents[toAgent].Name, Verdict: v}
	case v.NeedsApproval:
		if audit {
			o.auditDelegation(ctx, "delegation.approval_required", entity, entityID, from, toAgent, v)
		}
	default:
		if audit {
			o.auditDelegation(ctx, "delegation.allowed", entity, entityID, from, toAgent, v)
		}
	}
	return v, nil
}

// checkPlanDelegation refuses a plan (preset or planner-made) with a denied task, before anything is stored.
func (o *Orchestrator) checkPlanDelegation(ctx context.Context, requestedBy string, agents map[string]domain.Agent, plan PlanResponse) error {
	if o.hooks.deleg == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, pt := range plan.Tasks {
		a := pt.AgentID
		if _, ok := agents[a]; !ok {
			a = assistantID // an unknown agent is reassigned to the assistant (createTasks)
		}
		if seen[a] {
			continue
		}
		seen[a] = true
		if _, err := o.enforceDelegation(ctx, requestedBy, agents, a, "agent", a, false); err != nil {
			return err
		}
	}
	return nil
}

// delegationApproval asks the approver named by the rule before a task starts. handled=true means the
// task is finished (blocked or interrupted) and out is its outcome. It is idempotent and restart-safe:
// an approval already created for the task is attached again, a granted one lets the task go.
func (o *Orchestrator) delegationApproval(ctx context.Context, rs *run, t domain.Task) (out Outcome, handled bool) {
	if o.hooks.deleg == nil || t.AgentID == assistantID {
		return OutcomeDone, false
	}
	from, ok := delegator(rs.requestedBy, rs.agents)
	if !ok {
		return OutcomeDone, false
	}
	org := o.org(ctx)
	v := o.hooks.deleg.CheckDelegation(ctx, org, from, t.AgentID)
	if !v.Allowed { // the rules changed while the task waited
		o.auditDelegation(ctx, "delegation.denied", "task", t.ID, from, t.AgentID, v)
		o.blockTask(ctx, rs, t, "delegation denied by rule "+v.RuleID)
		return OutcomeBlocked, true
	}
	if !v.NeedsApproval {
		return OutcomeDone, false
	}
	var prev domain.Approval
	found := false
	if all, err := o.store.ListApprovals(ctx, org, ""); err == nil {
		for _, a := range all {
			if a.TaskID == t.ID && a.Action == delegationApprovalAction && (!found || a.CreatedAt.After(prev.CreatedAt)) {
				prev, found = a, true
			}
		}
	}
	if found && prev.Status == domain.ApprovalApproved {
		return OutcomeDone, false
	}
	if found && prev.Status == domain.ApprovalRejected {
		o.auditDelegation(ctx, "delegation.rejected", "task", t.ID, from, t.AgentID, v)
		o.blockTask(ctx, rs, t, "delegation rejected by the approver")
		return OutcomeBlocked, true
	}
	name := rs.agents[t.AgentID].Name
	if name == "" {
		name = t.AgentID
	}
	var ap domain.Approval
	var ch <-chan domain.Approval
	var err error
	if found { // pending: wait on the same approval, with its original deadline (restart)
		ap, ch, err = o.approvals.Attach(ctx, prev.ID)
	} else {
		ap = domain.Approval{TaskID: t.ID, AgentID: t.AgentID, Action: delegationApprovalAction, Risk: "medium",
			Title:   fmt.Sprintf("Delegation needs approval: %s -> %s", from.ID, name),
			Details: fmt.Sprintf("%s %s assigns %q to %s. Rule %s requires the approval of %s %s.", from.Kind, from.ID, t.Title, name, v.RuleID, v.ApproverKind, v.ApproverID),
			Context: map[string]any{"delegation": true, "approver_kind": v.ApproverKind, "approver": v.ApproverID, "rule_id": v.RuleID}}
		governApproval(&ap, rs, PolicyVerdict{RequiredApprovals: 1})
		ap, ch, err = o.approvals.Request(ctx, ap)
	}
	if err != nil {
		o.failTask(ctx, rs, t, err)
		return OutcomeFailed, true
	}
	o.auditDelegation(ctx, "delegation.approval_required", "task", t.ID, from, t.AgentID, v)
	tid := t.ID
	t.Status = domain.TaskAwaitingApproval
	if err := o.store.UpdateTask(ctx, org, t); err != nil {
		o.log.Warn("update task", "err", err)
	}
	o.setRequestStatus(ctx, rs, domain.RequestAwaitingApproval)
	o.setState(ctx, t.AgentID, domain.StateAwaitingApproval, "Esperando aprobación: "+ap.Title, &tid, 80)
	o.emitMetrics(ctx)
	resume := YieldSlot(ctx) // a task waiting for a human does not hold a scheduler slot
	res, werr := o.awaitApproval(ctx, rs, ch, ap, o.approvals.Deadline(ap))
	resume()
	if werr != nil {
		return OutcomeFailed, true // shutdown
	}
	o.setRequestStatus(ctx, rs, domain.RequestRunning)
	t.Status = domain.TaskPending
	if err := o.store.UpdateTask(ctx, org, t); err != nil {
		o.log.Warn("update task", "err", err)
	}
	if res.Status != domain.ApprovalApproved {
		o.auditDelegation(ctx, "delegation.rejected", "task", t.ID, from, t.AgentID, v)
		o.blockTask(ctx, rs, t, "delegation rejected: "+res.Note)
		return OutcomeBlocked, true
	}
	o.auditDelegation(ctx, "delegation.approved", "task", t.ID, from, t.AgentID, v)
	return OutcomeDone, false
}
