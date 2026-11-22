package api_test

import (
	"net/url"
	"strings"
	"testing"

	"aiworkforce/backend/internal/auth"
)

// GET /drive/list says what the person may do in the folder (the Files screen enables "Nueva carpeta", Subir, Renombrar
// and Mover from it): the person's level in the drive combined with the role's files:write. The rules do not change.
func TestDriveListAccess(t *testing.T) {
	o := newOfficeDrive(t)
	vera := o.e.member(o.owner, "vera-la@example.com", auth.RoleViewer)
	list := func(token, p string) string {
		t.Helper()
		l := o.e.json("GET", api1+"/drive/list?path="+url.QueryEscape(p), token, nil, 200)
		s, _ := l["access"].(string)
		return s
	}
	for _, c := range []struct {
		who, token, path, want string
	}{
		{"member in own office", o.ana, "/Offices/Legal", "edit"},
		{"member in own open office", o.ana, "/Offices/Ventas", "edit"},
		{"non-member in open office", o.beto, "/Offices/Ventas", "read"},
		{"anyone at /Offices", o.ana, "/Offices", "read"},
		{"member+ in Shared", o.beto, "/Shared", "edit"},
		{"viewer in Shared", vera, "/Shared", "read"},
		{"viewer at the root", vera, "/", "read"},
	} {
		if got := list(c.token, c.path); got != c.want {
			t.Errorf("%s: access(%s) = %q, want %q", c.who, c.path, got, c.want)
		}
	}
	// a sealed department stays a 404 for a non-member (nothing about its access leaks)
	if c := o.status("GET", api1+"/drive/list?path="+url.QueryEscape("/Offices/Legal"), o.beto, nil); c != 404 {
		t.Fatalf("beto list Legal = %d", c)
	}
}

// Renombrar a folder is a move to the same parent with another name: a member renames inside the department, a
// non-member of an open department is refused, a sealed department answers 404, and the department's own folder (and
// the roots) are protected.
func TestFolderRenameRespectsAccess(t *testing.T) {
	o := newOfficeDrive(t)
	sub := o.e.json("POST", api1+"/drive/folders", o.ana, map[string]any{"path": "/Offices/Ventas/Precios"}, 201)
	move := api1 + "/drive/folders/" + sub["id"].(string) + "/move"
	if c := o.status("POST", move, o.beto, map[string]any{"path": "/Offices/Ventas/Tarifas"}); c != 403 {
		t.Fatalf("beto renames in open Ventas = %d, want 403", c)
	}
	if f := o.e.json("POST", move, o.ana, map[string]any{"path": "/Offices/Ventas/Tarifas"}, 200); f["path"] != "/Offices/Ventas/Tarifas" {
		t.Fatalf("ana rename = %v", f)
	}
	sealed := o.e.json("POST", api1+"/drive/folders", o.ana, map[string]any{"path": "/Offices/Legal/Contratos/Vigentes"}, 201)
	if c := o.status("POST", api1+"/drive/folders/"+sealed["id"].(string)+"/move", o.beto, map[string]any{"path": "/Offices/Legal/Contratos/Viejos"}); c != 404 {
		t.Fatalf("beto renames in sealed Legal = %d, want 404", c)
	}
	if c := o.status("POST", api1+"/drive/folders", o.beto, map[string]any{"path": "/Offices/Legal/Nueva"}); c != 404 {
		t.Fatalf("beto mkdir in sealed Legal = %d, want 404", c)
	}
	// the department's own folder cannot be renamed, not even by its lead
	l := o.e.json("GET", api1+"/drive/list?path=/Offices", o.ana, nil, 200)
	for _, f := range l["folders"].([]any) {
		m := f.(map[string]any)
		if m["name"] != "Legal" {
			continue
		}
		// refused either way: the new name names no department (404) or the folder is protected (400)
		if r, b := o.e.do("POST", api1+"/drive/folders/"+m["id"].(string)+"/move", o.ana, map[string]any{"path": "/Offices/Juridico"}, nil); r.StatusCode < 400 {
			t.Fatalf("rename the department's folder = %d %s", r.StatusCode, b)
		}
	}
	if s := mustJSON(t, o.e.json("GET", api1+"/drive/list?path=/Offices", o.ana, nil, 200)); !strings.Contains(s, `"name":"Legal"`) {
		t.Fatalf("the department's folder changed: %s", s)
	}
	// "Nueva carpeta" creates the missing parents too
	if c := o.status("POST", api1+"/drive/folders", o.ana, map[string]any{"path": "/Shared/Uno/Dos"}); c != 201 {
		t.Fatalf("mkdir with parents = %d", c)
	}
}
