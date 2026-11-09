package drive_test

import (
	"context"
	"errors"
	"testing"

	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
)

func TestEnsureOfficeFolderCreatesTemplateOnce(t *testing.T) {
	svc, _, _ := newSvc(drive.Limits{})
	ctx := context.Background()
	subs := []string{"Clientes", "Impuestos"}
	first, err := svc.EnsureOfficeFolder(ctx, "dep_1", "Contabilidad", subs)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.EnsureOfficeFolder(ctx, "dep_1", "Contabilidad", subs)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("not idempotent: %s vs %s", first.ID, second.ID)
	}
	l, err := svc.List(ctx, "/Offices/Contabilidad")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Folders) != 2 || l.Folders[0].Name != "Clientes" || l.Folders[1].Name != "Impuestos" {
		t.Fatalf("subfolders = %+v", l.Folders)
	}
	got, err := svc.OfficeFolder(ctx, "dep_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "/Offices/Contabilidad" || got.RefID != "dep_1" || got.RootKind != "offices" {
		t.Fatalf("office folder = %+v", got)
	}
}

func TestEnsureOfficeFolderNameCollisionGetsSuffix(t *testing.T) {
	svc, _, _ := newSvc(drive.Limits{})
	ctx := context.Background()
	a, _ := svc.EnsureOfficeFolder(ctx, "dep_a", "Ventas", nil)
	b, err := svc.EnsureOfficeFolder(ctx, "dep_b", "Ventas", nil)
	if err != nil || a.Path == b.Path {
		t.Fatalf("a=%s b=%s err=%v", a.Path, b.Path, err)
	}
}

func TestOfficeFolderUnknownIsNotFound(t *testing.T) {
	svc, _, _ := newSvc(drive.Limits{})
	if _, err := svc.OfficeFolder(context.Background(), "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
