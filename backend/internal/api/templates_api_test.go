package api_test

import (
	"testing"

	"aiworkforce/backend/internal/auth"
)

func apiTemplateBody() map[string]any {
	return map[string]any{
		"name": "Revisión por cliente", "description": "Una revisión por cliente", "category": "finance", "shared": true,
		"params": []map[string]any{{"key": "clients", "label": "Clientes", "type": "list", "item_name": "client", "default": "Acme, Beta", "max": 10}},
		"objectives": []map[string]any{{"key": "o1", "title": "Revisión", "workflows": []map[string]any{
			{"key": "w1", "title": "{client}", "repeat": []string{"clients"}, "nodes": []map[string]any{
				{"key": "rev", "title": "Revisar a {client}", "agent": "accounting", "complexity": "M"},
				{"key": "ok", "title": "Aprobar a {client}", "kind": "gate", "deps": []string{"rev"}, "approval": map[string]any{"action": "approve_review", "risk": "medium"}},
			}},
		}}},
	}
}

func TestProjectTemplatesOverHTTP(t *testing.T) {
	e := newEnv(t, opts{auth: true, ws: true})
	owner := e.register("owner@example.com", "Owner")
	viewer := e.member(owner, "viewer@example.com", auth.RoleViewer)
	member := e.member(owner, "member@example.com", auth.RoleMember)
	admin := e.member(owner, "admin@example.com", auth.RoleAdmin)
	rival := e.register("rival@example.com", "Rival")

	if c := e.status("POST", api1+"/project-templates", viewer, apiTemplateBody()); c != 403 {
		t.Fatalf("a viewer created a template: %d", c)
	}
	if c := e.status("POST", api1+"/project-templates", member, map[string]any{"name": "x", "objectives": []any{}}); c != 400 {
		t.Fatalf("an empty template = %d, want 400", c)
	}
	created := e.json("POST", api1+"/project-templates", member, apiTemplateBody(), 201)
	id := str(created, "id")
	if id == "" || created["version"] != float64(1) || created["builtin"] != false {
		t.Fatalf("created: %v", created)
	}
	if stats, _ := created["stats"].(map[string]any); stats["tasks"] != float64(4) || stats["tasks_max"] != float64(20) {
		t.Fatalf("stats: %v", created["stats"])
	}
	// anyone reads; the library lists built-in and organization templates
	if got := e.json("GET", api1+"/project-templates/"+id, viewer, nil, 200); str(got, "name") != "Revisión por cliente" {
		t.Fatalf("get: %v", got)
	}
	found := map[string]bool{}
	for _, tp := range e.list(api1+"/project-templates", viewer) {
		found[str(tp, "id")] = true
	}
	if !found[id] || !found["tpl-annual-budget"] || !found["tpl-erp-migration"] {
		t.Fatalf("library: %v", found)
	}
	// validate and preview without saving
	bad := apiTemplateBody()
	bad["objectives"].([]map[string]any)[0]["workflows"].([]map[string]any)[0]["nodes"].([]map[string]any)[0]["deps"] = []string{"ok"}
	if v := e.json("POST", api1+"/project-templates/validate", member, bad, 200); v["ok"] != false {
		t.Fatalf("a cycle must not validate: %v", v)
	}
	pv := e.json("POST", api1+"/project-templates/preview", member, map[string]any{"template_id": id, "params": map[string]string{"clients": "A, B, C"}}, 200)
	if pv["tasks"] != float64(6) || pv["gates"] != float64(3) {
		t.Fatalf("preview: %v", pv)
	}
	// edit -> versions -> restore
	body := apiTemplateBody()
	body["name"] = "Revisión v2"
	body["base_version"] = 1
	if v2 := e.json("PUT", api1+"/project-templates/"+id, member, body, 200); v2["version"] != float64(2) {
		t.Fatalf("update: %v", v2)
	}
	if c := e.status("PUT", api1+"/project-templates/"+id, member, body); c != 409 {
		t.Fatalf("stale update = %d, want 409", c)
	}
	if vs := e.list(api1+"/project-templates/"+id+"/versions", member); len(vs) != 2 {
		t.Fatalf("versions: %v", vs)
	}
	if r := e.json("POST", api1+"/project-templates/"+id+"/versions/1/restore", member, nil, 200); r["version"] != float64(3) || str(r, "name") != "Revisión por cliente" {
		t.Fatalf("restore: %v", r)
	}
	// duplicate a built-in, edit the copy, create a project from it
	cp := e.json("POST", api1+"/project-templates/tpl-annual-budget/duplicate", member, map[string]any{"name": "Mi presupuesto"}, 201)
	if str(cp, "name") != "Mi presupuesto" || cp["forked_from"] != "tpl-annual-budget" {
		t.Fatalf("duplicate: %v", cp)
	}
	pid := str(e.json("POST", api1+"/projects/draft", member, map[string]any{"template_id": str(cp, "id"), "params": map[string]string{"units": "A, B", "countries": "X, Y"}}, 201), "project_id")
	if pid == "" {
		t.Fatal("no project from the copy")
	}
	// save a project as a template with a name
	saved := e.json("POST", api1+"/projects/"+pid+"/save-as-template", member, map[string]any{"name": "Presupuesto guardado", "category": "finance", "shared": false}, 201)
	if str(saved, "name") != "Presupuesto guardado" || saved["shared"] == true {
		t.Fatalf("save as template: %v", saved)
	}
	// permissions and tenants: members create but never delete; another org sees nothing
	if c := e.status("DELETE", api1+"/project-templates/"+id, member, nil); c != 403 {
		t.Fatalf("member delete = %d, want 403", c)
	}
	if c := e.status("DELETE", api1+"/project-templates/"+id, viewer, nil); c != 403 {
		t.Fatalf("viewer delete = %d, want 403", c)
	}
	if c := e.status("GET", api1+"/project-templates/"+id, rival.AccessToken, nil); c != 404 {
		t.Fatalf("rival reads the template = %d", c)
	}
	if c := e.status("DELETE", api1+"/project-templates/"+id, rival.AccessToken, nil); c != 403 && c != 404 {
		t.Fatalf("rival deletes = %d", c)
	}
	if c := e.status("DELETE", api1+"/project-templates/"+id, admin, nil); c != 204 {
		t.Fatalf("admin delete = %d, want 204", c)
	}
	if c := e.status("GET", api1+"/project-templates/"+id, admin, nil); c != 404 {
		t.Fatalf("deleted template = %d", c)
	}
	if c := e.status("DELETE", api1+"/project-templates/tpl-generic", admin, nil); c != 404 {
		t.Fatalf("deleting a built-in = %d, want 404", c)
	}
}
