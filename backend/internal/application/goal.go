package application

import (
	"context"
	"fmt"
	"strings"

	"aiworkforce/backend/internal/domain"
)

// Company goals (F1, docs/architecture/goals.md). A request may point to one goal of the organization; the goal is
// validated and stored with the request BEFORE it exists (like the customer scope, customer.go), and its title and
// description reach the planner and the tasks as one more recalled memory: untrusted data written by people, the
// same channel as the memory notes, never instructions. A request without a goal behaves exactly as before.

type goalCtxKey struct{}

// WithGoal marks ctx as work for one goal ("" leaves ctx unchanged).
func WithGoal(ctx context.Context, goalID string) context.Context {
	goalID = strings.TrimSpace(goalID)
	if goalID == "" {
		return ctx
	}
	return context.WithValue(ctx, goalCtxKey{}, goalID)
}

// GoalFrom is the goal ctx works for ("" = none).
func GoalFrom(ctx context.Context) string {
	v, _ := ctx.Value(goalCtxKey{}).(string)
	return v
}

// GoalSource is implemented by internal/goals (installed by the server).
type GoalSource interface {
	// CheckLink accepts a goal new work may point to: of this organization, active and visible to the person
	// (userID) asking. Any refusal is domain.ErrInvalid and does not tell a hidden goal from a missing one.
	CheckLink(ctx context.Context, org, userID, goalID string) error
	// AgentNote is the goal as context for an agent's task (agentID "" = the planner); ok=false: nothing to add
	// (for instance a private department's goal for an agent of another department).
	AgentNote(ctx context.Context, org, goalID, agentID string) (RecalledMemory, bool)
}

// SetGoals installs the goals (nil removes them).
func (o *Orchestrator) SetGoals(g GoalSource) { o.goals = g }

// checkGoal validates the goal in ctx for a new request ("" = none, always fine).
func (o *Orchestrator) checkGoal(ctx context.Context) (string, error) {
	gid := GoalFrom(ctx)
	if gid == "" {
		return "", nil
	}
	if o.goals == nil {
		return "", fmt.Errorf("%w: goals are not available", domain.ErrInvalid)
	}
	if err := o.goals.CheckLink(ctx, o.org(ctx), PersonID(ActorFrom(ctx, "")), gid); err != nil {
		return "", err
	}
	return gid, nil
}

// goalNote is the goal of the run for an agent (agentID "" = the planner).
func (o *Orchestrator) goalNote(ctx context.Context, rs *run, agentID string) (RecalledMemory, bool) {
	if o.goals == nil || rs.req.GoalID == "" {
		return RecalledMemory{}, false
	}
	return o.goals.AgentNote(ctx, o.org(ctx), rs.req.GoalID, agentID)
}
