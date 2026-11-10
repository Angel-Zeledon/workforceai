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

// Postgres resolves paths with lower() under the database collation, which folds the dotted capital İ to "i": a path
// spelled /OFFİCES/... reaches the office folder although EqualFold("OFFİCES", "Offices") is false. The memstore
// lowers with strings.ToLower, which folds İ the same way, so this test reproduces it without Postgres (its limit:
// other collation-only foldings are not covered here; the store lookup in isOfficesRoot covers them all the same).
func TestPersonAccessUnicodeSpellingOfOfficesRoot(t *testing.T) {
	ctx := context.Background()
	fp := &fakePeople{level: drive.AccessNone}
	svc := peopleSvc(t, fp)
	const p = "/OFFİCES/Legal/Casos/x.md"
	if f, err := svc.FolderByPath(ctx, "/OFFİCES/Legal"); err != nil || f.Path != "/Offices/Legal" {
		t.Fatalf("the store resolves the spelling to the office folder (precondition): %+v %v", f, err)
	}
	if got := svc.PersonAccess(ctx, "u1", p); got != drive.AccessNone {
		t.Fatalf("%s: got %s", p, got)
	}
	if got := svc.VisibleTo(ctx, "u1", []string{p, "/OFFİCES"}); got[0] || !got[1] {
		t.Fatalf("visible %v", got)
	}
	// a name that resolves to nothing is not an office: edit as before, and no office is asked
	fp.asked = nil
	if got := svc.PersonAccess(ctx, "u1", "/NİNGUNA/x.md"); got != drive.AccessEdit || len(fp.asked) != 0 {
		t.Fatalf("unknown root: %s asked %v", got, fp.asked)
	}
}

type peopleWithMembers struct {
	fakePeople
	members map[string][]string
	err     error
}

func (p *peopleWithMembers) OfficeMembers(_ context.Context, _, officeID string) ([]string, error) {
	return p.members[officeID], p.err
}

func TestSealedAudience(t *testing.T) {
	ctx := context.Background()
	pm := &peopleWithMembers{members: map[string][]string{"dep_legal": {"u-ana", "u-beto"}}}
	svc := peopleSvc(t, nil)
	svc.SetPeople(pm)
	for _, p := range []string{"/Shared/a.md", "/Offices", "/Agents/x/a.md"} {
		if m, sealed := svc.SealedAudience(ctx, p); sealed || m != nil {
			t.Fatalf("%s: %v %v", p, m, sealed)
		}
	}
	for _, p := range []string{"/Offices/Legal/Casos/x.md", "/offices/legal/x.md", "/OFFİCES/Legal/x.md"} {
		if m, sealed := svc.SealedAudience(ctx, p); !sealed || len(m) != 2 || m[0] != "u-ana" {
			t.Fatalf("%s: %v %v", p, m, sealed)
		}
	}
	// members that cannot be told: sealed, nobody named (the caller says nothing of the path)
	for _, p := range []string{"/Offices/Nope/x.md"} {
		if m, sealed := svc.SealedAudience(ctx, p); !sealed || m != nil {
			t.Fatalf("%s: %v %v", p, m, sealed)
		}
	}
	pm.err = errors.New("boom")
	if m, sealed := svc.SealedAudience(ctx, "/Offices/Legal/x.md"); !sealed || m != nil {
		t.Fatalf("error: %v %v", m, sealed)
	}
	svc.SetPeople(&fakePeople{level: drive.AccessEdit}) // an adapter without members
	if m, sealed := svc.SealedAudience(ctx, "/Offices/Legal/x.md"); !sealed || m != nil {
		t.Fatalf("no members adapter: %v %v", m, sealed)
	}
	svc.SetPeople(nil)
	if m, sealed := svc.SealedAudience(ctx, "/Offices/Legal/x.md"); sealed || m != nil {
		t.Fatalf("no people adapter: nothing sealed: %v %v", m, sealed)
	}
}
