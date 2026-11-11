package gateway

import (
	"context"
	"sync"
	"time"

	"aiworkforce/backend/internal/application"
)

// ledger is where an approved action is claimed before it runs: the durable one (Executions, Postgres) or, without
// it, the claims of this process. Both the outbox and the task woken by the same approval execute it; the claim is
// what makes only one of them send (the remembered outcome alone left a window between scheduling and remembering).
func (g *Gateway) ledger() application.ExecutionLedger {
	if g.Executions != nil {
		return g.Executions
	}
	return &g.local
}

// localLedger is the in-memory ExecutionLedger of one gateway (bounded like approvedOut).
type localLedger struct {
	mu sync.Mutex
	m  map[string]application.ApprovalExecution
}

func (l *localLedger) ClaimExecution(_ context.Context, _ string, e application.ApprovalExecution) (bool, application.ApprovalExecution, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if prev, ok := l.m[e.ApprovalID]; ok {
		return false, prev, nil
	}
	if l.m == nil || len(l.m) > 2000 {
		l.m = map[string]application.ApprovalExecution{}
	}
	e.Status, e.ClaimedAt = application.ExecutionClaimed, time.Now().UTC()
	l.m[e.ApprovalID] = e
	return true, e, nil
}

func (l *localLedger) FinishExecution(_ context.Context, _, approvalID, status, holdID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.m[approvalID]; ok {
		now := time.Now().UTC()
		e.Status, e.HoldID, e.FinishedAt = status, holdID, &now
		l.m[approvalID] = e
	}
	return nil
}
