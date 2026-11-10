package api_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"strings"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/auth"
)

func multipartFile(t *testing.T, filename string, data []byte, fields map[string]string) ([]byte, map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write(data)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	return buf.Bytes(), map[string]string{"Content-Type": mw.FormDataContentType()}
}

// The skill library over HTTP: members draft, only admins review; export then
// import gives the same skill; imports always wait for a person.
func TestSkillsOverHTTP(t *testing.T) {
	e := newEnv(t, opts{auth: true, skills: true})
	owner := e.register("owner-skills@example.com", "OwnerSkills")
	admin := e.member(owner, "admin-skills@example.com", auth.RoleAdmin)
	member := e.member(owner, "member-skills@example.com", auth.RoleMember)
	viewer := e.member(owner, "viewer-skills@example.com", auth.RoleViewer)
	rival := e.register("rival-skills@example.com", "RivalSkills")
	base := api1 + "/skills"

	// The first listing seeds the starter skills (active).
	starters := e.list(base, viewer)
	if len(starters) < 7 {
		t.Fatalf("starters: %d", len(starters))
	}
	if r, _ := e.do("GET", base, "", nil, nil); r.StatusCode != 401 {
		t.Fatalf("anonymous = %d", r.StatusCode)
	}

	body := map[string]any{"name": "weekly-report", "description": "Use for the weekly report", "instructions": "# Weekly\n\nSteps.\n",
		"allowed_tools": []string{"email.send"}, "files": []map[string]string{{"path": "templates/report.md", "content": "# Report\n"}}}
	if c := e.status("POST", base, viewer, body); c != 403 {
		t.Fatalf("viewer create = %d", c)
	}
	d := e.json("POST", base, member, body, 201)
	id := str(d, "id")
	if str(d, "status") != "draft" || id == "" {
		t.Fatalf("create: %v", d)
	}
	if r, b := e.do("POST", base, member, body, nil); r.StatusCode != 409 || !strings.Contains(string(b), "name_taken") {
		t.Fatalf("duplicate = %d %s", r.StatusCode, b)
	}
	if r, b := e.do("POST", base, member, map[string]any{"name": "Bad Name", "description": "x", "instructions": "y"}, nil); r.StatusCode != 400 || !strings.Contains(string(b), "name_invalid") {
		t.Fatalf("invalid name = %d %s", r.StatusCode, b)
	}

	// Members cannot review, activate, disable or delete; admins can.
	review := map[string]any{"decision": "approve", "version": 1}
	for name, c := range map[string]int{
		"review":  e.status("POST", base+"/"+id+"/review", member, review),
		"disable": e.status("POST", base+"/"+id+"/disable", member, nil),
		"delete":  e.status("DELETE", base+"/"+id, member, nil),
	} {
		if c != 403 {
			t.Errorf("member %s = %d", name, c)
		}
	}
	e.json("POST", base+"/"+id+"/submit", member, nil, 200)
	if got := e.json("POST", base+"/"+id+"/review", admin, review, 200); str(got, "status") != "active" {
		t.Fatalf("approve: %v", got)
	}

	// Assignment is an agent change: admins; the agent sees it as servable.
	if c := e.status("PUT", api1+"/agents/sales/skills/"+id, member, nil); c != 403 {
		t.Fatalf("member assign = %d", c)
	}
	as := e.list(api1+"/agents/sales/skills", viewer)
	_ = as
	if r, b := e.do("PUT", api1+"/agents/sales/skills/"+id, admin, nil, nil); r.StatusCode != 200 || !strings.Contains(string(b), "weekly-report") {
		t.Fatalf("assign = %d %s", r.StatusCode, b)
	}
	if r, _ := e.do("PUT", api1+"/agents/ghost/skills/"+id, admin, nil, nil); r.StatusCode != 404 {
		t.Fatalf("assign to a ghost = %d", r.StatusCode)
	}

	// Detail, update (a member's edit of an active skill waits for review), versions, restore.
	got := e.json("GET", base+"/"+id, viewer, nil, 200)
	if !strings.Contains(str(got, "instructions"), "Steps.") {
		t.Fatalf("detail: %v", got)
	}
	body["instructions"] = "# Weekly\n\nNew steps.\n"
	up := e.json("PUT", base+"/"+id, member, body, 200)
	if str(up, "status") != "in_review" || up["current_version"].(float64) != 2 || up["active_version"].(float64) != 1 || up["changed"] != true {
		t.Fatalf("update: %v", up)
	}
	if vs := e.list(base+"/"+id+"/versions", viewer); len(vs) != 2 {
		t.Fatalf("versions: %v", vs)
	}
	e.json("POST", base+"/"+id+"/versions/1/restore", member, nil, 200)

	// Export (zip in the standard layout, and SKILL.md alone), then import it again.
	r, zb := e.do("GET", base+"/"+id+"/export?version=1", viewer, nil, nil)
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "application/zip" || !strings.Contains(r.Header.Get("Content-Disposition"), "weekly-report.zip") {
		t.Fatalf("export = %d %v", r.StatusCode, r.Header)
	}
	zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "weekly-report/SKILL.md,weekly-report/templates/report.md" {
		t.Fatalf("zip layout: %v", names)
	}
	if r, md := e.do("GET", base+"/"+id+"/export?format=md", viewer, nil, nil); r.StatusCode != 200 || !strings.HasPrefix(string(md), "---\nname: weekly-report\n") {
		t.Fatalf("md export = %d %s", r.StatusCode, md)
	}
	mp, hdr := multipartFile(t, "weekly-report.zip", zb, nil)
	if r, b := e.do("POST", base+"/import", member, mp, hdr); r.StatusCode != 409 || !strings.Contains(string(b), "name_taken") {
		t.Fatalf("import over an existing name = %d %s", r.StatusCode, b)
	}
	mp, hdr = multipartFile(t, "weekly-report.zip", zb, map[string]string{"name": "weekly-report-2"})
	r, b := e.do("POST", base+"/import", member, mp, hdr)
	if r.StatusCode != 201 {
		t.Fatalf("import = %d %s", r.StatusCode, b)
	}
	var imp map[string]any
	_ = json.Unmarshal(b, &imp)
	if str(imp, "status") != "in_review" || str(imp, "source") != "imported" || str(imp, "name") != "weekly-report-2" {
		t.Fatalf("imported: %v", imp)
	}
	if !strings.Contains(string(b), `"allowed_tools"`) || !strings.Contains(string(b), "allowed_tools") {
		t.Fatalf("the reviewer must see the warnings: %s", b)
	}
	// hostile uploads are refused with a stable code
	var evil bytes.Buffer
	zw := zip.NewWriter(&evil)
	w, _ := zw.Create("../x.md")
	_, _ = w.Write([]byte("x"))
	_ = zw.Close()
	mp, hdr = multipartFile(t, "evil.zip", evil.Bytes(), nil)
	if r, b := e.do("POST", base+"/import", member, mp, hdr); r.StatusCode != 400 || !strings.Contains(string(b), "path_traversal") {
		t.Fatalf("traversal = %d %s", r.StatusCode, b)
	}
	mp, hdr = multipartFile(t, "SKILL.md", []byte("no frontmatter"), nil)
	if r, b := e.do("POST", base+"/import", member, mp, hdr); r.StatusCode != 400 || !strings.Contains(string(b), "frontmatter_missing") {
		t.Fatalf("bad skill = %d %s", r.StatusCode, b)
	}
	if c := e.status("POST", base+"/import", viewer, nil); c != 403 {
		t.Fatalf("viewer import = %d", c)
	}

	// Disable and delete (admin); another organization never sees any of it.
	e.json("POST", base+"/"+id+"/disable", admin, nil, 200)
	if c := e.status("DELETE", base+"/"+id, admin, nil); c != 204 {
		t.Fatalf("delete = %d", c)
	}
	if c := e.status("GET", base+"/"+id, viewer, nil); c != 404 {
		t.Fatalf("deleted get = %d", c)
	}
	if c := e.status("GET", base+"/"+str(imp, "id"), rival.AccessToken, nil); c != 404 {
		t.Fatalf("cross-tenant = %d", c)
	}
}

// Skills of a department over HTTP: a private office's skills are invisible to everyone outside it (administrators
// too), the responsible writes them, and only the agents of that department can receive them.
func TestSkillsOfADepartmentOverHTTP(t *testing.T) {
	e := newEnv(t, opts{auth: true, orgchart: true, skills: true, deps: func(d *api.Deps) {
		withOffices(d)
		d.Skills.SetOffices(d.Offices.Model)
	}})
	owner := e.register("owner-sd@example.com", "OwnerSD")
	ana := e.member(owner, "ana-sd@example.com", auth.RoleMember)   // responsible of Legal
	luis := e.member(owner, "luis-sd@example.com", auth.RoleMember) // member of Legal
	marta := e.member(owner, "marta-sd@example.com", auth.RoleMember)
	dan := e.member(owner, "dan-sd@example.com", auth.RoleAdmin) // admin who works in Legal
	anaID, luisID, danID := e.userID(ana), e.userID(luis), e.userID(dan)
	dep := e.json("POST", api1+"/org/departments", owner.AccessToken, map[string]any{"name": "Legal", "head": map[string]any{"kind": "person", "id": anaID}}, 201)
	depID := str(dep, "id")
	e.json("PUT", api1+"/org/people/"+luisID, owner.AccessToken, map[string]any{"department_id": depID}, 200)
	e.json("PUT", api1+"/org/people/"+danID, owner.AccessToken, map[string]any{"department_id": depID}, 200)
	e.json("PUT", api1+"/agents/sales/job", owner.AccessToken, map[string]any{"department_id": depID}, 200)
	e.json("POST", api1+"/offices", owner.AccessToken, map[string]any{"department_id": depID, "visibility": "private"}, 201)
	base := api1 + "/skills"
	body := func(name string) map[string]any {
		return map[string]any{"name": name, "description": "Use for " + name, "instructions": "# " + name + "\n\nSteps.\n", "office_id": depID}
	}

	// the responsible creates; a plain member of the office and an outsider cannot
	d := e.json("POST", base, ana, body("contract-review"), 201)
	id := str(d, "id")
	if str(d, "office_id") != depID || str(d, "office_name") != "Legal" {
		t.Fatalf("created: %v", d)
	}
	if c := e.status("POST", base, luis, body("nda-checklist")); c != 403 {
		t.Fatalf("member create = %d", c)
	}
	if c := e.status("POST", base, marta, body("legal-spy")); c != 403 {
		t.Fatalf("outsider create = %d", c)
	}
	e.json("POST", base, owner.AccessToken, map[string]any{"name": "company-wide", "description": "Everybody", "instructions": "# Hi\n"}, 201)

	// the list: members of Legal see it, the owner (admin, not a member) and Marta do not
	has := func(tok, name string) bool {
		for _, s := range e.list(base, tok) {
			if str(s, "name") == name {
				return true
			}
		}
		return false
	}
	if !has(luis, "contract-review") || !has(dan, "contract-review") || !has(ana, "contract-review") {
		t.Fatal("members of Legal must see its skill")
	}
	if has(marta, "contract-review") || has(owner.AccessToken, "contract-review") {
		t.Fatal("a private department's skill leaked to the list")
	}
	if !has(marta, "company-wide") || !has(owner.AccessToken, "company-wide") {
		t.Fatal("company skills are for everybody")
	}
	// hidden = inexistent, in detail, versions, edit, delete and export
	for _, tok := range []string{marta, owner.AccessToken} {
		for _, c := range []struct{ m, p string }{{"GET", ""}, {"GET", "/versions"}, {"GET", "/export"}, {"PUT", ""}, {"DELETE", ""}} {
			if got := e.status(c.m, base+"/"+id+c.p, tok, body("contract-review")); got != 404 {
				t.Fatalf("%s %s = %d, want 404", c.m, c.p, got)
			}
		}
	}
	if c := e.status("POST", base+"/"+id+"/disable", owner.AccessToken, nil); c != 404 {
		t.Fatalf("admin outside the department disables = %d, want 404", c)
	}
	e.json("GET", base+"/"+id, luis, nil, 200)

	// only the responsible edits; a skill never changes department
	if c := e.status("PUT", base+"/"+id, luis, body("contract-review")); c != 403 {
		t.Fatalf("member edit = %d", c)
	}
	edit := body("contract-review")
	edit["instructions"] = "# contract-review\n\nNew steps.\n"
	e.json("PUT", base+"/"+id, ana, edit, 200)
	edit["office_id"] = "general-other"
	if c := e.status("PUT", base+"/"+id, ana, edit); c != 400 {
		t.Fatalf("change department = %d", c)
	}

	// assigning: only to the agents of the department
	if r, b := e.do("PUT", api1+"/agents/accounting/skills/"+id, dan, nil, nil); r.StatusCode != 400 || !strings.Contains(string(b), "skill belongs to another department") {
		t.Fatalf("assign to another department = %d %s", r.StatusCode, b)
	}
	if c := e.status("PUT", api1+"/agents/sales/skills/"+id, dan, nil); c != 200 {
		t.Fatalf("assign to Legal's agent = %d", c)
	}
	if c := e.status("PUT", api1+"/agents/sales/skills/"+id, owner.AccessToken, nil); c != 404 {
		t.Fatalf("hidden assign = %d", c)
	}

	// the responsible deletes her own; a member who is not responsible cannot
	if c := e.status("DELETE", base+"/"+id, luis, nil); c != 403 {
		t.Fatalf("member delete = %d", c)
	}
	if c := e.status("DELETE", base+"/"+id, ana, nil); c != 204 {
		t.Fatalf("responsible delete = %d", c)
	}
}
