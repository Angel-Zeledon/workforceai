package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/domain"
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
