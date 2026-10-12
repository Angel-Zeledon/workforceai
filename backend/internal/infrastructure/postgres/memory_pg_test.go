package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/memory"
	"aiworkforce/backend/internal/memory/memorytest"
)

// Integration test of migration 550 (see store_rls_test.go: TEST_DATABASE_URL):
// the contract suite on Postgres, plus RLS and the tombstone at SQL level.

func TestMemoryStoreSuite(t *testing.T) {
	env := testStore(t)
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	memorytest.Run(t, &MemoryStore{S: env.Store}, driveSvc(env), "org-a", "org-b")
}

func TestMemoryDocsRLSAndTombstone(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	m := &MemoryStore{S: env.Store}
	now := time.Now().UTC()
	doc := memory.Doc{ID: "task_1", Kind: memory.KindTask, Ref: "task:1", Title: "Año fiscal", Body: "cierre contable", Scope: memory.ScopeOrg, AgentID: "finance", CreatedAt: now, UpdatedAt: now}
	if err := m.Upsert(ctx, "org-a", doc); err != nil {
		t.Fatal(err)
	}
	// a row of org A cannot be written, read or counted through org B's scope
	if err := m.Upsert(ctx, "org-b", memory.Doc{ID: "task_1", Kind: memory.KindTask, Ref: "task:1", Scope: memory.ScopeOrg, CreatedAt: now}); err != nil {
		t.Fatalf("same id in another organization is a different row: %v", err)
	}
	var n int
	if err := env.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM memory_docs WHERE title <> ''`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("RLS must hide org A rows from org B even without a WHERE org_id: %d %v", n, err)
	}
	if err := env.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO memory_docs (org_id, id, kind, ref) VALUES ('org-a','x','task','task:x')`)
		return err
	}); err == nil {
		t.Fatal("RLS WITH CHECK must refuse writing a row of another organization")
	}
	hits, _ := m.Search(ctx, "org-a", memory.SearchParams{Terms: []string{"ano", "fiscal"}, AgentID: "x"})
	if len(hits) != 1 || hits[0].Score <= 0 || hits[0].Score >= 1 {
		t.Fatalf("accent-insensitive match with a score in (0,1): %+v", hits)
	}
	if err := m.Suppress(ctx, "org-a", "task_1", "ana", now); err != nil {
		t.Fatal(err)
	}
	doc.Body = "cierre contable actualizado"
	if err := m.Upsert(ctx, "org-a", doc); err != nil {
		t.Fatal(err)
	}
	if hits, _ := m.Search(ctx, "org-a", memory.SearchParams{Terms: []string{"fiscal"}, AgentID: "x"}); len(hits) != 0 {
		t.Fatalf("a suppressed document stays suppressed after an upsert: %+v", hits)
	}
}
