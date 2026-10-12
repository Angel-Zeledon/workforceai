package sandbox

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/objstore"
)

func TestDriveFilesAdapter(t *testing.T) {
	dr := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: "org-a"})
	ctxA, ctxB := application.WithOrg(context.Background(), "org-a"), application.WithOrg(context.Background(), "org-b")
	w, err := dr.Write(ctxA, drive.Human("u"), drive.WriteInput{Path: "/Shared/datos.csv", Data: []byte("a,b\n1,2\n")})
	if err != nil {
		t.Fatal(err)
	}
	var notified []string
	df := &DriveFiles{Drive: dr, Notify: func(_ context.Context, org, agent, p string) { notified = append(notified, org+"|"+agent+"|"+p) }}
	bg := context.Background()

	for _, ref := range []string{w.File.ID, "/Shared/datos.csv"} { // by id and by path
		name, rc, err := df.Read(bg, "org-a", ref)
		if err != nil || name != "datos.csv" {
			t.Fatalf("read %s: %q %v", ref, name, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if string(b) != "a,b\n1,2\n" {
			t.Fatalf("content %q", b)
		}
	}
	// Another organization never sees it, by id or by path, and a bad ref is just "not found".
	_ = ctxB
	for _, ref := range []string{w.File.ID, "/Shared/datos.csv", "nope", "/Shared/../x"} {
		if _, _, err := df.Read(bg, "org-b", ref); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("org-b read %q: %v", ref, err)
		}
	}
	if err := dr.DeleteFile(ctxA, drive.Human("u"), w.File.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := df.Read(bg, "org-a", w.File.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a deleted file must not be an input: %v", err)
	}

	// Outputs go to the agent's desk, as the agent, and rewriting makes a new version.
	ref, err := df.WriteOutput(bg, "org-a", "analyst", "outputs/run_1", "r.csv", strings.NewReader("x\n1\n"))
	if err != nil || ref != "/Agents/analyst/outputs/run_1/r.csv" {
		t.Fatalf("ref %q %v", ref, err)
	}
	if _, err := df.WriteOutput(bg, "org-a", "analyst", "../../Shared", "r.csv", strings.NewReader("x\n2\n")); err != nil {
		t.Fatal(err) // the hint is not a trusted path: only its last segment is used
	}
	if _, err := dr.FileByPath(ctxA, "/Shared/r.csv"); err == nil {
		t.Fatal("the folder hint escaped the desk")
	}
	f, _ := dr.FileByPath(ctxA, ref)
	if f.CreatedBy != drive.AgentP("analyst") || f.CurrentVersion != 1 {
		t.Fatalf("file %+v", f)
	}
	if _, err := df.WriteOutput(bg, "org-a", "analyst", "outputs/run_1", "r.csv", strings.NewReader("x\n3\n")); err != nil {
		t.Fatal(err)
	}
	if f, _ = dr.FileByPath(ctxA, ref); f.CurrentVersion != 2 {
		t.Fatalf("expected a new version, got v%d", f.CurrentVersion)
	}
	if len(notified) != 3 || notified[0] != "org-a|analyst|"+ref {
		t.Fatalf("notified %v", notified)
	}
	if _, err := dr.FileByPath(ctxB, ref); err == nil {
		t.Fatal("org-b sees the output")
	}
}
