package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Migration 350 (durable resume): gate and chat in the run meta, the gateway
// card in the checkpoint, the execution ledger under RLS, and down/up.
func TestRunMetaGateChatAndGatewayCheckpoint(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	est := domain.CostEstimate{RequestID: "req-1", Currency: "USD", RequiresConfirmation: true, Total: domain.CostRange{MinUSD: 1, MaxUSD: 2}}
	started := time.Now().UTC().Truncate(time.Millisecond)
	meta := application.RunMeta{RequestID: "req-1", RequestedBy: "u1",
		Gate: &application.RunGate{Kind: application.GateCostConfirmation, StartedAt: started, Estimate: &est},
		Chat: &application.ChatLinkMeta{ConversationID: "agent:sales", TurnID: "turn-1", ReplyTo: "m1", Speaker: "sales", Locale: "es"}}
	if err := st.PutRunMeta(ctx, "org-a", meta); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRunMeta(ctx, "org-a", "req-1")
	if err != nil || got.Gate == nil || got.Gate.Kind != application.GateCostConfirmation || !got.Gate.StartedAt.Equal(started) ||
		got.Gate.Estimate == nil || got.Gate.Estimate.Total.MaxUSD != 2 || got.Chat == nil || got.Chat.TurnID != "turn-1" {
		t.Fatalf("run meta = %+v (gate %+v chat %+v), %v", got, got.Gate, got.Chat, err)
	}
	meta.Gate, meta.Chat = nil, nil // cleared once decided
	if err := st.PutRunMeta(ctx, "org-a", meta); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetRunMeta(ctx, "org-a", "req-1"); got.Gate != nil || got.Chat != nil {
		t.Fatalf("gate and chat must clear: %+v", got)
	}

	cp := application.TaskCheckpoint{TaskID: "task-1", RequestID: "req-1", ApprovalID: "ap-1",
		Tools:   []application.ToolRequest{{Tool: "email", Action: "send", Args: map[string]any{"to": "laura@acme.com"}}},
		Gateway: &application.GatewayPending{ArgsHash: "h1", OutboxID: "ob_1", Recipients: []string{"laura@acme.com"}, HoldSeconds: 60}}
	if err := st.PutCheckpoint(ctx, "org-a", cp); err != nil {
		t.Fatal(err)
	}
	c, err := st.GetCheckpoint(ctx, "org-a", "task-1")
	if err != nil || c.Gateway == nil || c.Gateway.ArgsHash != "h1" || c.Gateway.OutboxID != "ob_1" || c.Taint != nil {
		t.Fatalf("checkpoint = %+v, %v", c, err)
	}
}

func TestExecutionLedgerClaimsOnceAndIsolatesTenants(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")
	e := application.ApprovalExecution{ApprovalID: "ap-1", TaskID: "task-1", Tool: "email", Action: "send", ArgsHash: "h1"}

	// Concurrent claims: exactly one wins.
	wins := make(chan bool, 4)
	for range 4 {
		go func() {
			ok, _, err := st.ClaimExecution(ctx, "org-a", e)
			if err != nil {
				t.Error(err)
			}
			wins <- ok
		}()
	}
	n := 0
	for range 4 {
		if <-wins {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d claims won, want 1", n)
	}
	if err := st.FinishExecution(ctx, "org-a", "ap-1", "scheduled", "hold-1"); err != nil {
		t.Fatal(err)
	}
	ok, prev, err := st.ClaimExecution(ctx, "org-a", e)
	if err != nil || ok || prev.Status != "scheduled" || prev.HoldID != "hold-1" || prev.ArgsHash != "h1" || prev.FinishedAt == nil {
		t.Fatalf("second claim: ok=%v prev=%+v err=%v", ok, prev, err)
	}
	if err := st.FinishExecution(ctx, "org-a", "nope", "x", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("finish of an unknown claim: %v", err)
	}

	// Another tenant neither sees nor blocks org-a's record.
	if ok, _, err := st.ClaimExecution(ctx, "org-b", e); err != nil || !ok {
		t.Fatalf("org-b claim of the same id: %v %v", ok, err)
	}
	err = st.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM approval_executions").Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("org-b sees %d execution rows, want only its own", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx, "org-a"); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := st.ClaimExecution(ctx, "org-a", e); !ok {
		t.Fatal("reset must clear the execution records")
	}
}

func TestMigration350DownThenUpAgain(t *testing.T) {
	env := testStore(t)
	st := env.Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	if err := st.PutRunMeta(ctx, "org-a", application.RunMeta{RequestID: "req-1", Gate: &application.RunGate{Kind: application.GatePlanReview}}); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../../migrations/down/350_durable_resume_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	var n int
	_ = owner.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND
		((table_name='request_runs' AND column_name IN ('gate','chat')) OR (table_name='task_checkpoints' AND column_name='gateway'))`, env.schema).Scan(&n)
	if n != 0 {
		t.Fatalf("down left %d columns", n)
	}
	var reg *string
	_ = owner.QueryRow(ctx, `SELECT to_regclass('approval_executions')::text`).Scan(&reg)
	if reg != nil {
		t.Fatalf("down left the ledger: %s", *reg)
	}
	var rows int
	_ = owner.QueryRow(ctx, `SELECT count(*) FROM request_runs`).Scan(&rows)
	if rows != 1 {
		t.Fatalf("run meta rows after down = %d (the 290 data must survive)", rows)
	}
	up, err := migrationsFS.ReadFile("migrations/350_durable_resume.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // idempotent
		if _, err := owner.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up after down: %v", err)
		}
	}
	if ok, _, err := st.ClaimExecution(ctx, "org-a", application.ApprovalExecution{ApprovalID: "ap-1"}); err != nil || !ok {
		t.Fatalf("the ledger works again as app_user: %v %v", ok, err)
	}
}
