package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
)

// K1 (migration 600): the context of /clear and /compact, isolated per organization,
// and the memory delete behind /memory. Needs TEST_DATABASE_URL (see store_rls_test.go).
func TestConversationContextStoreAndRLS(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "k1-a")
	seedOrg(t, env.Store, "k1-b")

	if _, ok, err := env.Store.GetConvContext(ctx, "k1-a", "office"); err != nil || ok {
		t.Fatalf("empty = %v %v", ok, err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	in := application.ConvContext{ConversationID: "office", Epoch: 2, ClearedAt: now, Summary: "resumen", SummaryUpto: now.Add(time.Second), SummaryTokens: 42}
	if err := env.Store.PutConvContext(ctx, "k1-a", in); err != nil {
		t.Fatal(err)
	}
	got, ok, err := env.Store.GetConvContext(ctx, "k1-a", "office")
	if err != nil || !ok || got.Epoch != 2 || got.Summary != "resumen" || got.SummaryTokens != 42 || !got.ClearedAt.Equal(now) || !got.SummaryUpto.Equal(now.Add(time.Second)) {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	if !got.Cutoff().Equal(now.Add(time.Second)) {
		t.Fatalf("cutoff = %v", got.Cutoff())
	}
	// never cleared: NULL timestamps come back as the zero time
	if err := env.Store.PutConvContext(ctx, "k1-a", application.ConvContext{ConversationID: "agent:sales", Summary: "s"}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ = env.Store.GetConvContext(ctx, "k1-a", "agent:sales"); !got.ClearedAt.IsZero() || !got.SummaryUpto.IsZero() || got.Summary != "s" {
		t.Fatalf("null times: %+v", got)
	}
	// upsert
	in.Epoch, in.Summary = 3, ""
	if err := env.Store.PutConvContext(ctx, "k1-a", in); err != nil {
		t.Fatal(err)
	}
	if got, _, _ = env.Store.GetConvContext(ctx, "k1-a", "office"); got.Epoch != 3 || got.Summary != "" {
		t.Fatalf("upsert: %+v", got)
	}
	// tenant isolation: the other organization sees nothing, and the table is under forced RLS
	if _, ok, _ := env.Store.GetConvContext(ctx, "k1-b", "office"); ok {
		t.Fatal("k1-b reads k1-a's conversation context")
	}
	var visible int
	if err := env.Store.WithOrgTx(ctx, "k1-b", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM conversation_contexts`).Scan(&visible)
	}); err != nil || visible != 0 {
		t.Fatalf("k1-b sees %d rows (%v)", visible, err)
	}
	owner, err := pgx.Connect(ctx, env.rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(ctx)
	var rls, force bool
	if err := owner.QueryRow(ctx, `SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE relname = 'conversation_contexts' AND relnamespace = current_schema()::regnamespace`).Scan(&rls, &force); err != nil || !rls || !force {
		t.Fatalf("RLS = %v/%v (%v)", rls, force, err)
	}

	// memory delete
	if err := env.Store.SetMemory(ctx, "k1-a", "sales", domain.Memory{Scope: "agent", Key: "k1", Value: "v"}); err != nil {
		t.Fatal(err)
	}
	if err := env.Store.SetMemory(ctx, "k1-a", "sales", domain.Memory{Scope: "agent", Key: "k2", Value: "v"}); err != nil {
		t.Fatal(err)
	}
	if err := env.Store.DeleteMemory(ctx, "k1-a", "sales", "agent", "k1"); err != nil {
		t.Fatal(err)
	}
	if ms, _ := env.Store.ListMemory(ctx, "k1-a", "sales"); len(ms) != 1 || ms[0].Key != "k2" {
		t.Fatalf("after delete: %v", ms)
	}
	// another organization cannot delete it
	_ = env.Store.DeleteMemory(ctx, "k1-b", "sales", "agent", "k2")
	if ms, _ := env.Store.ListMemory(ctx, "k1-a", "sales"); len(ms) != 1 {
		t.Fatalf("cross-tenant delete: %v", ms)
	}

	// the down migration removes the table and nothing else
	down, err := os.ReadFile("../../../migrations/down/600_conversation_context_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down: %v", err)
	}
	var c int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'conversation_contexts'`).Scan(&c); err != nil || c != 0 {
		t.Fatalf("table still there: %d %v", c, err)
	}
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'messages'`).Scan(&c); err != nil || c != 1 {
		t.Fatalf("messages must stay: %d %v", c, err)
	}
}
