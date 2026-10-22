package trust

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/quality"
)

// Signals gives the raw counts of one agent from data the platform already records: approvals, tasks and the
// audit trail (policy denials). The Postgres store has the same definitions in SQL (postgres.TrustStore.Collect).
type Signals interface {
	Collect(ctx context.Context, org, agentID string, since, burstSince time.Time) (Counts, error)
}

// PolicyDenialReasonPrefix marks a tool.denied audit entry produced by the policy engine (the agent asked for
// something the organization's rules forbid). Kill switch, read-only mode or a missing grant also deny tool
// requests, but those are not the agent's fault and are not counted.
const PolicyDenialReasonPrefix = "policy:"

// StoreSignals derives the counts from the application store and its audit trail, so it works with the memory
// store too.
type StoreSignals struct {
	Store application.Store
	Audit application.AuditStore // nil = policy denials are not counted
}

var _ Signals = StoreSignals{}

// at is the moment a task ended (or the best we know).
func taskAt(t domain.Task) time.Time {
	at := t.CreatedAt
	if t.StartedAt != nil {
		at = *t.StartedAt
	}
	if t.FinishedAt != nil {
		at = *t.FinishedAt
	}
	return at
}

// Collect implements Signals. Evidence is what happened strictly AFTER since: the decision that caused a change
// of level belongs to the old level, never to the new one.
func (s StoreSignals) Collect(ctx context.Context, org, agentID string, since, burstSince time.Time) (Counts, error) {
	var c Counts
	approvals, err := s.Store.ListApprovals(ctx, org, "")
	if err != nil {
		return c, err
	}
	for _, a := range approvals {
		if a.AgentID != agentID || a.ResolvedAt == nil || !a.ResolvedAt.After(since) {
			continue
		}
		switch {
		case a.Status == domain.ApprovalApproved:
			c.Approved++
		case a.Status == domain.ApprovalRejected && !quality.SystemRejection(a.Note):
			c.Rejected++
			if strings.EqualFold(a.Risk, "high") {
				c.HighRiskRejected++
			}
			if !a.ResolvedAt.Before(burstSince) {
				c.RecentRejected++
			}
		}
	}
	tasks, err := s.Store.ListTasks(ctx, org, agentID, "")
	if err != nil {
		return c, err
	}
	for _, t := range tasks {
		if t.AgentID != agentID || !taskAt(t).After(since) {
			continue
		}
		switch t.Status {
		case domain.TaskDone:
			c.TasksDone++
		case domain.TaskFailed:
			c.TasksFailed++
		case domain.TaskBlocked:
			c.TasksBlocked++
		}
	}
	if s.Audit != nil {
		from := since
		q := domain.AuditQuery{From: &from, Actor: agentID, Action: "tool.denied", Limit: 1000}
		for pages := 0; pages < 20; pages++ {
			page, err := s.Audit.QueryAudit(ctx, org, q)
			if err != nil {
				return c, err
			}
			for _, e := range page.Items {
				if e.TS.After(since) && PolicyDenial(e.Details) {
					c.PolicyDenials++
				}
			}
			if page.NextCursor == "" {
				break
			}
			q.Cursor = page.NextCursor
		}
	}
	return c, nil
}

// PolicyDenial tells whether the details of a tool.denied entry name a policy rule.
func PolicyDenial(details any) bool {
	m, ok := details.(map[string]any)
	if !ok {
		b, err := json.Marshal(details)
		if err != nil || json.Unmarshal(b, &m) != nil {
			return false
		}
	}
	r, _ := m["reason"].(string)
	return strings.HasPrefix(r, PolicyDenialReasonPrefix)
}
