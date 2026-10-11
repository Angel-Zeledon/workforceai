package postgres

import (
	"context"
	"github.com/jackc/pgx/v5"
	"os"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/drive/drivetest"
	"aiworkforce/backend/internal/objstore/objstoretest"
)

// Integration tests of migrations 480/481 (see store_rls_test.go for how to run
// them: TEST_DATABASE_URL).

func driveSvc(env *testEnv) *drive.Service {
	return drive.New(drive.Config{Store: &DriveStore{S: env.Store}, Objects: &ObjectStore{S: env.Store}, OrgID: "org-a"})
}

func TestDriveStoreSuite(t *testing.T) {
	env := testStore(t)
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	drivetest.Run(t, driveSvc(env), "org-a", "org-b", drivetest.Options{Stemming: true})
}

func TestPostgresObjectStoreContract(t *testing.T) {
	env := testStore(t)
	seedOrg(t, env.Store, "org-a")
	objstoretest.Run(t, &ObjectStore{S: env.Store}, "org-a")
}

func TestDriveRLSAndImmutability(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	svc := driveSvc(env)
	ctxA := application.WithOrg(ctx, "org-a")
	w, err := svc.Write(ctxA, drive.Human("u"), drive.WriteInput{Path: "/Shared/a.txt", Data: []byte("hola")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Write(ctxA, drive.Human("u"), drive.WriteInput{Path: "/Shared/a.txt", Data: []byte("hola 2")}); err != nil {
		t.Fatal(err)
	}
	// Raw statements as app_user inside the wrong org: RLS hides and refuses everything.
	for _, tbl := range []string{"drive_folders", "drive_files", "drive_versions", "drive_grants", "drive_objects"} {
		var n int
		err := env.WithOrgTx(ctx, "org-b", func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&n)
		})
		if err != nil || n != 0 {
			t.Fatalf("%s: org-b sees %d rows (%v)", tbl, n, err)
		}
	}
	if err := env.exec(ctx, "org-b", `UPDATE drive_files SET name='hacked'`); err != nil {
		t.Fatal(err) // zero rows, no error
	}
	if err := env.exec(ctx, "org-b", `INSERT INTO drive_folders (org_id, id, name, path, root_kind, created_by) VALUES ('org-a','x','x','/Shared/x','shared','{}')`); err == nil {
		t.Fatal("org-b wrote a folder of org-a (WITH CHECK)")
	}
	// Versions are immutable and nothing is deletable for the application role.
	if err := env.exec(ctx, "org-a", `UPDATE drive_versions SET message='x'`); err == nil {
		t.Fatal("drive_versions must be immutable")
	}
	for _, tbl := range []string{"drive_versions", "drive_files", "drive_folders", "drive_grants"} {
		if err := env.exec(ctx, "org-a", `DELETE FROM `+tbl); err == nil {
			t.Fatalf("%s must not be deletable by app_user", tbl)
		}
	}
	// A version of org-a cannot be attached to a file of org-b (FK by org).
	if err := env.exec(ctx, "org-b", `INSERT INTO drive_versions (org_id, file_id, version, object_key, sha256, size_bytes, mime, author) VALUES ('org-b',$1,9,'k','s',1,'text/plain','{}')`, w.File.ID); err == nil {
		t.Fatal("cross-tenant version insert must fail")
	}
	// Case-insensitive uniqueness among live files.
	if err := env.exec(ctx, "org-a", `INSERT INTO drive_files (org_id, id, folder_id, name, mime, size_bytes, current_version, created_by, updated_by) VALUES ('org-a','dup',$1,'A.TXT','text/plain',1,1,'{}','{}')`, w.File.FolderID); err == nil {
		t.Fatal("duplicate live name (case-insensitive) must fail")
	}
	// The file-name length and the mime of search text are bounded by CHECKs.
	// Down migration removes everything.
	for _, f := range []string{"481_artifact_blobs_object_down.sql", "480_drive_down.sql"} {
		down, err := os.ReadFile("../../../migrations/down/" + f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.owner(t).Exec(ctx, string(down)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	var n int
	if err := env.owner(t).QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name LIKE 'drive_%'`, env.schema).Scan(&n); err != nil || n != 0 {
		t.Fatalf("drive tables after down: %d %v", n, err)
	}
}
