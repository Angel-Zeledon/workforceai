package postgres

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/objstore"
	"github.com/jackc/pgx/v5"
)

// Integration test of migration 370 (artifact_blobs); see store_rls_test.go
// for how to run it (TEST_DATABASE_URL).

func TestArtifactBlobsRoundTripIsolationAndDown(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	as := &ArtifactStore{S: env.Store}
	st, v1 := artifactFixture("org-a", "art_pdf")
	if err := as.Create(ctx, "org-a", st, v1); err != nil {
		t.Fatal(err)
	}
	data := []byte("%PDF-1.4\n%%EOF\n")
	b := artifacts.Blob{ID: "blob_0123456789abcdef0123", ArtifactID: "art_pdf", MIME: "application/pdf", Filename: "c.pdf", SHA256: "ab",
		Size: len(data), Data: data, CreatedBy: artifacts.ActorRef{Kind: "user", ID: "u1"}, CreatedAt: time.Now().UTC()}
	if err := as.PutBlob(ctx, "org-a", b); err != nil {
		t.Fatal(err)
	}
	got, err := as.GetBlob(ctx, "org-a", b.ID)
	if err != nil || string(got.Data) != string(data) || got.CreatedBy.ID != "u1" || got.Filename != "c.pdf" {
		t.Fatalf("round trip: %v %+v", err, got)
	}
	if _, err := as.GetBlob(ctx, "org-b", b.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant read: %v", err)
	}
	// org-b cannot attach a blob to an artifact of org-a (FK by org) nor write rows of org-a (RLS).
	other := b
	other.ID = "blob_fedcba9876543210fedc"
	if err := as.PutBlob(ctx, "org-b", other); err == nil {
		t.Fatal("blob for an artifact of another org must fail")
	}
	if err := as.S.exec(ctx, "org-a", `UPDATE artifact_blobs SET filename='x'`); err == nil {
		t.Fatal("artifact_blobs must be immutable for app_user")
	}
	if err := as.S.exec(ctx, "org-a", `DELETE FROM artifact_blobs`); err == nil {
		t.Fatal("artifact_blobs must not be deletable by app_user")
	}
	bad := b
	bad.ID, bad.MIME = "blob_aaaaaaaaaaaaaaaaaaaaaaaa", "text/html"
	if err := as.PutBlob(ctx, "org-a", bad); err == nil {
		t.Fatal("non-pdf mime must be refused by the CHECK")
	}

	down, err := os.ReadFile("../../../migrations/down/370_artifact_blobs_down.sql")
	if err != nil {
		t.Fatal(err)
	}
	owner := env.owner(t)
	if _, err := owner.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down: %v", err)
	}
	var n int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name='artifact_blobs'`, env.schema).Scan(&n); err != nil || n != 0 {
		t.Fatalf("table still present after down: %d %v", n, err)
	}
}

// F1: with an object store configured new PDFs live there and the row only
// keeps the key; PDFs stored as bytea before keep being read.
func TestArtifactBlobsObjectStoreAndLegacyBytea(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	legacy := &ArtifactStore{S: env.Store}
	st, v1 := artifactFixture("org-a", "art_pdf")
	if err := legacy.Create(ctx, "org-a", st, v1); err != nil {
		t.Fatal(err)
	}
	old := []byte("%PDF-1.4\nold\n%%EOF\n")
	b1 := artifacts.Blob{ID: "blob_0123456789abcdef0123", ArtifactID: "art_pdf", MIME: "application/pdf", Filename: "old.pdf", SHA256: objstore.SumBytes(old),
		Size: len(old), Data: old, CreatedBy: artifacts.ActorRef{Kind: "user", ID: "u1"}, CreatedAt: time.Now().UTC()}
	if err := legacy.PutBlob(ctx, "org-a", b1); err != nil {
		t.Fatal(err)
	}

	mem := objstore.NewMemory()
	as := &ArtifactStore{S: env.Store, Objects: mem}
	data := []byte("%PDF-1.4\nnew\n%%EOF\n")
	b2 := b1
	b2.ID, b2.Filename, b2.Data, b2.Size, b2.SHA256 = "blob_fedcba9876543210fedc", "new.pdf", data, len(data), objstore.SumBytes(data)
	if err := as.PutBlob(ctx, "org-a", b2); err != nil {
		t.Fatal(err)
	}
	if mem.Len() != 1 {
		t.Fatalf("object not stored: %v", mem.Keys())
	}
	var nulls int
	if err := env.WithOrgTx(ctx, "org-a", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM artifact_blobs WHERE data IS NULL AND object_key IS NOT NULL`).Scan(&nulls)
	}); err != nil || nulls != 1 {
		t.Fatalf("row must hold the key, not the bytes: %d %v", nulls, err)
	}
	got, err := as.GetBlob(ctx, "org-a", b2.ID)
	if err != nil || !bytes.Equal(got.Data, data) || got.Filename != "new.pdf" {
		t.Fatalf("object blob: %v %+v", err, got)
	}
	// old bytea blob still readable with and without the object store
	for _, s := range []*ArtifactStore{as, legacy} {
		got, err = s.GetBlob(ctx, "org-a", b1.ID)
		if err != nil || !bytes.Equal(got.Data, old) {
			t.Fatalf("legacy blob: %v", err)
		}
	}
	// an object-backed blob without the store configured fails loudly, not with empty bytes
	if _, err := legacy.GetBlob(ctx, "org-a", b2.ID); err == nil {
		t.Fatal("object blob read without an object store must fail")
	}
	// tenant isolation and immutability still hold
	if _, err := as.GetBlob(ctx, "org-b", b2.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant: %v", err)
	}
	if err := env.exec(ctx, "org-a", `UPDATE artifact_blobs SET object_key='orgs/x/y'`); err == nil {
		t.Fatal("blobs must stay immutable")
	}
	// the CHECK refuses a row with neither bytes nor key
	if err := env.exec(ctx, "org-a", `INSERT INTO artifact_blobs (id, org_id, artifact_id, mime, filename, sha256, size_bytes, data, created_by)
		VALUES ('blob_aaaaaaaaaaaaaaaaaaaaaaaa','org-a','art_pdf','application/pdf','x','s',1,NULL,'{}')`); err == nil {
		t.Fatal("a blob without bytes and without key must be refused")
	}
}
