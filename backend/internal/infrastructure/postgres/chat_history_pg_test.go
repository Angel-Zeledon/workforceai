package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application/chathistorytest"
)

// Postgres passes the same chat history suite as the memory store, as the
// non-owner app_user role (RLS enforced), one organization pair per subtest.
func TestChatHistorySuite(t *testing.T) {
	env := testStore(t)
	n := 0
	chathistorytest.Run(t, func(t *testing.T) chathistorytest.Fixture {
		n++
		org, other := fmt.Sprintf("ch-org-%d", n), fmt.Sprintf("ch-other-%d", n)
		seedOrg(t, env.Store, org)
		seedOrg(t, env.Store, other)
		return chathistorytest.Fixture{Org: org, Other: other, Core: env.Store, Hist: env.Store}
	})
}

// The migration adds the new columns and table, keeps RLS on chat_reads, and
// its down migration removes them again without touching messages.
func TestChatHistoryMigrationAndRLS(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	if err := env.Store.ChatMarkRead(ctx, "org-a", "thread:general", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	owner, err := pgx.Connect(ctx, env.rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(ctx)
	var rls, force bool
	if err := owner.QueryRow(ctx, `SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE relname = 'chat_reads' AND relnamespace = current_schema()::regnamespace`).Scan(&rls, &force); err != nil {
		t.Fatal(err)
	}
	if !rls || !force {
		t.Fatalf("chat_reads RLS = %v/%v", rls, force)
	}
	// org-b cannot see or move org-a's markers: its own thread list is unread-only
	ts, err := env.Store.ChatThreads(ctx, "org-b", nil)
	if err != nil || len(ts) != 1 || ts[0].Unread != 0 {
		t.Fatalf("org-b threads = %+v, %v", ts, err)
	}
	var visible int
	if err := env.Store.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM chat_reads`).Scan(&visible)
	}); err != nil || visible != 0 {
		t.Fatalf("org-b sees %d chat_reads of org-a (%v)", visible, err)
	}

	downSQL, err := os.ReadFile("../../../migrations/down/570_chat_history_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	down := string(downSQL)
	if _, err := owner.Exec(ctx, down); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	for _, q := range []string{
		`SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'conversations' AND column_name IN ('archived_at','title_custom')`,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'chat_reads'`,
	} {
		var c int
		if err := owner.QueryRow(ctx, q).Scan(&c); err != nil || c != 0 {
			t.Fatalf("after down: %s = %d, %v", strings.Fields(q)[3], c, err)
		}
	}
}
