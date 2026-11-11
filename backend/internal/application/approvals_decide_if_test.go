package application_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/infrastructure/memory"
)

// brokenApprovals is a store whose approvals cannot be read (a database error).
type brokenApprovals struct{ *memory.Store }

func (brokenApprovals) GetApproval(context.Context, string, string) (domain.Approval, error) {
	return domain.Approval{}, errors.New("database unavailable")
}

func newDecideIf(t *testing.T, store application.Store) (*application.Approvals, context.Context) {
	t.Helper()
	cfg := application.DefaultConfig()
	rec := &application.Recorder{OrgID: cfg.OrgID, Store: store, Pub: &capture{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return application.NewApprovals(cfg, store, rec), application.WithOrg(context.Background(), cfg.OrgID)
}

// D3 (pendientes, área B, punto 4): what an approval is about (the outbox e-mail) is edited only while it has no vote,
// and a vote is recorded only if its check still holds; both in the critical section of the decisions.
func TestDecideIfAndWhileUndecided(t *testing.T) {
	appr, ctx := newDecideIf(t, memory.New())
	ap, _, err := appr.Request(ctx, domain.Approval{TaskID: "t1", AgentID: "sales", Action: "send", Risk: "high", Title: "x", RequiredApprovals: 2})
	if err != nil {
		t.Fatal(err)
	}
	voter := application.WithActor(ctx, "u-voter")

	// a failed check records nothing
	stale := errors.New("the e-mail changed")
	if _, err := appr.DecideIf(voter, ap.ID, "approve", "", func(domain.Approval) error { return stale }); !errors.Is(err, stale) {
		t.Fatalf("DecideIf with a failing check = %v", err)
	}
	ran := false
	if err := appr.WhileUndecided(ctx, ap.ID, func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("no vote yet: the edit runs (%v %v)", err, ran)
	}

	// while the edit runs nobody decides: the vote waits for it
	voted := make(chan error, 1)
	if err := appr.WhileUndecided(ctx, ap.ID, func() error {
		go func() { _, err := appr.Decide(voter, ap.ID, "approve", ""); voted <- err }()
		select {
		case err := <-voted:
			t.Errorf("a vote was recorded in the middle of an edit: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-voted; err != nil {
		t.Fatalf("the vote after the edit: %v", err)
	}

	// once there is a vote nobody edits
	ran = false
	if err := appr.WhileUndecided(ctx, ap.ID, func() error { ran = true; return nil }); !errors.Is(err, application.ErrApprovalVoted) || ran {
		t.Fatalf("edit after a vote: %v ran=%v", err, ran)
	}
	// a missing approval leaves the edit alone (the item has no record to protect)
	if err := appr.WhileUndecided(ctx, "nope", func() error { return nil }); err != nil {
		t.Fatalf("missing approval: %v", err)
	}
}

// When the approval cannot be read the edit fails closed: it does not run.
func TestWhileUndecidedFailsClosed(t *testing.T) {
	appr, ctx := newDecideIf(t, brokenApprovals{memory.New()})
	ran := false
	if err := appr.WhileUndecided(ctx, "ap-1", func() error { ran = true; return nil }); err == nil || ran {
		t.Fatalf("unreadable approval: err=%v ran=%v", err, ran)
	}
}
