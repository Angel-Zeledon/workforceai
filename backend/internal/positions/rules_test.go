package positions

import (
	"slices"
	"testing"
)

var bounds = Bounds{Office: "/Offices/Contabilidad", Shared: "/Shared", SharedIn: []string{"/Offices/Legal/Contratos"}}

func lib(xs ...string) []Folder {
	var out []Folder
	for i := 0; i+1 < len(xs); i += 2 {
		out = append(out, Folder{Path: xs[i], Level: xs[i+1]})
	}
	return out
}

func TestLevelAtDeepestWins(t *testing.T) {
	l := lib("/Offices/Contabilidad", LevelEdit, "/Offices/Contabilidad/Nominas", LevelNone, "/Offices/Contabilidad/Nominas/Resumen", LevelRead)
	cases := map[string]string{
		"/Offices/Contabilidad/Bancos/a.xlsx":        LevelEdit,
		"/offices/contabilidad/nominas/enero.xlsx":   LevelNone, // the drive resolves paths without regard to case
		"/Offices/Contabilidad/Nominas/Resumen/x.md": LevelRead,
		"/Offices/Contabilidad":                      LevelEdit,
		"/Offices/ContabilidadX/a":                   LevelNone, // a sibling with the same prefix is not inside
		"/Shared/a":                                  LevelNone,
	}
	for p, want := range cases {
		if got := LevelAt(l, p); got != want {
			t.Errorf("LevelAt(%s) = %s, want %s", p, got, want)
		}
	}
}

func TestEmptyLibraryMeansTheDefaults(t *testing.T) {
	e := Effective(nil, bounds)
	if LevelAt(e, "/Offices/Contabilidad/x") != LevelEdit || LevelAt(e, "/Shared/x") != LevelRead || LevelAt(e, "/Offices/Legal/Contratos/c.pdf") != LevelRead {
		t.Fatalf("defaults: %v", e)
	}
	if LevelAt(e, "/Offices/Legal/Otros") != LevelNone {
		t.Fatal("what is not shared stays out")
	}
}

func TestExceeds(t *testing.T) {
	manager := Package{Skills: []string{"a", "b"}, Library: lib("/Offices/Contabilidad", LevelEdit, "/Offices/Contabilidad/Nominas", LevelNone, "/Shared", LevelRead)}
	ok := Package{Skills: []string{"a"}, Library: lib("/Offices/Contabilidad/Bancos", LevelRead)}
	if ex := Exceeds(manager, ok, bounds); !ex.Empty() {
		t.Fatalf("a subset exceeds: %+v", ex)
	}
	bad := Package{Skills: []string{"a", "c"}, Library: lib("/Offices/Contabilidad", LevelEdit, "/Shared", LevelEdit, "/Offices/Legal/Contratos", LevelRead)}
	ex := Exceeds(manager, bad, bounds)
	if !slices.Equal(ex.Skills, []string{"c"}) {
		t.Fatalf("skills: %v", ex.Skills)
	}
	// the payroll folder the manager excluded, /Shared at Edit and the shared contracts are all beyond the manager
	want := map[string]string{"/Offices/Contabilidad/Nominas": LevelEdit, "/Shared": LevelEdit, "/Offices/Legal/Contratos": LevelRead}
	if len(ex.Folders) != len(want) {
		t.Fatalf("folders: %v", ex.Folders)
	}
	for _, f := range ex.Folders {
		if want[f.Path] != f.Level {
			t.Fatalf("folders: %v", ex.Folders)
		}
	}
	// a subordinate without a library has the defaults: beyond a manager with a narrower library
	if ex := Exceeds(manager, Package{}, bounds); ex.Empty() {
		t.Fatal("no library under a narrower manager must exceed")
	}
	// under a manager without a library (the defaults) any library inside the department fits
	if ex := Exceeds(Package{Skills: []string{"a"}}, ok, bounds); !ex.Empty() {
		t.Fatalf("under the defaults: %+v", ex)
	}
}

func TestClampCutsToTheSuperior(t *testing.T) {
	manager := Package{Skills: []string{"a"}, Library: lib("/Offices/Contabilidad", LevelEdit, "/Offices/Contabilidad/Nominas", LevelNone, "/Shared/Plantillas", LevelRead)}
	sub := Package{Skills: []string{"a", "b"}, Library: lib("/Offices/Contabilidad", LevelEdit, "/Shared", LevelEdit)}
	got, skillsLost, folders := Clamp(manager, sub, bounds)
	if !Exceeds(manager, got, bounds).Empty() {
		t.Fatalf("clamped still exceeds: %+v", got)
	}
	if !slices.Equal(got.Skills, []string{"a"}) || !slices.Equal(skillsLost, []string{"b"}) {
		t.Fatalf("skills: %v lost %v", got.Skills, skillsLost)
	}
	// what it keeps: the office (edit), not payroll, /Shared/Plantillas at read only
	if LevelAt(got.Library, "/Offices/Contabilidad/x") != LevelEdit || LevelAt(got.Library, "/Offices/Contabilidad/Nominas/x") != LevelNone ||
		LevelAt(got.Library, "/Shared/Plantillas/x") != LevelRead || LevelAt(got.Library, "/Shared/Otra/x") != LevelNone {
		t.Fatalf("library: %v", got.Library)
	}
	if len(folders) == 0 {
		t.Fatal("the losses are reported")
	}
	// cutting a subordinate without a library makes its library explicit (never "no library" = more)
	got, _, _ = Clamp(Package{Library: lib("/Shared", LevelNone)}, Package{}, bounds)
	if len(got.Library) == 0 || LevelAt(got.Library, "/Shared/x") != LevelNone || LevelAt(got.Library, "/Offices/Contabilidad/x") != LevelNone {
		t.Fatalf("nothing left must stay explicit: %v", got.Library)
	}
}

func TestWithinBounds(t *testing.T) {
	cases := map[string]string{
		"/Offices/Contabilidad/Bancos":   kindOffice,
		"/Shared":                        kindShared,
		"/Shared/Plantillas":             kindShared,
		"/Offices/Legal/Contratos/2026":  kindSharedIn,
		"/Offices/Legal":                 "",
		"/Offices":                       "",
		"/Agents/valeria":                "",
		"/Offices/ContabilidadSecreta/x": "",
	}
	for p, want := range cases {
		if got := bounds.Within(p); got != want {
			t.Errorf("Within(%s) = %q, want %q", p, got, want)
		}
	}
}
