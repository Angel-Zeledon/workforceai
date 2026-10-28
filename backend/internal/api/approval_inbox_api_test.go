package api_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// B17: the inbox lists pending approvals with their task and cost and never decides anything.
func TestApprovalInboxAPI(t *testing.T) {
	e := newEnv(t, opts{})
	ctx, org := context.Background(), application.DefaultConfig().OrgID
	now := time.Now().UTC()
	if err := e.store.CreateTask(ctx, org, domain.Task{ID: "t-in", RequestID: "rq", Title: "Enviar propuesta", AgentID: "sales", Status: domain.TaskAwaitingApproval, CreatedAt: now, CostUSD: 0.02}); err != nil {
		t.Fatal(err)
	}
	for _, ap := range []domain.Approval{
		{ID: "ap-low", TaskID: "t-in", AgentID: "sales", Action: "draft", Title: "Borrador", Risk: "low", Status: domain.ApprovalPending, CreatedAt: now, RequiredApprovals: 1, Decisions: []domain.ApprovalDecision{}},
		{ID: "ap-high", TaskID: "t-in", AgentID: "sales", Action: "send", Title: "Enviar", Risk: "high", Status: domain.ApprovalPending, CreatedAt: now, RequiredApprovals: 1, Decisions: []domain.ApprovalDecision{}},
	} {
		if err := e.store.CreateApproval(ctx, org, ap); err != nil {
			t.Fatal(err)
		}
	}
	resp, body := e.do("GET", "/api/v1/approvals/inbox", "", nil, nil)
	var inbox application.ApprovalInbox
	_ = json.Unmarshal(body, &inbox)
	if resp.StatusCode != 200 || len(inbox.Items) != 2 || inbox.Items[0].ID != "ap-high" || !inbox.Items[0].HighRisk ||
		inbox.Items[1].TaskTitle != "Enviar propuesta" || inbox.Items[1].TaskCostUSD != 0.02 || inbox.Groups == nil || inbox.Suggestions == nil {
		t.Fatalf("inbox = %d %s", resp.StatusCode, body)
	}
	if ap, _ := e.store.GetApproval(ctx, org, "ap-low"); ap.Status != domain.ApprovalPending {
		t.Fatalf("reading the inbox changed an approval: %+v", ap)
	}
}
