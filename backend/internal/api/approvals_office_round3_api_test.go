package api_test

import (
	"context"
	"strings"
	"testing"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/gateway"
)

// D3 round 3: between the two votes of a double approval the e-mail cannot be edited (409): what leaves is what both
// people approved, never a text only the second one saw.
func TestOutboxEditBetweenTwoVotesIsRefused(t *testing.T) {
	var gw *gateway.Gateway
	p := newOfficeApprovals(t, withGateway(&gw))
	item, apID := p.outboxItem(gw, "sales", func(a *domain.Approval) { a.RequiredApprovals = 2 })

	if resp, raw := p.e.do("POST", api1+"/outbox/"+item+"/approve", p.beto, map[string]any{"version": 1}, nil); resp.StatusCode != 202 {
		t.Fatalf("first vote = %d %s", resp.StatusCode, raw)
	}
	resp, raw := p.e.do("PATCH", api1+"/outbox/"+item, p.carla, map[string]any{"body": "otro texto", "to": []string{"eve@evil.example"}}, nil)
	if resp.StatusCode != 409 {
		t.Fatalf("edit after one vote = %d %s", resp.StatusCode, raw)
	}
	resp, raw = p.e.do("POST", api1+"/outbox/"+item+"/approve", p.carla, map[string]any{"version": 1}, nil)
	if resp.StatusCode != 200 || strings.Contains(string(raw), "otro texto") || strings.Contains(string(raw), "eve@evil.example") {
		t.Fatalf("second vote = %d %s", resp.StatusCode, raw)
	}
	hs, _ := gw.Holds(context.Background(), p.org, "held")
	if len(hs) != 1 || strings.Contains(strings.Join(hs[0].Recipients, ","), "eve@") {
		t.Fatalf("held: %+v", hs)
	}
	if ap, _ := p.e.store.GetApproval(context.Background(), p.org, apID); ap.Status != domain.ApprovalApproved || len(ap.Decisions) != 2 {
		t.Fatalf("approval: %s %d", ap.Status, len(ap.Decisions))
	}

	// before any vote the e-mail is still editable
	item2, _ := p.outboxItem(gw, "accounting", func(a *domain.Approval) { a.RequiredApprovals = 2 })
	if resp, raw := p.e.do("PATCH", api1+"/outbox/"+item2, p.beto, map[string]any{"subject": "Hola 2"}, nil); resp.StatusCode != 200 {
		t.Fatalf("edit before voting = %d %s", resp.StatusCode, raw)
	}
}
