package gateway

import (
	"context"
	"errors"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/connections/gmail"
	"aiworkforce/backend/internal/infrastructure/memory"
)

// Durable at-most-once (A1b): the execution ledger outlives the gateway's memory.

func TestApprovedSendRunsOnceAcrossGatewayRestarts(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	writeConn(e)
	ledger := memory.New()
	e.g.Executions = ledger
	ac, _ := approvedCall(t, e, sendArgs())
	if out := e.exec(ac); out.Status != "scheduled" {
		t.Fatalf("first execution: %+v", out)
	}
	// A new gateway over the same stores (the process restarted): its in-memory
	// record of approved outcomes is empty, the ledger is not.
	g2 := New(e.cs, e.ctl, e.g.Suspects, e.g.Log)
	g2.Now, g2.Executions = e.clk.Now, ledger
	out := g2.Execute(context.Background(), ac)
	if out.Decision != "allowed" || out.Status != application.StatusAlreadyExecuted {
		t.Fatalf("second execution must be refused as already executed: %+v", out)
	}
	if hs, _ := e.g.Holds(context.Background(), org, ""); len(hs) != 1 {
		t.Fatalf("one hold, got %d", len(hs))
	}
	_, rec, _ := ledger.ClaimExecution(context.Background(), org, application.ApprovalExecution{ApprovalID: ac.ApprovalID})
	if rec.Status != "scheduled" || rec.HoldID == "" || rec.ArgsHash != ac.ApprovedArgsHash {
		t.Fatalf("ledger record: %+v", rec)
	}
}

type brokenLedger struct{}

func (brokenLedger) ClaimExecution(context.Context, string, application.ApprovalExecution) (bool, application.ApprovalExecution, error) {
	return false, application.ApprovalExecution{}, errors.New("db down")
}
func (brokenLedger) FinishExecution(context.Context, string, string, string, string) error {
	return nil
}

func TestApprovedSendFailsClosedWithoutTheLedger(t *testing.T) {
	e := newEnv(t, gmail.Config{})
	writeConn(e)
	e.g.Executions = brokenLedger{}
	ac, _ := approvedCall(t, e, sendArgs())
	if out := e.exec(ac); out.Decision != "denied" || out.DenyReason != CodeExecutionUnavailable {
		t.Fatalf("must not run when at-most-once cannot be guaranteed: %+v", out)
	}
	if hs, _ := e.g.Holds(context.Background(), org, ""); len(hs) != 0 {
		t.Fatalf("nothing scheduled, got %d", len(hs))
	}
}
