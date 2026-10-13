package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/docindex"
	"aiworkforce/backend/internal/drive"
)

// Integration test of migration 690 (passage index). Needs TEST_DATABASE_URL
// (see store_rls_test.go); skipped otherwise.

type pgEmbed struct{}

func (pgEmbed) Provider() string { return "openai" }
func (pgEmbed) Model() string    { return "fake" }
func (pgEmbed) Embed(_ context.Context, texts []string) ([][]float32, int, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := []float32{0.01, 0.01, 0.01}
		low := strings.ToLower(t)
		if strings.Contains(low, "car") || strings.Contains(low, "automobile") {
			v[0] = 1
		}
		if strings.Contains(low, "invoice") || strings.Contains(low, "bill") {
			v[1] = 1
		}
		out[i] = v
	}
	return out, len(texts), nil
}

func TestDocIndexPostgres(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	var idx *docindex.Service
	dsvc := drive.New(drive.Config{Store: &DriveStore{S: env.Store}, Objects: &ObjectStore{S: env.Store}, OrgID: "org-a",
		Audit: func(_ context.Context, org string, e drive.AuditEntry) {
			if idx != nil {
				idx.OnDriveAudit(org, e.Action, e.Entity, e.EntityID)
			}
		}})
	st := &DocIndexStore{S: env.Store}
	idx = docindex.New(docindex.Config{Store: st, Drive: dsvc, OrgID: "org-a",
		Embeddings: func(context.Context, string) docindex.EmbedChoice { return docindex.EmbedChoice{Embedder: pgEmbed{}} }})
	ctxA, ctxB := application.WithOrg(ctx, "org-a"), application.WithOrg(ctx, "org-b")
	write := func(c context.Context, p, text string) drive.File {
		t.Helper()
		r, err := dsvc.Write(c, drive.Human("u"), drive.WriteInput{Path: p, Data: []byte(text)})
		if err != nil {
			t.Fatal(err)
		}
		return r.File
	}
	var b strings.Builder
	for p := 1; p <= 8; p++ {
		fmt.Fprintf(&b, "[page %d]\nEl informe trimestral describe las operaciones de la empresa en la región.\n", p)
		if p == 6 {
			b.WriteString("Las facturas pendientes del proveedor suman 12.000 euros.\n")
		}
	}
	rep := write(ctxA, "/Shared/informe.txt", b.String())
	write(ctxA, "/Agents/legal/secreto.md", "Las facturas de la fusión son confidenciales.")
	write(ctxA, "/Shared/fleet.md", "We bought a new car for the sales team.")
	write(ctxB, "/Shared/b.md", "Facturas de la organización B.")

	if n := idx.Sync(ctx, "org-a"); n != 3 {
		t.Fatalf("backfill org-a: %d", n)
	}
	idx.Sync(ctx, "org-b")

	// FTS with Spanish stemming: "factura" finds "facturas", page carried into the citation
	r, err := idx.SearchChunks(ctxA, drive.ChunkQuery{Query: "factura proveedor", Mode: "keyword"})
	if err != nil || len(r.Hits) == 0 || r.Hits[0].Path != "/Shared/informe.txt" || r.Hits[0].Page != 6 {
		t.Fatalf("keyword: %v %+v", err, r.Hits)
	}
	// semantic + hybrid over real[] vectors
	r, err = idx.SearchChunks(ctxA, drive.ChunkQuery{Query: "automobile", Mode: "semantic"})
	if err != nil || len(r.Hits) == 0 || r.Hits[0].Path != "/Shared/fleet.md" {
		t.Fatalf("semantic: %v %+v", err, r)
	}
	r, _ = idx.SearchChunks(ctxA, drive.ChunkQuery{Query: "facturas"})
	if r.Mode != drive.SearchHybrid || len(r.Hits) < 2 {
		t.Fatalf("hybrid (person, unscoped): %+v", r)
	}
	// agent scope: the forbidden desk never appears, in any mode
	fs := &drive.AgentFS{Svc: dsvc, Chunks: idx}
	roots, _ := fs.ReadableRoots(ctxA, "sales")
	for _, m := range []string{"keyword", "semantic", "hybrid"} {
		r, err := idx.SearchChunks(ctxA, drive.ChunkQuery{Query: "facturas fusión confidenciales", Mode: m, Under: roots})
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range r.Hits {
			if strings.HasPrefix(h.Path, "/Agents/legal") {
				t.Fatalf("%s leaked %s", m, h.Path)
			}
		}
	}
	// tenant isolation: org-b sees only its own passages, and raw SQL as org-b sees no org-a rows
	r, _ = idx.SearchChunks(ctxB, drive.ChunkQuery{Query: "facturas", Mode: "hybrid"})
	if len(r.Hits) != 1 || r.Hits[0].Path != "/Shared/b.md" {
		t.Fatalf("org-b: %+v", r.Hits)
	}
	for _, tbl := range []string{"doc_chunks", "doc_index_state"} {
		var n int
		_ = env.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT count(*) FROM "+tbl+" WHERE org_id='org-a'").Scan(&n)
		})
		if n != 0 {
			t.Fatalf("%s: org-b sees %d org-a rows", tbl, n)
		}
	}
	// new version: old text hidden at once, re-indexed by the next sync
	write(ctxA, "/Shared/informe.txt", "[page 1]\nNuevo informe sin pendientes.\n")
	if r, _ := idx.SearchChunks(ctxA, drive.ChunkQuery{Query: "proveedor", Mode: "keyword"}); len(r.Hits) != 0 {
		t.Fatalf("old version searchable: %+v", r.Hits)
	}
	idx.Sync(ctx, "org-a")
	if r, _ := idx.SearchChunks(ctxA, drive.ChunkQuery{Query: "nuevo informe", Mode: "keyword"}); len(r.Hits) != 1 || r.Hits[0].Version != 2 {
		t.Fatalf("v2: %+v", r.Hits)
	}
	// trash: hidden at once, chunks deleted by the reconcile
	if err := dsvc.DeleteFile(ctxA, drive.Human("u"), rep.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ := idx.SearchChunks(ctxA, drive.ChunkQuery{Query: "nuevo informe", Mode: "keyword"}); len(r.Hits) != 0 {
		t.Fatal("trashed file searchable")
	}
	idx.Sync(ctx, "org-a")
	var left int
	_ = env.WithOrgTx(ctx, "org-a", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM doc_chunks WHERE file_id=$1", rep.ID).Scan(&left)
	})
	if left != 0 {
		t.Fatalf("chunks of a trashed file left: %d", left)
	}
	// status per folder
	sc, err := idx.Status(ctxA, "/Shared")
	if err != nil || sc.Indexed != 1 || sc.Chunks == 0 {
		t.Fatalf("status: %v %+v", err, sc)
	}
	// claims are exclusive across replicas (SKIP LOCKED + lease)
	write(ctxA, "/Shared/q1.md", "uno")
	write(ctxA, "/Shared/q2.md", "dos")
	if _, err := st.Reconcile(ctxA, "org-a", 100); err != nil {
		t.Fatal(err)
	}
	j1, err1 := st.Claim(ctxA, "org-a", 1, time.Minute)
	j2, err2 := st.Claim(ctxA, "org-a", 5, time.Minute)
	if err1 != nil || err2 != nil || len(j1) != 1 || len(j2) != 1 || j1[0].FileID == j2[0].FileID {
		t.Fatalf("claims: %v %v %+v %+v", err1, err2, j1, j2)
	}
	if j3, _ := st.Claim(ctxA, "org-a", 5, time.Minute); len(j3) != 0 {
		t.Fatalf("leased jobs claimed twice: %+v", j3)
	}
	// turning indexing off deletes the organization's index
	if _, err := idx.SetEnabled(ctxA, "admin", false); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.SearchChunks(ctxA, drive.ChunkQuery{Query: "car"}); err != drive.ErrIndexOff {
		t.Fatalf("off: %v", err)
	}
	if n, _ := st.ChunkCount(ctxA, "org-a"); n != 0 {
		t.Fatalf("index left after off: %d", n)
	}
	// daily usage booked
	if u, _ := st.EmbedUsage(ctxA, "org-a", time.Now()); u == 0 {
		t.Fatal("embedding usage not booked")
	}
}
