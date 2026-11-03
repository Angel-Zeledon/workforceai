package postgres

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
)

// `server ctl objects-to-s3` (TEST_DATABASE_URL): copies drive files and PDF blobs from Postgres to S3,
// verifies, is idempotent and resumable, never deletes a copy that does not verify, and the drive keeps
// reading every file through objstore.Tiered before, during and after the move.
func TestMigrateObjectsToS3(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	pgObjs := &ObjectStore{S: env.Store}
	files := map[string]string{"org-a": "/Shared/a.md", "org-b": "/Shared/b.md"}
	dsvcPG := drive.New(drive.Config{Store: &DriveStore{S: env.Store}, Objects: pgObjs, OrgID: "org-a"})
	ids := map[string]string{}
	for org, p := range files {
		w, err := dsvcPG.Write(application.WithOrg(ctx, org), drive.Human("u"), drive.WriteInput{Path: p, Data: []byte("contenido de " + org)})
		if err != nil {
			t.Fatal(err)
		}
		ids[org] = w.File.ID
		if _, err := dsvcPG.Write(application.WithOrg(ctx, org), drive.Human("u"), drive.WriteInput{Path: p, Data: []byte("v2 de " + org)}); err != nil {
			t.Fatal(err)
		}
	}
	legacy := &ArtifactStore{S: env.Store}
	st, v1 := artifactFixture("org-a", "art_pdf")
	if err := legacy.Create(ctx, "org-a", st, v1); err != nil {
		t.Fatal(err)
	}
	pdf := []byte("%PDF-1.4\nold\n%%EOF\n")
	blob := artifacts.Blob{ID: "blob_0123456789abcdef0123", ArtifactID: "art_pdf", MIME: "application/pdf", Filename: "old.pdf", SHA256: objstore.SumBytes(pdf),
		Size: len(pdf), Data: pdf, CreatedBy: artifacts.ActorRef{Kind: "user", ID: "u1"}, CreatedAt: time.Now().UTC()}
	if err := legacy.PutBlob(ctx, "org-a", blob); err != nil {
		t.Fatal(err)
	}

	pool, err := pgxpool.New(ctx, env.rawURL) // owner connection, as the ctl command uses
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	orgs, err := OrgsWithObjects(ctx, pool, "org-a")
	if err != nil || len(orgs) < 2 {
		t.Fatalf("orgs: %v %v", orgs, err)
	}
	s3 := objstore.NewMemory()
	tiered := &objstore.Tiered{Primary: s3, Fallback: pgObjs}
	dsvc := drive.New(drive.Config{Store: &DriveStore{S: env.Store}, Objects: tiered, OrgID: "org-a"})
	read := func(org string) string {
		f, err := dsvc.GetFile(application.WithOrg(ctx, org), ids[org])
		if err != nil {
			t.Fatal(err)
		}
		rc, _, err := dsvc.Open(application.WithOrg(ctx, org), f, 1)
		if err != nil {
			t.Fatalf("read %s v1: %v", org, err)
		}
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		return string(b)
	}
	if read("org-a") != "contenido de org-a" {
		t.Fatal("before: tiered must read from Postgres")
	}

	// dry run: nothing copied, nothing changed
	rep, err := MigrateObjects(ctx, pool, s3, orgs, ObjectsMigrateOptions{DryRun: true})
	if err != nil || rep.Seen != 5 || rep.Copied != 0 || s3.Len() != 0 {
		t.Fatalf("dry run: %+v %v", rep, err)
	}
	// copy only: everything in S3, Postgres untouched
	rep, err = MigrateObjects(ctx, pool, s3, orgs, ObjectsMigrateOptions{Batch: 2})
	if err != nil || rep.Copied != 5 || rep.Failed != 0 || s3.Len() != 5 {
		t.Fatalf("copy: %+v %v", rep, err)
	}
	// idempotent: a second run copies nothing
	rep, err = MigrateObjects(ctx, pool, s3, orgs, ObjectsMigrateOptions{})
	if err != nil || rep.Copied != 0 || rep.Already != 5 {
		t.Fatalf("rerun: %+v %v", rep, err)
	}
	// a corrupted copy in S3 is replaced, a corrupted Postgres row is never deleted
	key := objstore.OrgKey("org-b", "drive", ids["org-b"], "v1")
	if _, err := s3.Put(ctx, key, bytes.NewReader([]byte("basura...........")), 17, objstore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	badKey := objstore.OrgKey("org-a", "drive", ids["org-a"], "v2")
	if err := env.exec(ctx, "org-a", `UPDATE drive_objects SET sha256='0000' WHERE org_id='org-a' AND key=$1`, badKey); err != nil {
		t.Fatal(err)
	}
	rep, err = MigrateObjects(ctx, pool, s3, orgs, ObjectsMigrateOptions{Delete: true})
	if err != nil || rep.Failed != 1 || rep.Deleted != 4 {
		t.Fatalf("delete: %+v %v", rep, err)
	}
	var left int
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.org_id','org-a',true)`); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM drive_objects`).Scan(&left)
	}); err != nil || left != 1 {
		t.Fatalf("only the unverifiable object stays in Postgres: %d %v", left, err)
	}
	// after: every file and the PDF are read from S3; the PDF row now holds the key
	if read("org-a") != "contenido de org-a" || read("org-b") != "contenido de org-b" {
		t.Fatal("after: files must still read")
	}
	as := &ArtifactStore{S: env.Store, Objects: tiered}
	got, err := as.GetBlob(ctx, "org-a", blob.ID)
	if err != nil || !bytes.Equal(got.Data, pdf) || got.ObjectKey == "" {
		t.Fatalf("pdf after move: %v %+v", err, got.ObjectKey)
	}
}
