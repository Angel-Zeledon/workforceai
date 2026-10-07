package gateway

import (
	"context"

	"aiworkforce/backend/internal/application"
)

// Guard adapts the controls service and the gateway to application.ExecutionGuard.
type Guard struct{ G *Gateway }

var _ application.ExecutionGuard = Guard{}

// Admit implements application.ExecutionGuard.
func (x Guard) Admit(ctx context.Context, org, agentID string) application.GuardVerdict {
	v := x.G.Controls.Admit(ctx, org, agentID)
	return application.GuardVerdict{Allowed: v.Allowed, Code: v.Code}
}

// SideEffectsBlocked implements application.ExecutionGuard.
func (x Guard) SideEffectsBlocked(ctx context.Context, org, agentID string) bool {
	return x.G.Controls.SideEffectsBlocked(ctx, org, agentID)
}

// ToolBlocked implements application.ExecutionGuard.
func (x Guard) ToolBlocked(ctx context.Context, org, tool, action string) bool {
	return x.G.Controls.ToolBlocked(ctx, org, tool, action)
}

// IsSideEffect implements application.ExecutionGuard.
func (x Guard) IsSideEffect(tool, action string) bool { return x.G.IsSideEffect(tool, action) }
