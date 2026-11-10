package api_test

import (
	"context"
	"errors"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/gateway"
)

// failingApproval is the store with GetApproval failing for one approval id (a database error).
type failingApproval struct {
	application.Store
	id string
}

func (f failingApproval) GetApproval(ctx context.Context, org, id string) (domain.Approval, error) {
	if id == f.id {
		return domain.Approval{}, errors.New("database unavailable")
	}
	return f.Store.GetApproval(ctx, org, id)
}

func heldCount(t *testing.T, gw *gateway.Gateway, org string) int {
	t.Helper()
	hs, err := gw.Holds(context.Background(), org, "held")
	if err != nil {
		t.Fatal(err)
	}
	return len(hs)
}

// D3 round 2: an outbox e-mail leaves only once its approval is granted. With double approval the first vote is
// recorded (202) and nothing is scheduled; the second person's vote schedules it.
func TestOutboxDoubleApprovalSendsOnlyOnTheSecondVote(t *testing.T) {
	var gw *gateway.Gateway
	p := newOfficeApprovals(t, withGateway(&gw))
	item, apID := p.outboxItem(gw, "sales", func(a *domain.Approval) { a.RequiredApprovals = 2 })

	resp, raw := p.e.do("POST", api1+"/outbox/"+item+"/approve", p.beto, map[string]any{"version": 1}, nil)
	if resp.StatusCode != 202 {
		t.Fatalf("first vote = %d %s", resp.StatusCode, raw)
	}
	if n := heldCount(t, gw, p.org); n != 0 {
		t.Fatalf("one vote of two scheduled %d e-mails", n)
	}
	if ap, _ := p.e.store.GetApproval(context.Background(), p.org, apID); ap.Status != domain.ApprovalPending || len(ap.Decisions) != 1 {
		t.Fatalf("after the first vote: %s %d", ap.Status, len(ap.Decisions))
	}
	resp, raw = p.e.do("POST", api1+"/outbox/"+item+"/approve", p.carla, map[string]any{"version": 1}, nil)
	if resp.StatusCode != 200 || heldCount(t, gw, p.org) != 1 {
		t.Fatalf("second vote = %d %s", resp.StatusCode, raw)
	}
}

// D3 round 2: when the approval cannot be read the outbox fails closed: nothing is decided, nothing is sent.
func TestOutboxFailsClosedWhenTheApprovalCannotBeRead(t *testing.T) {
	var gw *gateway.Gateway
	const broken = "ap-broken"
	p := newOfficeApprovals(t, withGateway(&gw), func(o *opts) {
		prev := o.deps
		o.deps = func(d *api.Deps) {
			prev(d)
			d.Store = failingApproval{Store: d.Store, id: broken}
		}
	})
	item, _ := p.outboxItem(gw, "sales", func(a *domain.Approval) { a.ID = broken })
	resp, raw := p.e.do("POST", api1+"/outbox/"+item+"/approve", p.beto, map[string]any{"version": 1}, nil)
	if resp.StatusCode < 500 {
		t.Fatalf("approve with an unreadable approval = %d %s", resp.StatusCode, raw)
	}
	if n := heldCount(t, gw, p.org); n != 0 {
		t.Fatalf("%d e-mails scheduled", n)
	}
}
