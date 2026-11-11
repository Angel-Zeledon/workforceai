package api_test

import (
	"context"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/gateway"
)

// An item queued a moment ago whose approval is not bound yet answers 409 approval_not_ready (and sends nothing).
func TestOutboxApproveBeforeBindingAnswersNotReady(t *testing.T) {
	var gw *gateway.Gateway
	p := newOfficeApprovals(t, withGateway(&gw))
	p.outboxItem(gw, "sales") // a connection and a grant for sales
	out := gw.Execute(context.Background(), application.GatewayCall{Org: p.org, AgentID: "sales", TaskID: "t-x", Tool: "email", Action: "send",
		Autonomy: "autonomous", Args: map[string]any{"to": "laura@acme.com", "subject": "Sin aprobación aún", "body": "Texto"}})
	if out.Pending == nil {
		t.Fatalf("no pending send: %+v", out)
	}
	resp, raw := p.e.do("POST", api1+"/outbox/"+out.Pending.OutboxID+"/approve", p.beto, map[string]any{"version": 1}, nil)
	if resp.StatusCode != 409 || !strings.Contains(string(raw), "approval_not_ready") {
		t.Fatalf("approve before binding = %d %s", resp.StatusCode, raw)
	}
	if n := heldCount(t, gw, p.org); n != 0 {
		t.Fatalf("%d e-mails scheduled without a decision", n)
	}
}
