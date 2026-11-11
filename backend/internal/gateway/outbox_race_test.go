package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/connections/gmail"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
)

// D3 (pendientes, área B, punto 4): the outbox and the waiting task both execute an approved send, the decision and
// the execution can disagree, and a person can edit while another votes.

// Without Postgres (no durable ledger) the outbox and the woken task execute the same approval. The task's call is
// forced into the window where the outbox already stored the hold but has not remembered its outcome: one send only.
func TestOutboxAndWokenTaskSendOnceWithoutLedger(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	ctx := context.Background()
	c, p := pendingSend(t, e)
	e.g.RegisterApproval("ap-1", p)
	task := call("email", "send", map[string]any{"to": "laura@acme.com", "subject": "Propuesta", "body": "Adjunto la propuesta."})
	task.ApprovalID, task.ApprovedArgsHash = "ap-1", p.ArgsHash
	emit, woke := e.g.Emit, false
	var taskOut application.GatewayOutcome
	e.g.Emit = func(ctx context.Context, org, typ string, pl map[string]any) {
		emit(ctx, org, typ, pl)
		if typ == "tool_call.hold_changed" && !woke {
			woke = true
			taskOut = e.g.Execute(ctx, task) // the task wakes up right now
		}
	}
	if _, err := e.g.OutboxApprove(ctx, org, p.OutboxID, 1, "admin", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !woke || taskOut.Status == "scheduled" && taskOut.HoldID != p.OutboxID {
		t.Fatalf("the task scheduled its own send: %+v", taskOut)
	}
	if holds, _ := e.g.Holds(ctx, org, "held"); len(holds) != 1 {
		t.Fatalf("%d holds for one approval", len(holds))
	}
	e.clk.Add(2 * time.Minute)
	e.g.ProcessDue(ctx)
	if n := len(e.gm.FakeFor(c.ID).SentMessages()); n != 1 {
		t.Fatalf("%d e-mails sent for one approval", n)
	}
}

// Approved, then refused when it was about to be scheduled (the grant was revoked meanwhile): the approval is spent,
// so the item leaves the queue as blocked with the reason instead of hanging pending (and 409 on every retry).
func TestOutboxApprovedButDeniedIsClosed(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	ctx := context.Background()
	c, p := pendingSend(t, e)
	e.g.RegisterApproval("ap-1", p)
	_, err := e.g.OutboxApprove(ctx, org, p.OutboxID, 1, "admin", func(string) error {
		return e.cs.RevokeGrant(ctx, org, c.ID, "assistant", "admin")
	})
	var de *DeniedError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v", err)
	}
	if items, _ := e.g.Outbox(ctx, org, OutboxPendingApproval); len(items) != 0 {
		t.Fatalf("still pending after its approval was spent: %+v", items)
	}
	items, _ := e.g.Outbox(ctx, org, OutboxBlocked)
	if len(items) != 1 || items[0].ID != p.OutboxID || items[0].BlockReason != de.Code {
		t.Fatalf("blocked items: %+v", items)
	}
}

// The orchestrator binds the item to its approval (RegisterApproval) right after the gateway queued it. Approving it
// from the outbox in that window must not send it without any decision.
func TestOutboxApproveBeforeTheApprovalIsBound(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	ctx := context.Background()
	_, p := pendingSend(t, e)
	decided := false
	_, err := e.g.OutboxApprove(ctx, org, p.OutboxID, 1, "admin", func(string) error { decided = true; return nil })
	if !errors.Is(err, ErrApprovalNotReady) || decided {
		t.Fatalf("approve before binding: err=%v decided=%v", err, decided)
	}
	if holds, _ := e.g.Holds(ctx, org, "held"); len(holds) != 0 {
		t.Fatal("scheduled without a decision")
	}
	// once bound it is approved as usual
	e.g.RegisterApproval("ap-1", p)
	if it, err := e.g.OutboxApprove(ctx, org, p.OutboxID, 1, "admin", func(string) error { return nil }); err != nil || it.Status != OutboxHeld {
		t.Fatalf("after binding: %v %+v", err, it)
	}
}

// A double approval: a person approves version 1 from the outbox while another edits the text. The edit sneaks in
// after the first person loaded the item and before their vote is recorded: the vote must be refused (the text
// changed), never recorded for a text that person did not see.
func TestOutboxVoteAndEditDoNotCross(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	ctx := application.WithOrg(context.Background(), org)
	_, p := pendingSend(t, e)
	store := memory.New()
	cfg := application.DefaultConfig()
	rec := &application.Recorder{OrgID: org, Store: store, Pub: nopPub{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	appr := application.NewApprovals(cfg, store, rec)
	ap, _, err := appr.Request(ctx, domain.Approval{TaskID: "t1", AgentID: "assistant", Action: "send", Risk: "high", Title: "enviar", RequiredApprovals: 2})
	if err != nil {
		t.Fatal(err)
	}
	e.g.RegisterApproval(ap.ID, p)
	body := "otro texto"
	edit := func() error {
		return appr.WhileUndecided(application.WithActor(ctx, "u-editor"), ap.ID, func() error {
			_, err := e.g.OutboxEdit(ctx, org, p.OutboxID, OutboxPatch{Body: &body})
			return err
		})
	}
	_, err = e.g.OutboxApprove(ctx, org, p.OutboxID, 1, "u-voter", func(id string) error {
		if err := edit(); err != nil { // the other person edits right now
			t.Fatalf("edit before any vote: %v", err)
		}
		_, err := appr.DecideIf(application.WithActor(ctx, "u-voter"), id, "approve", "", func(domain.Approval) error {
			return e.g.OutboxCurrent(org, p.OutboxID, 1)
		})
		return err
	})
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("vote on a text that changed: %v", err)
	}
	if cur, _ := store.GetApproval(ctx, org, ap.ID); len(cur.Decisions) != 0 {
		t.Fatalf("the vote was recorded for a text the voter did not see: %+v", cur.Decisions)
	}

	// the other order: once a vote is recorded nobody edits
	if _, err := appr.DecideIf(application.WithActor(ctx, "u-voter"), ap.ID, "approve", "", func(domain.Approval) error {
		return e.g.OutboxCurrent(org, p.OutboxID, 2)
	}); err != nil {
		t.Fatal(err)
	}
	if err := edit(); !errors.Is(err, application.ErrApprovalVoted) {
		t.Fatalf("edit after a vote: %v", err)
	}
	if it, _ := e.g.OutboxEntry(org, p.OutboxID); it.Version != 2 {
		t.Fatalf("version = %d", it.Version)
	}
}

type nopPub struct{}

func (nopPub) Publish(context.Context, domain.Event) error { return nil }
