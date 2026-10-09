// Package drivetest is the behaviour suite of drive.Store implementations: it
// drives a drive.Service (so the object store and the metadata store are tested
// together) through the scenarios every backend must get right.
package drivetest

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
)

func code(err error) string {
	var e *drive.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

// Options tune what the backend under test is expected to support.
type Options struct {
	// Stemming: the search understands Spanish word forms ("contratos" finds "contrato").
	Stemming bool
}

// Run runs the suite. orgA and orgB must be existing, empty organizations.
func Run(t *testing.T, svc *drive.Service, orgA, orgB string, o Options) {
	ctxA := application.WithOrg(context.Background(), orgA)
	ctxB := application.WithOrg(context.Background(), orgB)
	h := drive.Human("u1")
	ag := drive.AgentP("sales")

	t.Run("roots are virtual until used", func(t *testing.T) {
		l := must[drive.Listing](t)(svc.List(ctxA, "/"))
		if len(l.Folders) != 4 || !l.Folders[0].Virtual {
			t.Fatalf("roots: %+v", l.Folders)
		}
	})

	t.Run("create, version, unchanged, conflict", func(t *testing.T) {
		r := must[drive.WriteResult](t)(svc.Write(ctxA, h, drive.WriteInput{Path: "/Shared/notas.md", Data: []byte("# Notas\nprimer borrador\n"), Message: "first"}))
		if !r.Created || r.File.CurrentVersion != 1 || r.File.Path != "/Shared/notas.md" || r.File.Extraction != "text" {
			t.Fatalf("create: %+v", r)
		}
		same := must[drive.WriteResult](t)(svc.Write(ctxA, h, drive.WriteInput{Path: "/Shared/notas.md", Data: []byte("# Notas\nprimer borrador\n")}))
		if !same.Unchanged || same.File.CurrentVersion != 1 {
			t.Fatalf("same content must not add a version: %+v", same)
		}
		one := 1
		r2 := must[drive.WriteResult](t)(svc.Write(ctxA, ag, drive.WriteInput{Path: "/Shared/notas.md", Data: []byte("# Notas\nsegundo borrador\n"), Expected: &one, Message: "agent edit"}))
		if r2.File.CurrentVersion != 2 || r2.File.UpdatedBy != ag || r2.File.CreatedBy != h {
			t.Fatalf("v2: %+v", r2.File)
		}
		_, err := svc.Write(ctxA, h, drive.WriteInput{Path: "/Shared/notas.md", Data: []byte("otro"), Expected: &one})
		var de *drive.Error
		if !errors.As(err, &de) || de.Code != "version_conflict" || de.Current != 2 || !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("stale write: %v", err)
		}
		vs := must[[]drive.Version](t)(svc.Versions(ctxA, r.File.ID))
		if len(vs) != 2 || vs[0].Version != 2 || vs[0].Author != ag || vs[1].Message != "first" {
			t.Fatalf("versions: %+v", vs)
		}
		// restore v1 = a new version 3 with the old bytes; v2 stays readable
		rr := must[drive.WriteResult](t)(svc.RestoreVersion(ctxA, h, r.File.ID, 1, nil))
		if rr.File.CurrentVersion != 3 {
			t.Fatalf("restore: %+v", rr.File)
		}
		rc, _, err := svc.Open(ctxA, rr.File, 0)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if !strings.Contains(string(b), "primer borrador") {
			t.Fatalf("restored content: %q", b)
		}
		rc, _, _ = svc.Open(ctxA, rr.File, 2)
		b, _ = io.ReadAll(rc)
		rc.Close()
		if !strings.Contains(string(b), "segundo borrador") {
			t.Fatalf("v2 lost: %q", b)
		}
		d := must[drive.DiffResult](t)(svc.Diff(ctxA, rr.File, 1, 2))
		if d.Added != 1 || d.Removed != 1 || !strings.Contains(d.Unified, "-primer borrador") || !strings.Contains(d.Unified, "+segundo borrador") {
			t.Fatalf("diff: %+v", d)
		}
	})

	t.Run("move rename and name clash", func(t *testing.T) {
		a := must[drive.WriteResult](t)(svc.Write(ctxA, h, drive.WriteInput{Path: "/Shared/Docs/a.txt", Data: []byte("A")}))
		must[drive.WriteResult](t)(svc.Write(ctxA, h, drive.WriteInput{Path: "/Shared/Docs/b.txt", Data: []byte("B")}))
		if _, err := svc.MoveFile(ctxA, h, a.File.ID, "/Shared/Docs/B.TXT"); code(err) != "exists" {
			t.Fatalf("case-insensitive clash: %v", err)
		}
		f := must[drive.File](t)(svc.MoveFile(ctxA, h, a.File.ID, "/Shared/Archive/a2.txt"))
		if f.Path != "/Shared/Archive/a2.txt" {
			t.Fatal(f.Path)
		}
		if _, err := svc.MoveFile(ctxA, h, a.File.ID, "/Shared/Archive/a2.md"); code(err) != "extension_change" {
			t.Fatalf("extension change: %v", err)
		}
		if _, err := svc.FileByPath(ctxA, "/Shared/Docs/a.txt"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("old path still resolves: %v", err)
		}
	})

	t.Run("soft delete and restore file", func(t *testing.T) {
		w := must[drive.WriteResult](t)(svc.Write(ctxA, h, drive.WriteInput{Path: "/Shared/tmp/x.txt", Data: []byte("x")}))
		if err := svc.DeleteFile(ctxA, ag, w.File.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.FileByPath(ctxA, "/Shared/tmp/x.txt"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("deleted file visible: %v", err)
		}
		tr := must[drive.Trash](t)(svc.Trash(ctxA))
		found := false
		for _, f := range tr.Files {
			found = found || (f.ID == w.File.ID && f.DeletedBy != nil && *f.DeletedBy == ag)
		}
		if !found {
			t.Fatalf("trash: %+v", tr)
		}
		// content of a deleted file is still there (nothing erased)
		if rc, _, err := svc.Open(ctxA, w.File, 0); err != nil {
			t.Fatal(err)
		} else {
			rc.Close()
		}
		// a new file takes the name: restoring the old one conflicts instead of overwriting
		must[drive.WriteResult](t)(svc.Write(ctxA, h, drive.WriteInput{Path: "/Shared/tmp/x.txt", Data: []byte("new")}))
		if _, err := svc.RestoreFile(ctxA, h, w.File.ID); code(err) != "cannot_restore" {
			t.Fatalf("restore over a live name: %v", err)
		}
	})

	t.Run("folder delete restore and move rewrite the subtree", func(t *testing.T) {
		must[drive.WriteResult](t)(svc.Write(ctxA, h, drive.WriteInput{Path: "/Shared/Tree/sub/deep.txt", Data: []byte("deep")}))
		must[drive.WriteResult](t)(svc.Write(ctxA, h, drive.WriteInput{Path: "/Shared/Tree/top.txt", Data: []byte("top")}))
		tree := must[drive.Folder](t)(svc.FolderByPath(ctxA, "/Shared/Tree"))
		moved := must[drive.Folder](t)(svc.MoveFolder(ctxA, h, tree.ID, "/Shared/Moved"))
		if moved.Path != "/Shared/Moved" {
			t.Fatal(moved.Path)
		}
		if _, err := svc.FileByPath(ctxA, "/Shared/Moved/sub/deep.txt"); err != nil {
			t.Fatalf("subtree not rewritten: %v", err)
		}
		if _, err := svc.MoveFolder(ctxA, h, tree.ID, "/Shared/Moved/sub/inside"); code(err) != "path_invalid" {
			t.Fatalf("move into itself: %v", err)
		}
		if err := svc.DeleteFolder(ctxA, h, tree.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.FileByPath(ctxA, "/Shared/Moved/sub/deep.txt"); err == nil {
			t.Fatal("file of a deleted folder is still visible")
		}
		if _, err := svc.RestoreFolder(ctxA, h, tree.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.FileByPath(ctxA, "/Shared/Moved/sub/deep.txt"); err != nil {
			t.Fatalf("restore did not bring the files back: %v", err)
		}
	})

	t.Run("owned folders and desks", func(t *testing.T) {
		p := must[drive.Folder](t)(svc.EnsureOwnedFolder(ctxA, drive.RootProjects, "prj_1", "Plan Q1/2026"))
		again := must[drive.Folder](t)(svc.EnsureOwnedFolder(ctxA, drive.RootProjects, "prj_1", "renamed"))
		if p.ID != again.ID || p.RefID != "prj_1" || strings.Contains(p.Name, "/") {
			t.Fatalf("owned: %+v %+v", p, again)
		}
		other := must[drive.Folder](t)(svc.EnsureOwnedFolder(ctxA, drive.RootProjects, "prj_2", "Plan Q1/2026"))
		if other.Name == p.Name {
			t.Fatalf("two projects with the same name share a folder: %q", other.Name)
		}
		d := must[drive.Folder](t)(svc.EnsureOwnedFolder(ctxA, drive.RootAgents, "sales", "Sales"))
		if d.Path != "/Agents/sales" || d.RefID != "sales" {
			t.Fatalf("desk: %+v", d)
		}
		if _, err := svc.MoveFolder(ctxA, h, p.ID, "/Projects/other"); code(err) != "protected_folder" {
			t.Fatalf("project folder must be protected: %v", err)
		}
	})

	t.Run("search", func(t *testing.T) {
		must[drive.WriteResult](t)(svc.Write(ctxA, h, drive.WriteInput{Path: "/Shared/contrato-acme.md", Data: []byte("Cláusula de penalidades por retraso en la entrega.")}))
		hits := must[[]drive.Hit](t)(svc.Retrieve(ctxA, drive.RetrieveQuery{Query: "penalidades retraso"}))
		if len(hits) == 0 || hits[0].Path != "/Shared/contrato-acme.md" || hits[0].Snippet == "" {
			t.Fatalf("hits: %+v", hits)
		}
		if o.Stemming {
			hits = must[[]drive.Hit](t)(svc.Retrieve(ctxA, drive.RetrieveQuery{Query: "contratos"}))
			if len(hits) == 0 {
				t.Fatal("spanish stemming: contratos should find contrato")
			}
		}
		hits = must[[]drive.Hit](t)(svc.Retrieve(ctxA, drive.RetrieveQuery{Query: "penalidades", Under: []string{"/Projects"}}))
		if len(hits) != 0 {
			t.Fatalf("scope must restrict the search: %+v", hits)
		}
		hits = must[[]drive.Hit](t)(svc.Retrieve(ctxA, drive.RetrieveQuery{Query: "penalidades", Under: []string{}}))
		_ = hits // an empty scope is the caller's "nothing readable": no hits are guaranteed by AgentFS, not here
		must[drive.WriteResult](t)(svc.Write(ctxA, h, drive.WriteInput{Path: "/Shared/secret-plan.md", Data: []byte("zebra-unico-token")}))
		if hits = must[[]drive.Hit](t)(svc.Retrieve(ctxB, drive.RetrieveQuery{Query: "zebra-unico-token"})); len(hits) != 0 {
			t.Fatalf("search leaked across organizations: %+v", hits)
		}
	})

	t.Run("organizations are isolated", func(t *testing.T) {
		w := must[drive.WriteResult](t)(svc.Write(ctxA, h, drive.WriteInput{Path: "/Shared/iso.txt", Data: []byte("iso")}))
		if _, err := svc.GetFile(ctxB, w.File.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("org B read a file of org A: %v", err)
		}
		if _, err := svc.FileByPath(ctxB, "/Shared/iso.txt"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("org B resolved a path of org A: %v", err)
		}
		if err := svc.DeleteFile(ctxB, h, w.File.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("org B deleted a file of org A: %v", err)
		}
		if _, err := svc.NewVersion(ctxB, h, w.File.ID, []byte("x"), "", nil, "t"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("org B wrote a version: %v", err)
		}
		rc, _, err := svc.Open(ctxB, w.File, 0)
		if err == nil {
			rc.Close()
			t.Fatal("org B opened the content of org A")
		}
		la := must[drive.Listing](t)(svc.List(ctxB, "/Shared"))
		if len(la.Files) != 0 {
			t.Fatalf("org B lists files of org A: %+v", la.Files)
		}
		ua := must[drive.Usage](t)(svc.Usage(ctxB))
		if ua.UsedBytes != 0 {
			t.Fatalf("usage leaked: %+v", ua)
		}
	})

	t.Run("grants", func(t *testing.T) {
		g := must[drive.Grant](t)(svc.AddGrant(ctxA, h, "sales", "/Clients/Acme"))
		gs := must[[]drive.Grant](t)(svc.Grants(ctxA, "sales"))
		if len(gs) != 1 || gs[0].ID != g.ID || gs[0].Mode != "read" {
			t.Fatalf("grants: %+v", gs)
		}
		if other := must[[]drive.Grant](t)(svc.Grants(ctxB, "sales")); len(other) != 0 {
			t.Fatalf("grants leaked: %+v", other)
		}
		if _, err := svc.AddGrant(ctxA, ag, "sales", "/Clients/Acme"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("an agent granted access: %v", err)
		}
		if err := svc.RevokeGrant(ctxA, h, g.ID); err != nil {
			t.Fatal(err)
		}
		if gs = must[[]drive.Grant](t)(svc.Grants(ctxA, "sales")); len(gs) != 0 {
			t.Fatalf("revoked grant still active: %+v", gs)
		}
	})
}
