package api_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/drive"
)

// shareList decodes a bare Share[] answer.
func (o *officeDrive) shareList(token, query string) []drive.Share {
	o.e.t.Helper()
	r, b := o.e.do("GET", api1+"/drive/shares?"+query, token, nil, nil)
	if r.StatusCode != 200 {
		o.e.t.Fatalf("GET shares?%s = %d %s", query, r.StatusCode, b)
	}
	var out []drive.Share
	if err := json.Unmarshal(b, &out); err != nil {
		o.e.t.Fatal(err)
	}
	return out
}

func TestDriveSharesBetweenOffices(t *testing.T) {
	o := newOfficeDrive(t)
	e := o.e
	compras := o.mk("Compras", "private")
	carla := e.member(o.owner, "carla-sh@example.com", auth.RoleMember)
	dani := e.member(o.owner, "dani-sh@example.com", auth.RoleMember)
	e.json("PUT", api1+"/offices/"+o.legal+"/members/"+e.userID(o.beto), o.owner.AccessToken, map[string]any{"role": "member"}, 200)
	e.json("PUT", api1+"/offices/"+compras+"/members/"+e.userID(carla), o.owner.AccessToken, map[string]any{"role": "member"}, 200)

	dem := o.upload(o.ana, "/Offices/Legal/Casos/demanda.md", "demanda confidencial del caso")
	o.upload(o.ana, "/Offices/Legal/privado.md", "confidencial solo para legal")
	casos := e.json("GET", api1+"/drive/list?path="+url.QueryEscape("/Offices/Legal"), o.ana, nil, 200)["folders"].([]any)[0].(map[string]any)["id"].(string)
	post := func(token, path, target string, want int) map[string]any {
		return e.json("POST", api1+"/drive/shares", token, map[string]any{"path": path, "target_office_id": target}, want)
	}

	// errors of the request
	if post(o.beto, "/Offices/Legal", compras, 400)["code"] != "not_in_office" ||
		post(o.beto, "/Shared/x.md", compras, 400)["code"] != "not_in_office" ||
		post(o.beto, "/Offices/Legal/Casos", "general", 400)["code"] != "general_office" ||
		post(o.beto, "/Offices/Legal/Casos", o.legal, 400)["code"] != "same_office" {
		t.Fatal("400 codes")
	}
	post(o.beto, "/Offices/Legal/Casos", "dep_nope", 404)
	post(carla, "/Offices/Legal/Casos", compras, 404)                       // not a member of the owner (private)
	if post(o.beto, "/Offices/Ventas/x.md", compras, 403)["code"] != "read_only_folder" { // open office: beto only reads
		t.Fatal("read_only_folder")
	}

	sh := post(o.beto, "/Offices/Legal/Casos", compras, 201)
	if sh["status"] != "pending" || sh["kind"] != "folder" || sh["file_id"] != casos || sh["can_decide"] != false || sh["can_revoke"] != true {
		t.Fatalf("share: %v", sh)
	}
	id := sh["id"].(string)
	if post(o.ana, "/Offices/Legal/Casos", compras, 409)["code"] != "already_shared" {
		t.Fatal("duplicate")
	}

	// before the approval the target sees nothing
	if c := o.status("GET", api1+"/drive/files/"+dem, carla, nil); c != 404 {
		t.Fatalf("pending: %d", c)
	}
	if in := o.shareList(carla, "office="+compras+"&dir=in"); len(in) != 0 {
		t.Fatalf("in: %+v", in)
	}
	if c := o.status("GET", api1+"/drive/shares?office="+o.legal+"&dir=out", carla, nil); c != 404 {
		t.Fatalf("a non-member lists out: %d", c)
	}
	if out := o.shareList(o.ana, "office="+o.legal+"&dir=out&status=pending"); len(out) != 1 || !out[0].CanDecide {
		t.Fatalf("out: %+v", out)
	}
	dec := func(token, d string, want int) map[string]any {
		return e.json("POST", api1+"/drive/shares/"+id+"/decision", token, map[string]any{"decision": d}, want)
	}
	if dec(o.beto, "approve", 403)["code"] != "not_decider" {
		t.Fatal("not_decider")
	}
	dec(carla, "approve", 404)
	if a := dec(o.ana, "approve", 200); a["status"] != "active" || a["decided_by"] == nil {
		t.Fatalf("approve: %v", a)
	}
	if dec(o.ana, "reject", 409)["code"] != "not_pending" {
		t.Fatal("not_pending")
	}

	// the target opens what was shared by id and by path, without access to the parent folder
	if in := o.shareList(carla, "office="+compras+"&dir=in"); len(in) != 1 || in[0].FileID != casos || in[0].CanRevoke {
		t.Fatalf("in: %+v", in)
	}
	e.json("GET", api1+"/drive/files/"+dem, carla, nil, 200)
	for _, sub := range []string{"/versions", "/preview"} {
		if c := o.status("GET", api1+"/drive/files/"+dem+sub, carla, nil); c != 200 && c != 415 {
			t.Fatalf("GET file%s = %d", sub, c)
		}
	}
	if r, b := e.do("GET", api1+"/drive/files/"+dem+"/content", carla, nil, nil); r.StatusCode != 200 || !strings.Contains(string(b), "demanda") {
		t.Fatalf("content = %d %s", r.StatusCode, b)
	}
	if l := e.json("GET", api1+"/drive/list?path="+url.QueryEscape("/Offices/Legal/Casos"), carla, nil, 200); len(l["files"].([]any)) != 1 {
		t.Fatalf("list shared folder: %v", l)
	}
	// but nothing more: parent, siblings, search, and never a write
	for _, p := range []string{"/Offices/Legal", "/offices/legal/privado.md"} {
		if c := o.status("GET", api1+"/drive/list?path="+url.QueryEscape(p), carla, nil); c != 404 {
			t.Fatalf("list %s = %d", p, c)
		}
	}
	if s := e.json("GET", api1+"/drive/search?q=confidencial", carla, nil, 200); len(s["hits"].([]any)) != 1 {
		t.Fatalf("search: %v", s)
	}
	if c := o.status("PUT", api1+"/drive/files/"+dem+"/content", carla, []byte("x")); c != 403 {
		t.Fatalf("write = %d", c)
	}
	if c := o.status("PUT", drivePut("/Offices/Legal/Casos/nuevo.md"), carla, []byte("x")); c != 403 {
		t.Fatalf("upload = %d", c)
	}
	if c := o.status("GET", api1+"/drive/files/"+dem, dani, nil); c != 404 {
		t.Fatalf("a third person: %d", c)
	}

	// who can see it
	at := o.shareList(carla, "path="+url.QueryEscape("/Offices/Legal/Casos/demanda.md"))
	if len(at) != 1 || at[0].ID != id || at[0].CanRevoke {
		t.Fatalf("at: %+v", at)
	}
	if c := o.status("GET", api1+"/drive/shares?path="+url.QueryEscape("/Offices/Legal/privado.md"), carla, nil); c != 404 {
		t.Fatalf("at, no access: %d", c)
	}

	// revoke: the target cannot; who asked can, and the access ends at once
	if c := o.status("DELETE", api1+"/drive/shares/"+id, carla, nil); c != 404 {
		t.Fatalf("target revokes: %d", c)
	}
	if c := o.status("DELETE", api1+"/drive/shares/"+id, o.beto, nil); c != 204 {
		t.Fatalf("revoke: %d", c)
	}
	if c := o.status("GET", api1+"/drive/files/"+dem, carla, nil); c != 404 {
		t.Fatalf("after revoke: %d", c)
	}
	if c := o.status("DELETE", api1+"/drive/shares/"+id, o.ana, nil); c != 409 {
		t.Fatalf("revoke twice: %d", c)
	}

	// a responsible shares a file at once
	f := post(o.ana, "/Offices/Legal/Casos/demanda.md", compras, 201)
	if f["status"] != "active" || f["kind"] != "file" || f["file_id"] != dem {
		t.Fatalf("file share: %v", f)
	}
	e.json("GET", api1+"/drive/files/"+dem, carla, nil, 200)
}
