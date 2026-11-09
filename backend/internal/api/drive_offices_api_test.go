package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/url"
	"strings"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/calendar"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/meetings"
	"aiworkforce/backend/internal/offices"
)

// officeDriveAccess is the same rule as the server's adapter (cmd/server/drive_people.go, package main, not
// importable here): a member edits; a non-member reads an open office and has no access to a private one, admins
// included.
type officeDriveAccess struct{ svc *offices.Service }

func (o officeDriveAccess) OfficeAccess(ctx context.Context, org, userID, officeID string) (string, error) {
	m, err := o.svc.Model(ctx, org)
	if err != nil {
		return drive.AccessNone, err
	}
	if m.IsMember(userID, officeID) {
		return drive.AccessEdit, nil
	}
	if off, ok := m.Office(officeID); ok && off.Visibility == offices.Open {
		return drive.AccessRead, nil
	}
	return drive.AccessNone, nil
}

// officeDrive: a private office "Legal" and an open office "Ventas", both led by ana; beto belongs to none. Both are
// admins (admins get no pass into a private office's folder).
type officeDrive struct {
	e             *env
	owner         *auth.Session
	ana, beto     string
	legal, ventas string // office ids
}

func newOfficeDrive(t *testing.T) *officeDrive {
	t.Helper()
	var dr *drive.Service
	e := newEnv(t, opts{auth: true, orgchart: true, drive: true, ws: true, deps: func(d *api.Deps) {
		withOffices(d)
		d.Drive.SetPeople(officeDriveAccess{svc: d.Offices})
		dr = d.Drive
		// the other HTTP paths that read drive files: commands, meeting minutes, chat attachments
		d.Commands = &api.CommandsDeps{}
		d.Meetings = meetings.New(meetings.Config{Store: meetings.NewMemStore(), Calendars: meetCals{fake: calendar.NewFake(calendar.ProviderGoogle)},
			Summarizer: meetSummary{orch: d.Orch}, Tasks: meetTasks{orch: d.Orch}, Files: meetFiles{d: d.Drive}, Agents: meetAgents{st: d.Store},
			JitsiBase: "https://jitsi.example.org"})
		d.Orch.SetFileResolver(chatDriveFiles{d: d.Drive})
	}})
	owner := e.register("owner-do@example.com", "OwnerDO")
	o := &officeDrive{e: e, owner: owner,
		ana:  e.member(owner, "ana-do@example.com", auth.RoleAdmin),
		beto: e.member(owner, "beto-do@example.com", auth.RoleAdmin)}
	anaID := e.userID(o.ana)
	office := func(name, vis string) string {
		dep := e.json("POST", api1+"/org/departments", owner.AccessToken, map[string]any{"name": name, "head": map[string]any{"kind": "person", "id": anaID}}, 201)
		id := dep["id"].(string)
		e.json("POST", api1+"/offices", owner.AccessToken, map[string]any{"department_id": id, "visibility": vis}, 201)
		if _, err := dr.EnsureOfficeFolder(application.WithOrg(context.Background(), owner.OrgID), id, name, nil); err != nil {
			t.Fatal(err)
		}
		return id
	}
	o.legal, o.ventas = office("Legal", "private"), office("Ventas", "open")
	return o
}

// chatDriveFiles resolves chat attachments like the server's resolver (cmd/server/drive.go).
type chatDriveFiles struct{ d *drive.Service }

func (c chatDriveFiles) ResolveFile(ctx context.Context, org, p string) (domain.FileRef, bool) {
	f, err := c.d.FileByPath(application.WithOrg(ctx, org), p)
	if err != nil || f.DeletedAt != nil {
		return domain.FileRef{}, false
	}
	return domain.FileRef{FileID: f.ID, Path: f.Path, Name: f.Name, Mime: f.Mime, Size: f.Size, Version: f.CurrentVersion}, true
}

func drivePut(p string) string { return api1 + "/drive/upload?path=" + url.QueryEscape(p) }

var mdHdr = map[string]string{"Content-Type": "text/markdown"}

// upload stores a file and returns its id.
func (o *officeDrive) upload(token, p, body string) string {
	o.e.t.Helper()
	r, b := o.e.do("PUT", drivePut(p), token, []byte(body), mdHdr)
	if r.StatusCode != 201 {
		o.e.t.Fatalf("upload %s = %d %s", p, r.StatusCode, b)
	}
	var w struct {
		File drive.File `json:"file"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		o.e.t.Fatal(err)
	}
	return w.File.ID
}

func (o *officeDrive) status(method, path, token string, body any) int {
	o.e.t.Helper()
	var hdr map[string]string
	if _, raw := body.([]byte); raw {
		hdr = mdHdr
	}
	r, _ := o.e.do(method, path, token, body, hdr)
	return r.StatusCode
}

func TestOfficePrivateHiddenFromNonMember(t *testing.T) {
	o := newOfficeDrive(t)
	id := o.upload(o.ana, "/Offices/Legal/contrato.md", "clausula confidencialisima del contrato")

	names := func(token string) string {
		l := o.e.json("GET", api1+"/drive/list?path="+url.QueryEscape("/Offices"), token, nil, 200)
		s := []string{}
		for _, f := range l["folders"].([]any) {
			s = append(s, f.(map[string]any)["name"].(string))
		}
		return strings.Join(s, ",")
	}
	if got := names(o.beto); strings.Contains(got, "Legal") || !strings.Contains(got, "Ventas") {
		t.Fatalf("beto sees /Offices = %s", got)
	}
	if got := names(o.ana); !strings.Contains(got, "Legal") || !strings.Contains(got, "Ventas") {
		t.Fatalf("ana sees /Offices = %s", got)
	}
	// the root still shows Offices
	if root := mustJSON(t, o.e.json("GET", api1+"/drive/list?path=/", o.beto, nil, 200)); !strings.Contains(root, `"name":"Offices"`) {
		t.Fatalf("root = %s", root)
	}
	if c := o.status("GET", api1+"/drive/list?path="+url.QueryEscape("/Offices/Legal"), o.beto, nil); c != 404 {
		t.Fatalf("list Legal = %d", c)
	}
	if c := o.status("GET", api1+"/drive/list?path="+url.QueryEscape("/offices/legal"), o.beto, nil); c != 404 {
		t.Fatalf("list legal (lower case) = %d", c)
	}
	files := api1 + "/drive/files/" + id
	for _, sub := range []string{"", "/content", "/text", "/preview", "/versions", "/diff?from=1&to=1"} {
		if c := o.status("GET", files+sub, o.beto, nil); c != 404 {
			t.Fatalf("GET file%s = %d", sub, c)
		}
	}
	if s := o.e.json("GET", api1+"/drive/search?q=confidencialisima", o.beto, nil, 200); len(s["hits"].([]any)) != 0 {
		t.Fatalf("beto search = %v", s)
	}
	if s := o.e.json("GET", api1+"/drive/search?q=confidencialisima", o.ana, nil, 200); len(s["hits"].([]any)) != 1 {
		t.Fatalf("ana search = %v", s)
	}
	// writes are 404 too (no existence leak), and the trash hides what ana deleted there
	if c := o.status("PUT", drivePut("/Offices/Legal/x.md"), o.beto, []byte("x")); c != 404 {
		t.Fatalf("beto upload into Legal = %d", c)
	}
	if c := o.status("DELETE", files, o.beto, nil); c != 404 {
		t.Fatalf("beto delete = %d", c)
	}
	if c := o.status("DELETE", files, o.ana, nil); c != 204 {
		t.Fatalf("ana delete = %d", c)
	}
	if tr := mustJSON(t, o.e.json("GET", api1+"/drive/trash", o.beto, nil, 200)); strings.Contains(tr, id) {
		t.Fatalf("beto trash = %s", tr)
	}
	if tr := mustJSON(t, o.e.json("GET", api1+"/drive/trash", o.ana, nil, 200)); !strings.Contains(tr, id) {
		t.Fatalf("ana trash = %s", tr)
	}
	if c := o.status("POST", files+"/restore", o.beto, nil); c != 404 {
		t.Fatalf("beto restore = %d", c)
	}
	// who sees a folder and grants into it
	if c := o.status("GET", api1+"/drive/folder-access?path="+url.QueryEscape("/Offices/Legal"), o.beto, nil); c != 404 {
		t.Fatalf("beto folder-access = %d", c)
	}
	if c := o.status("POST", api1+"/drive/grants", o.beto, map[string]any{"agent_id": "sales", "path": "/Offices/Legal"}); c != 404 {
		t.Fatalf("beto grant = %d", c)
	}
	if c := o.status("PUT", api1+"/agents/sales/folder-access", o.beto, map[string]any{"path": "/Offices/Legal", "level": "read"}); c != 404 {
		t.Fatalf("beto agent folder-access = %d", c)
	}
	o.e.json("PUT", api1+"/agents/sales/folder-access", o.ana, map[string]any{"path": "/Offices/Legal", "level": "read"}, 200)
	if fa := mustJSON(t, o.e.json("GET", api1+"/agents/sales/folder-access", o.beto, nil, 200)); strings.Contains(fa, "Legal") {
		t.Fatalf("beto sees the agent's Legal rule: %s", fa)
	}
	if fa := mustJSON(t, o.e.json("GET", api1+"/agents/sales/folder-access", o.ana, nil, 200)); !strings.Contains(fa, "/Offices/Legal") {
		t.Fatalf("ana does not see the agent's Legal rule: %s", fa)
	}
}

func TestOfficeOpenIsReadOnlyForNonMember(t *testing.T) {
	o := newOfficeDrive(t)
	id := o.upload(o.ana, "/Offices/Ventas/precios.md", "precio de lista")
	files := api1 + "/drive/files/" + id
	o.e.json("GET", files, o.beto, nil, 200)
	if r, b := o.e.do("GET", files+"/content", o.beto, nil, nil); r.StatusCode != 200 || string(b) != "precio de lista" {
		t.Fatalf("content = %d %s", r.StatusCode, b)
	}
	o.e.json("GET", api1+"/drive/list?path="+url.QueryEscape("/Offices/Ventas"), o.beto, nil, 200)
	if s := o.e.json("GET", api1+"/drive/search?q=precio", o.beto, nil, 200); len(s["hits"].([]any)) != 1 {
		t.Fatalf("beto search = %v", s)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"PUT", drivePut("/Offices/Ventas/x.txt"), []byte("x")},
		{"PUT", files + "/content", []byte("otro")},
		{"POST", api1 + "/drive/folders", map[string]any{"path": "/Offices/Ventas/Nueva"}},
		{"PUT", files + "/tags", map[string]any{"tags": []string{"a"}}},
		{"POST", files + "/versions/1/restore", nil},
		{"POST", files + "/move", map[string]any{"path": "/Offices/Ventas/otro.md"}},
		{"DELETE", files, nil},
		{"POST", api1 + "/drive/grants", map[string]any{"agent_id": "sales", "path": "/Offices/Ventas"}},
	} {
		if got := o.status(c.method, c.path, o.beto, c.body); got != 403 {
			t.Fatalf("%s %s = %d, want 403", c.method, c.path, got)
		}
	}
	// a multipart upload: each file on its own; a refused one does not undo the others
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, n := range []string{"Shared/a.txt", "Offices/Ventas/b.txt", "Offices/Legal/c.txt"} {
		fw, _ := mw.CreateFormFile("file", n)
		_, _ = fw.Write([]byte("hola " + n))
	}
	_ = mw.Close()
	r, b := o.e.do("POST", api1+"/drive/upload?folder=/", o.beto, buf.Bytes(), map[string]string{"Content-Type": mw.FormDataContentType()})
	if r.StatusCode != 200 {
		t.Fatalf("multipart = %d %s", r.StatusCode, b)
	}
	var res struct {
		Results []struct {
			Path   string `json:"path"`
			Status int    `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &res); err != nil || len(res.Results) != 3 ||
		res.Results[0].Status != 201 || res.Results[1].Status != 403 || res.Results[2].Status != 404 {
		t.Fatalf("multipart results = %s", b)
	}
	o.e.json("GET", api1+"/drive/list?path=/Shared", o.beto, nil, 200)
	if l := mustJSON(t, o.e.json("GET", api1+"/drive/list?path="+url.QueryEscape("/Offices/Ventas"), o.ana, nil, 200)); strings.Contains(l, "b.txt") {
		t.Fatalf("the refused file was written: %s", l)
	}
}

func TestMoveOutOfOfficeNeedsBothEnds(t *testing.T) {
	o := newOfficeDrive(t)
	a := o.upload(o.ana, "/Offices/Legal/a.md", "a")
	if f := o.e.json("POST", api1+"/drive/files/"+a+"/move", o.ana, map[string]any{"path": "/Shared/a.md"}, 200); f["path"] != "/Shared/a.md" {
		t.Fatalf("ana move = %v", f)
	}
	b := o.upload(o.beto, "/Shared/b.md", "b")
	if c := o.status("POST", api1+"/drive/files/"+b+"/move", o.beto, map[string]any{"path": "/Offices/Legal/b.md"}); c != 404 {
		t.Fatalf("beto move into Legal = %d", c)
	}
	if f := o.e.json("GET", api1+"/drive/files/"+b, o.beto, nil, 200); f["path"] != "/Shared/b.md" {
		t.Fatalf("the file moved: %v", f)
	}
	// folders too, both ways
	dir := o.e.json("POST", api1+"/drive/folders", o.beto, map[string]any{"path": "/Shared/Carpeta"}, 201)
	if c := o.status("POST", api1+"/drive/folders/"+dir["id"].(string)+"/move", o.beto, map[string]any{"path": "/Offices/Legal/Carpeta"}); c != 404 {
		t.Fatalf("beto folder into Legal = %d", c)
	}
	sub := o.e.json("POST", api1+"/drive/folders", o.ana, map[string]any{"path": "/Offices/Legal/Sub"}, 201)
	if c := o.status("POST", api1+"/drive/folders/"+sub["id"].(string)+"/move", o.beto, map[string]any{"path": "/Shared/Sub"}); c != 404 {
		t.Fatalf("beto folder out of Legal = %d", c)
	}
	o.e.json("POST", api1+"/drive/folders/"+sub["id"].(string)+"/move", o.ana, map[string]any{"path": "/Shared/Sub"}, 200)
}

func TestMemberWorksNormally(t *testing.T) {
	o := newOfficeDrive(t)
	dir := o.e.json("POST", api1+"/drive/folders", o.ana, map[string]any{"path": "/Offices/Legal/Contratos"}, 201)
	id := o.upload(o.ana, "/Offices/Legal/Contratos/c.md", "uno")
	files := api1 + "/drive/files/" + id
	if c := o.status("PUT", files+"/content?message=dos", o.ana, []byte("dos")); c != 200 {
		t.Fatalf("new version = %d", c)
	}
	o.e.json("PUT", files+"/tags", o.ana, map[string]any{"tags": []string{"firmado"}}, 200)
	o.e.json("POST", files+"/versions/1/restore", o.ana, nil, 200)
	if c := o.status("DELETE", files, o.ana, nil); c != 204 {
		t.Fatalf("delete = %d", c)
	}
	o.e.json("POST", files+"/restore", o.ana, nil, 200)
	if r, b := o.e.do("GET", files+"/content", o.ana, nil, nil); r.StatusCode != 200 || string(b) != "uno" {
		t.Fatalf("content = %d %s", r.StatusCode, b)
	}
	if c := o.status("DELETE", api1+"/drive/folders/"+dir["id"].(string), o.ana, nil); c != 204 {
		t.Fatalf("delete folder = %d", c)
	}
	if c := o.status("POST", api1+"/drive/folders/"+dir["id"].(string)+"/restore", o.beto, nil); c != 404 {
		t.Fatalf("beto restores Legal's folder = %d", c)
	}
	o.e.json("POST", api1+"/drive/folders/"+dir["id"].(string)+"/restore", o.ana, nil, 200)
	if l := mustJSON(t, o.e.json("GET", api1+"/drive/list?path="+url.QueryEscape("/Offices/Legal/Contratos"), o.ana, nil, 200)); !strings.Contains(l, "c.md") {
		t.Fatalf("list = %s", l)
	}
}

func TestBecomingPrivateTakesEffectNextRequest(t *testing.T) {
	o := newOfficeDrive(t)
	id := o.upload(o.ana, "/Offices/Ventas/v.md", "v")
	o.e.json("GET", api1+"/drive/files/"+id, o.beto, nil, 200)
	o.e.json("PUT", api1+"/offices/"+o.ventas, o.ana, map[string]any{"visibility": "private"}, 200)
	if c := o.status("GET", api1+"/drive/files/"+id, o.beto, nil); c != 404 {
		t.Fatalf("after private = %d", c)
	}
}

// hiddenVsMissing asserts that what beto gets for a file in Legal is word for word what he gets for a missing one.
func hiddenVsMissing(t *testing.T, o *officeDrive, method, u string, body func(p string) any) {
	t.Helper()
	r1, b1 := o.e.do(method, u, o.beto, body("/Offices/Legal/contrato.md"), nil)
	r2, b2 := o.e.do(method, u, o.beto, body("/Offices/Legal/nada.md"), nil)
	if r1.StatusCode != 404 || r1.StatusCode != r2.StatusCode || strings.ReplaceAll(string(b1), "contrato", "nada") != string(b2) {
		t.Fatalf("hidden = %d %s; missing = %d %s", r1.StatusCode, b1, r2.StatusCode, b2)
	}
}

func (o *officeDrive) run(token, raw string) (int, string) {
	o.e.t.Helper()
	r, b := o.e.do("POST", api1+"/commands/run", token, map[string]any{"raw": raw, "lang": "en", "context": map[string]any{"surface": "office"}}, nil)
	return r.StatusCode, string(b)
}

func TestOfficeHiddenFromMeetingMinutes(t *testing.T) {
	o := newOfficeDrive(t)
	o.upload(o.ana, "/Offices/Legal/contrato.md", "acuerdo secreto")
	u := api1 + "/meetings/mtg_ghost/minutes"
	hiddenVsMissing(t, o, "POST", u, func(p string) any { return map[string]any{"file_path": p} })
	// for ana the file gate passes and the (missing) meeting answers instead
	if r, b := o.e.do("POST", u, o.ana, map[string]any{"file_path": "/Offices/Legal/contrato.md"}, nil); r.StatusCode != 404 || strings.Contains(string(b), "file not found") {
		t.Fatalf("ana = %d %s", r.StatusCode, b)
	}
	// the /minutes command with a path
	c1, b1 := o.run(o.beto, "/minutes /Offices/Legal/contrato.md")
	c2, b2 := o.run(o.beto, "/minutes /Offices/Legal/nada.md")
	if c1 != 404 || c1 != c2 || b1 != b2 {
		t.Fatalf("/minutes hidden = %d %s; missing = %d %s", c1, b1, c2, b2)
	}
}

func TestOfficeHiddenFromProjectInputs(t *testing.T) {
	o := newOfficeDrive(t)
	csv := map[string]string{"Content-Type": "text/csv"}
	if r, b := o.e.do("PUT", drivePut("/Offices/Legal/matriz.csv"), o.ana, []byte("creditor,debtor\nS01,S02\n"), csv); r.StatusCode != 201 {
		t.Fatalf("upload csv = %d %s", r.StatusCode, b)
	}
	created := o.e.json("POST", api1+"/projects/draft", o.owner.AccessToken, map[string]any{"goal": "Cierre", "template_id": "group_consolidated_close",
		"params": map[string]string{"subsidiaries": "3"}, "locale": "es"}, 201)
	link := api1 + "/projects/" + str(created, "project_id") + "/inputs/intercompany/link"
	r1, b1 := o.e.do("POST", link, o.beto, map[string]any{"paths": []string{"/Offices/Legal/matriz.csv"}}, nil)
	r2, b2 := o.e.do("POST", link, o.beto, map[string]any{"paths": []string{"/Offices/Legal/nada.csv"}}, nil)
	if r1.StatusCode != 404 || !strings.Contains(string(b1), "file_not_found") || strings.ReplaceAll(string(b1), "matriz", "nada") != string(b2) || r2.StatusCode != 404 {
		t.Fatalf("hidden = %d %s; missing = %d %s", r1.StatusCode, b1, r2.StatusCode, b2)
	}
	if v := o.e.json("POST", link, o.ana, map[string]any{"paths": []string{"/Offices/Legal/matriz.csv"}}, 200); !strings.Contains(mustJSON(t, v), "matriz.csv") {
		t.Fatalf("ana link = %v", v)
	}
}

func TestOfficeHiddenFromCommandSearch(t *testing.T) {
	o := newOfficeDrive(t)
	o.upload(o.ana, "/Offices/Legal/contrato.md", "clausula confidencialisima")
	if c, b := o.run(o.beto, "/search confidencialisima"); strings.Contains(b, "contrato") || strings.Contains(b, "Legal") {
		t.Fatalf("beto /search = %d %s", c, b)
	}
	if c, b := o.run(o.ana, "/search confidencialisima"); c != 200 || !strings.Contains(b, "contrato.md") {
		t.Fatalf("ana /search = %d %s", c, b)
	}
	if c, b := o.run(o.beto, "/file /Offices/Legal/contrato.md"); c != 404 || strings.Contains(b, "files?open") {
		t.Fatalf("beto /file path = %d %s", c, b)
	}
	if c, b := o.run(o.beto, "/file confidencialisima"); c != 404 || strings.Contains(b, "files?open") {
		t.Fatalf("beto /file search = %d %s", c, b)
	}
	if c, b := o.run(o.ana, "/file /Offices/Legal/contrato.md"); c != 200 || !strings.Contains(b, "files?open") {
		t.Fatalf("ana /file = %d %s", c, b)
	}
	complete := api1 + "/commands/complete?kind=file&q=confidencialisima"
	if l := o.e.list(complete, o.beto); len(l) != 0 {
		t.Fatalf("beto completes %v", l)
	}
	if l := o.e.list(complete, o.ana); len(l) != 1 {
		t.Fatalf("ana completes %v", l)
	}
}

func TestOfficeHiddenFromChatAttachments(t *testing.T) {
	o := newOfficeDrive(t)
	o.upload(o.ana, "/Offices/Legal/contrato.md", "x")
	post := func(token, text string) {
		t.Helper()
		if r, b := o.e.do("POST", api1+"/messages", token, map[string]any{"conversation": "office", "text": text,
			"attachments": []string{"/Offices/Legal/contrato.md"}}, nil); r.StatusCode >= 300 {
			t.Fatalf("post = %d %s", r.StatusCode, b)
		}
	}
	files := func(text string) []domain.FileRef {
		t.Helper()
		_, raw := o.e.do("GET", api1+"/conversations/office/messages", o.owner.AccessToken, nil, nil)
		var ms []domain.Message
		if err := json.Unmarshal(raw, &ms); err != nil {
			t.Fatalf("messages: %v %s", err, raw)
		}
		for _, m := range ms {
			if m.Text == text {
				return m.Files
			}
		}
		t.Fatalf("message %q not found in %s", text, raw)
		return nil
	}
	post(o.beto, "mira esto beto")
	if fs := files("mira esto beto"); len(fs) != 0 {
		t.Fatalf("beto attached a hidden file: %v", fs)
	}
	post(o.ana, "mira esto ana")
	if fs := files("mira esto ana"); len(fs) != 1 || fs[0].Path != "/Offices/Legal/contrato.md" {
		t.Fatalf("ana's attachment: %v", fs)
	}
}
