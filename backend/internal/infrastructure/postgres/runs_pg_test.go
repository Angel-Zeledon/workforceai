package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// Durable runs (migration 290): round trip, tenant isolation under RLS and reset.
func TestRunStoreRoundTripAndIsolation(t *testing.T) {
	st := testStore(t).Store
	ctx := context.Background()
	seedOrg(t, st, "org-a")
	seedOrg(t, st, "org-b")

	meta := application.RunMeta{RequestID: "req-1", RequestedBy: "u1", ConversationID: "c1", ReadOnly: true, RemovedTaskIDs: []string{"t9"}}
	if err := st.PutRunMeta(ctx, "org-a", meta); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRunMeta(ctx, "org-a", "req-1")
	if err != nil || !got.ReadOnly || got.RequestedBy != "u1" || len(got.RemovedTaskIDs) != 1 {
		t.Fatalf("run meta = %+v, %v", got, err)
	}
	cp := application.TaskCheckpoint{TaskID: "task-1", RequestID: "req-1", ApprovalID: "ap-1", ToolIndex: 1,
		Tools:  []application.ToolRequest{{Tool: "email", Action: "read"}, {Tool: "email", Action: "send_proposal", Args: map[string]any{"to": "x"}, Risk: "high"}},
		Output: domain.StructuredOutput{Summary: "s"}, Taint: &application.CheckpointTaint{Tainted: true, Conns: []string{"conn-1"}}}
	if err := st.PutCheckpoint(ctx, "org-a", cp); err != nil {
		t.Fatal(err)
	}
	cp.Executed = true
	if err := st.PutCheckpoint(ctx, "org-a", cp); err != nil { // upsert
		t.Fatal(err)
	}
	c, err := st.GetCheckpoint(ctx, "org-a", "task-1")
	if err != nil || !c.Executed || c.ToolIndex != 1 || len(c.Tools) != 2 || c.Tools[1].Args["to"] != "x" || c.Taint == nil || !c.Taint.Tainted {
		t.Fatalf("checkpoint = %+v, %v", c, err)
	}

	// Another tenant sees nothing, even with the ids.
	if _, err := st.GetRunMeta(ctx, "org-b", "req-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("org-b reads org-a run meta: %v", err)
	}
	if _, err := st.GetCheckpoint(ctx, "org-b", "task-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("org-b reads org-a checkpoint: %v", err)
	}
	err = st.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		for _, tb := range []string{"request_runs", "task_checkpoints"} {
			var n int
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+tb).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Errorf("%s: org-b transaction sees %d rows of org-a", tb, n)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Delete and reset.
	if err := st.DeleteCheckpoint(ctx, "org-a", "task-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetCheckpoint(ctx, "org-a", "task-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deleted checkpoint still readable: %v", err)
	}
	if err := st.Reset(ctx, "org-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetRunMeta(ctx, "org-a", "req-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("reset must remove run meta: %v", err)
	}
}
