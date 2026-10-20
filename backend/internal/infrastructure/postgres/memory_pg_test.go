package postgres

import (
	"context"
	"strings"
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

// Integration test of migration 786 (embeddings as real[]): vectors are kept
// while the text is unchanged, dropped when it changes, isolated by
// organization and agent, and never compared across models.
func TestMemoryEmbeddingsPostgres(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	m := &MemoryStore{S: env.Store}
	now := time.Now().UTC()
	vec := []float32{1, 0, 0.1}
	doc := memory.Doc{ID: "task_1", Kind: memory.KindTask, Ref: "task:1", Title: "Cobro pendiente", Body: "clientes morosos", Scope: memory.ScopeOrg, AgentID: "fin",
		CreatedAt: now, UpdatedAt: now, Vec: vec, EmbedModel: "fake"}
	note := memory.Doc{ID: "note_1", Kind: memory.KindNote, Ref: "memory:1", Title: "Privada", Body: "solo del agente fin", Scope: memory.ScopeAgent, AgentID: "fin",
		CreatedAt: now, UpdatedAt: now, Vec: vec, EmbedModel: "fake"}
	for _, d := range []memory.Doc{doc, note} {
		if err := m.Upsert(ctx, "org-a", d); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Upsert(ctx, "org-b", memory.Doc{ID: "task_1", Kind: memory.KindTask, Ref: "task:1", Title: "Otra org", Scope: memory.ScopeOrg, CreatedAt: now, UpdatedAt: now,
		Vec: vec, EmbedModel: "fake"}); err != nil {
		t.Fatal(err)
	}
	ids := func(org, agent, model string) string {
		cs, err := m.Candidates(ctx, org, memory.VecQuery{AgentID: agent, Model: model, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range cs {
			if len(c.Vec) != 3 {
				t.Fatalf("vector round trip: %v", c.Vec)
			}
			out = append(out, c.ID)
		}
		return strings.Join(out, ",")
	}
	if got := ids("org-a", "fin", "fake"); got != "note_1,task_1" && got != "task_1,note_1" {
		t.Fatalf("owner sees the document and its note: %q", got)
	}
	if got := ids("org-a", "ops", "fake"); got != "task_1" {
		t.Fatalf("another agent does not see a private note: %q", got)
	}
	if got := ids("org-a", "fin", "other-model"); got != "" {
		t.Fatalf("vectors of another model are not candidates: %q", got)
	}
	if got := ids("org-b", "fin", "fake"); got != "task_1" {
		t.Fatalf("org B sees only its own row: %q", got)
	}
	var n int
	if err := env.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM memory_docs WHERE embedding IS NOT NULL AND title <> 'Otra org'`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("RLS hides org A vectors from org B: %d %v", n, err)
	}

	// Same text without a vector (re-index while the provider is off): the vector stays.
	again := doc
	again.Vec, again.EmbedModel = nil, ""
	if err := m.Upsert(ctx, "org-a", again); err != nil {
		t.Fatal(err)
	}
	if got := ids("org-a", "ops", "fake"); got != "task_1" {
		t.Fatalf("an unchanged text keeps its vector: %q", got)
	}
	// Changed text without a new vector: never a stale vector.
	again.Body = "clientes morosos del segundo trimestre"
	if err := m.Upsert(ctx, "org-a", again); err != nil {
		t.Fatal(err)
	}
	if got := ids("org-a", "ops", "fake"); got != "" {
		t.Fatalf("a changed text loses its vector: %q", got)
	}
	pend, err := m.Unembedded(ctx, "org-a", "fake", 10)
	if err != nil || len(pend) != 1 || pend[0].ID != "task_1" {
		t.Fatalf("the changed document is pending: %v %+v", err, pend)
	}
	// SetEmbedding: ignored for a stale hash, applied for the current one.
	if err := m.SetEmbedding(ctx, "org-a", "task_1", "stale", "fake", []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	if got := ids("org-a", "ops", "fake"); got != "" {
		t.Fatalf("a vector computed for an older text is ignored: %q", got)
	}
	if err := m.SetEmbedding(ctx, "org-a", "task_1", memory.ContentHash(again), "fake", []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	if got := ids("org-a", "ops", "fake"); got != "task_1" {
		t.Fatalf("backfill stores the vector: %q", got)
	}
	// A suppressed document is never a candidate.
	if err := m.Suppress(ctx, "org-a", "task_1", "ana", now); err != nil {
		t.Fatal(err)
	}
	if got := ids("org-a", "ops", "fake"); got != "" {
		t.Fatalf("suppressed: %q", got)
	}
}
