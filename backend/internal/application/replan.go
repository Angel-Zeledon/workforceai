package application

import (
	"context"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// Replanning of a failed branch of a running project (Q2). The runtime
// capability is optional (like HierarchicalPlanner): POST /v1/replan proposes a
// replacement sub-plan for one failed node. The runtime never applies it: the
// projects layer turns it into a plan change proposal that a human approves.

// ReplanNode is a node of the project as the replanner sees it.
type ReplanNode struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	// Error is why the node failed (replaced node only).
	Error string `json:"error,omitempty"`
	// Summary is the output of a finished dependency (completed inputs only).
	Summary string `json:"summary,omitempty"`
}

type ReplanRequest struct {
	ProjectGoal string       `json:"project_goal"`
	Failed      ReplanNode   `json:"failed"`
	Inputs      []ReplanNode `json:"inputs"`     // finished dependencies of the failed node
	Dependents  []ReplanNode `json:"dependents"` // nodes waiting for it
	Agents      []PlanAgent  `json:"agents"`
	MaxTasks    int          `json:"max_tasks"`
	BudgetUSD   float64      `json:"budget_usd,omitempty"`
	Locale      string       `json:"locale,omitempty"`
}

type ReplanResponse struct {
	// Tasks reuse the phase task shape: keys are local, depends_on lists local keys.
	Tasks  []PhaseTask `json:"tasks"`
	Reason string      `json:"reason,omitempty"`
	Usage  *Usage      `json:"usage,omitempty"`
}

// Replanner is the optional runtime capability behind "Replan this branch".
type Replanner interface {
	Replan(ctx context.Context, in ReplanRequest) (ReplanResponse, error)
}

// IsAgentPrincipal reports whether actor is an agent, the system or the
// orchestrator: principals that can propose but never approve.
func IsAgentPrincipal(actor string) bool { return isAgentPrincipal(actor, "") }

// ApprovalGovernance says who must approve an internal, non-tool action (the
// policy rules of the organization: minimum role, double approval, no
// self-approval). Without policy it is one approval from any approver.
func (o *Orchestrator) ApprovalGovernance(ctx context.Context, agentID, tool, action, risk string, amountUSD float64) (required int, role string, noSelf bool, rule string) {
	if o.policy == nil {
		return 1, "", false, ""
	}
	ag, err := o.store.GetAgent(ctx, o.org(ctx), agentID)
	if err != nil {
		ag = domain.Agent{ID: agentID}
	}
	v := o.policy.Decide(ctx, PolicyInput{Agent: ag, Tool: tool, Action: action, Args: map[string]any{"amount_usd": amountUSD}, Risk: strings.ToLower(risk)})
	return max(v.RequiredApprovals, 1), v.RequiredRole, v.NoSelfApproval, v.RuleID
}
