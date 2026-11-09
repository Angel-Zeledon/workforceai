package drive_test

import (
	"context"
	"errors"
	"testing"

	"aiworkforce/backend/internal/drive"
)

type fakePeople struct {
	level string
	err   error
	asked []string
}

func (f *fakePeople) OfficeAccess(_ context.Context, _, _, officeID string) (string, error) {
	f.asked = append(f.asked, officeID)
	return f.level, f.err
}

func peopleSvc(t *testing.T, fp *fakePeople) *drive.Service {
	t.Helper()
	svc, _, _ := newSvc(drive.Limits{})
	if fp != nil {
		svc.SetPeople(fp)
	}
	if _, err := svc.EnsureOfficeFolder(context.Background(), "dep_legal", "Legal", []string{"Casos"}); err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestPersonAccessOutsideOfficesIsEdit(t *testing.T) {
	fp := &fakePeople{level: drive.AccessNone}
	svc := peopleSvc(t, fp)
	if got := svc.PersonAccess(context.Background(), "u1", "/Shared/a.md"); got != drive.AccessEdit {
		t.Fatalf("got %s", got)
	}
	if len(fp.asked) != 0 {
		t.Fatalf("asked offices: %v", fp.asked)
	}
}

func TestPersonAccessOfficeRoot(t *testing.T) {
	svc := peopleSvc(t, &fakePeople{level: drive.AccessNone})
	if got := svc.PersonAccess(context.Background(), "u1", "/Offices"); got != drive.AccessRead {
		t.Fatalf("got %s", got)
	}
}

func TestPersonAccessDelegatesByRefID(t *testing.T) {
	fp := &fakePeople{level: drive.AccessRead}
	svc := peopleSvc(t, fp)
	if got := svc.PersonAccess(context.Background(), "u1", "/Offices/Legal/Casos/x.md"); got != drive.AccessRead {
		t.Fatalf("got %s", got)
	}
	if len(fp.asked) != 1 || fp.asked[0] != "dep_legal" {
		t.Fatalf("asked %v", fp.asked)
	}
}

func TestPersonAccessFailsClosed(t *testing.T) {
	ctx := context.Background()
	svc := peopleSvc(t, &fakePeople{err: errors.New("boom")})
	if got := svc.PersonAccess(ctx, "u1", "/Offices/Legal/x.md"); got != drive.AccessNone {
		t.Fatalf("error: got %s", got)
	}
	if got := svc.PersonAccess(ctx, "u1", "/Offices/Nope/x.md"); got != drive.AccessNone {
		t.Fatalf("unknown folder: got %s", got)
	}
	svc = peopleSvc(t, &fakePeople{level: "weird"})
	if got := svc.PersonAccess(ctx, "u1", "/Offices/Legal"); got != drive.AccessNone {
		t.Fatalf("bad level: got %s", got)
	}
}

func TestPersonAccessWithoutAdapterIsEdit(t *testing.T) {
	svc := peopleSvc(t, nil)
	if got := svc.PersonAccess(context.Background(), "u1", "/Offices/Legal/x.md"); got != drive.AccessEdit {
		t.Fatalf("got %s", got)
	}
}

func TestVisibleToResolvesEachOfficeOnce(t *testing.T) {
	fp := &fakePeople{level: drive.AccessNone}
	svc := peopleSvc(t, fp)
	got := svc.VisibleTo(context.Background(), "u1", []string{"/Shared/a.md", "/Offices", "/Offices/Legal/a", "/Offices/Legal/Casos/b"})
	want := []bool{true, true, false, false}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if len(fp.asked) != 1 {
		t.Fatalf("asked %v", fp.asked)
	}
}

func TestPersonAccessIgnoresCaseOfOfficesRoot(t *testing.T) {
	svc := peopleSvc(t, &fakePeople{level: drive.AccessNone})
	for _, p := range []string{"/offices/Legal/x.md", "/OFFICES/legal/x.md"} {
		if got := svc.PersonAccess(context.Background(), "u1", p); got != drive.AccessNone {
			t.Fatalf("%s: got %s", p, got)
		}
	}
}

func TestVisibleToMemoIgnoresCase(t *testing.T) {
	fp := &fakePeople{level: drive.AccessNone}
	svc := peopleSvc(t, fp)
	got := svc.VisibleTo(context.Background(), "u1", []string{"/Offices/Legal/a", "/offices/legal/b"})
	if got[0] || got[1] || len(fp.asked) != 1 {
		t.Fatalf("got %v asked %v", got, fp.asked)
	}
}
