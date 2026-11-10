package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/connections/gmail"
)

// D3 (Tarea 6, ronda 2): the decision comes first. Nothing is scheduled when deciding fails or when the approval
// still needs another vote (double approval); the send happens only once the approval is granted.
func TestOutboxApproveDecidesBeforeSending(t *testing.T) {
	ctx := context.Background()
	for name, decideErr := range map[string]error{"refused": errors.New("forbidden"), "partial": ErrApprovalIncomplete} {
		e := newEnv(t, gmail.Config{})
		c, p := pendingSend(t, e)
		e.g.RegisterApproval("ap-1", p)
		_, err := e.g.OutboxApprove(ctx, org, p.OutboxID, 1, "admin", func(string) error {
			if holds, _ := e.g.Holds(ctx, org, "held"); len(holds) != 0 {
				t.Errorf("%s: scheduled before deciding", name)
			}
			return decideErr
		})
		if !errors.Is(err, decideErr) {
			t.Fatalf("%s: err = %v", name, err)
		}
		e.clk.Add(2 * time.Minute)
		e.g.ProcessDue(ctx)
		if holds, _ := e.g.Holds(ctx, org, "held"); len(holds) != 0 || len(e.gm.FakeFor(c.ID).SentMessages()) != 0 {
			t.Fatalf("%s: the e-mail left", name)
		}
		if items, _ := e.g.Outbox(ctx, org, OutboxPendingApproval); len(items) != 1 {
			t.Fatalf("%s: the item is no longer pending: %+v", name, items)
		}
	}

	// granted: it is scheduled as before
	e := newEnv(t, gmail.Config{})
	_, p := pendingSend(t, e)
	e.g.RegisterApproval("ap-1", p)
	if it, err := e.g.OutboxApprove(ctx, org, p.OutboxID, 1, "admin", func(string) error { return nil }); err != nil || it.Status != OutboxHeld {
		t.Fatalf("granted: %v %+v", err, it)
	}

	// a refused rejection leaves the item pending
	e = newEnv(t, gmail.Config{})
	_, p = pendingSend(t, e)
	e.g.RegisterApproval("ap-1", p)
	if _, err := e.g.OutboxReject(ctx, org, p.OutboxID, "admin", func(string) error { return errors.New("forbidden") }); err == nil {
		t.Fatal("a refused rejection must fail")
	}
	if items, _ := e.g.Outbox(ctx, org, OutboxPendingApproval); len(items) != 1 {
		t.Fatalf("the refused rejection closed the item: %+v", items)
	}
}
