package api_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/projects"
)

// HTTP-level tests of the projects and artifacts endpoints (docs/architecture/
// workflow-visualization.md Appendix A and agent-workspaces.md sec. 11).

type infoFn func(ctx context.Context, id string) (string, string, error)

func (f infoFn) Info(ctx context.Context, id string) (string, string, error) { return f(ctx, id) }

func attachWorkspaces(d *api.Deps, core application.Store, rec *application.Recorder, appr *application.Approvals, orch *application.Orchestrator, log *slog.Logger, ctx context.Context,
	gate func(ctx context.Context, org string) error) {
	var ps *projects.Service
	as := artifacts.New(artifacts.Config{Store: artifacts.NewMemStore(), WriteGate: gate, Rec: rec, Core: core, Asker: orch, OrgID: d.Cfg.OrgID, Log: log,
		Projects: infoFn(func(ctx context.Context, id string) (string, string, error) { return ps.Info(ctx, id) })})
	ps = projects.New(ctx, projects.Config{Store: projects.NewMemStore(), Orch: orch, Core: core, Approvals: appr, Rec: rec, Runtime: fakeRuntime{}, Sink: as,
		OrgID: d.Cfg.OrgID, Poll: 10 * time.Millisecond, Log: log})
	d.Projects, d.Artifacts = ps, as
}

func (e *env) json(method, path, token string, body any, want int) map[string]any {
	e.t.Helper()
	resp, raw := e.do(method, path, token, body, nil)
	if resp.StatusCode != want {
		e.t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func (e *env) list(path, token string) []map[string]any {
	e.t.Helper()
	resp, raw := e.do("GET", path, token, nil, nil)
	if resp.StatusCode != 200 {
		e.t.Fatalf("GET %s = %d: %s", path, resp.StatusCode, raw)
	}
	var arr []map[string]any
	if json.Unmarshal(raw, &arr) == nil {
		return arr
	}
	var wrapped struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(raw, &wrapped)
	return wrapped.Items
}

func (e *env) eventually(what string, fn func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("timeout waiting for %s", what)
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

const api1 = "/api/v1"

func TestProjectsLifecycleOverHTTP(t *testing.T) {
	e := newEnv(t, opts{ws: true})
	// Templates: the built-ins, the catalog workflows and (later) the saved ones.
	ids := map[string]bool{}
	for _, tp := range e.list(api1+"/project-templates", "") {
		ids[str(tp, "id")] = true
	}
	if !ids["tpl-financial-close"] || !ids["wf:month_close"] {
		t.Fatalf("templates: %v", ids)
	}
	// Draft -> snapshot.
	created := e.json("POST", api1+"/projects/draft", "", map[string]any{"goal": "cierre financiero", "template_id": "tpl-financial-close"}, 201)
	id := str(created, "project_id")
	snap := e.json("GET", api1+"/projects/"+id+"/snapshot?depth=full", "", nil, 200)
	proj := snap["project"].(map[string]any)
	if proj["status"] != "draft" || proj["tasks_total"].(float64) != 14 || len(snap["objectives"].([]any)) != 3 || snap["estimate"] == nil {
		t.Fatalf("snapshot: %v", proj)
	}
	nodes := snap["nodes"].([]any)
	if len(nodes) != 14+4 {
		t.Fatalf("nodes (tasks + workflow groups) = %d", len(nodes))
	}
	if est := e.json("POST", api1+"/projects/"+id+"/estimate", "", nil, 200)["estimate"].(map[string]any); est["basis"] != "priors" {
		t.Fatalf("estimate: %v", est)
	}
	if v := e.json("POST", api1+"/projects/"+id+"/validate", "", nil, 200); v["ok"] != true {
		t.Fatalf("validate: %v", v)
	}
	first := nodes[1].(map[string]any)
	patch := e.json("PATCH", api1+"/projects/"+id+"/plan", "", map[string]any{"ops": []map[string]any{{"op": "update", "id": first["id"], "fields": map[string]any{"title": "Recopilar balanza"}}}}, 200)
	if patch["structure_version"].(float64) != 2 {
		t.Fatalf("patch: %v", patch)
	}
	if c := e.status("PATCH", api1+"/projects/"+id+"/plan", "", map[string]any{"ops": []map[string]any{{"op": "delete", "id": "x"}}}); c != 400 {
		t.Fatalf("unsupported op = %d", c)
	}
	// Controls on a draft and invalid launches.
	if c := e.status("POST", api1+"/projects/"+id+"/pause", "", nil); c != 409 {
		t.Fatalf("pausing a draft = %d", c)
	}
	if c := e.status("POST", api1+"/projects/"+id+"/launch", "", map[string]any{"approved_budget_usd": 0.0001}); c != 409 {
		t.Fatalf("underbudget launch = %d", c)
	}
	if c := e.status("POST", api1+"/projects/nope/launch", "", map[string]any{"approved_budget_usd": 5}); c != 404 {
		t.Fatalf("unknown project = %d", c)
	}
	e.json("PUT", api1+"/projects/"+id+"/budget", "", map[string]any{"budget_usd": 5}, 200)
	launched := e.json("POST", api1+"/projects/"+id+"/launch", "", map[string]any{"approved_budget_usd": 5}, 202)
	if launched["project"].(map[string]any)["status"] != "running" {
		t.Fatalf("launch: %v", launched)
	}

	// The tasks exist for real and run in parallel: the orchestrator lists them.
	reqID := str(e.json("GET", api1+"/projects/"+id, "", nil, 200)["project"].(map[string]any), "request_id")
	e.eventually("tasks of the request", func() bool {
		d := e.json("GET", api1+"/requests/"+reqID, "", nil, 200)
		ts, _ := d["tasks"].([]any)
		return len(ts) == 14
	})
	// Approve the statements by batch, the owner gate individually.
	pending := func(action string) []map[string]any {
		var out []map[string]any
		for _, a := range e.list(api1+"/projects/"+id+"/approvals", "") {
			if a["status"] == "pending" && a["action"] == action {
				out = append(out, a)
			}
		}
		return out
	}
	e.eventually("both statements waiting", func() bool { return len(pending("publish_statement")) == 2 })
	batch := func(action string, n int, high bool, want int) map[string]any {
		return e.json("POST", api1+"/approvals/batch", "", map[string]any{"filter": map[string]any{"project_id": id, "action": action}, "decision": "approve", "expected_count": n, "include_high": high}, want)
	}
	batch("publish_statement", 1, false, 409)
	if res := batch("publish_statement", 2, false, 200); res["decided"].(float64) != 2 {
		t.Fatalf("batch: %v", res)
	}
	e.eventually("owner gate", func() bool { return len(pending("approve_close")) == 1 })
	batch("approve_close", 1, false, 409) // high risk needs confirmation
	gate := pending("approve_close")[0]
	e.json("POST", api1+"/approvals/"+str(gate, "id")+"/decision", "", map[string]string{"decision": "approve"}, 200)
	e.eventually("project done", func() bool {
		return e.json("GET", api1+"/projects/"+id+"/snapshot", "", nil, 200)["project"].(map[string]any)["status"] == "done"
	})
	h := e.json("GET", api1+"/projects/"+id+"/health", "", nil, 200)
	if h["light"] != "green" || h["project_id"] != id {
		t.Fatalf("health: %v", h)
	}
	if c := e.status("POST", api1+"/projects/"+id+"/cancel", "", nil); c != 409 {
		t.Fatalf("cancel of a finished project = %d", c)
	}
	// Save as a template and list it; the project list has the project.
	saved := e.json("POST", api1+"/projects/"+id+"/save-as-template", "", nil, 201)
	if saved["builtin"] != false {
		t.Fatalf("saved template: %v", saved)
	}
	found := false
	for _, tp := range e.list(api1+"/project-templates", "") {
		found = found || str(tp, "id") == str(saved, "id")
	}
	if !found {
		t.Fatal("the saved template is not listed")
	}
	if l := e.list(api1+"/projects", ""); len(l) != 1 || l[0]["status"] != "done" {
		t.Fatalf("projects: %v", l)
	}
	// Deliverables of the project were created in its workspace (artifacts) and
	// finished. The workspace sink runs on the engine tick after the last task,
	// so it can trail the derived "done" status by one tick.
	var ws map[string]any
	e.eventually("deliverables finished", func() bool {
		ws = e.json("GET", api1+"/projects/"+id+"/workspace", "", nil, 200)
		dl, _ := ws["deliverables"].([]any)
		if len(dl) == 0 {
			return false
		}
		for _, d := range dl {
			if d.(map[string]any)["build_state"] != "ready_for_review" {
				return false
			}
		}
		return true
	})
	if ws["name"] == "" {
		t.Fatalf("workspace: %v", ws)
	}
}

func TestProjectsAndArtifactsPermissionsAndTenants(t *testing.T) {
	e := newEnv(t, opts{auth: true, ws: true})
	owner := e.register("owner@example.com", "Owner")
	viewer := e.member(owner, "viewer@example.com", auth.RoleViewer)
	member := e.member(owner, "member@example.com", auth.RoleMember)
	admin := e.member(owner, "admin@example.com", auth.RoleAdmin)
	rival := e.register("rival@example.com", "Rival")

	// Without a token nothing works.
	for _, p := range []string{"/projects", "/project-templates", "/artifacts", "/artifact-kinds"} {
		if c := e.status("GET", api1+p, "", nil); c != 401 {
			t.Errorf("GET %s without token = %d", p, c)
		}
	}
	// Viewer: reads, never creates.
	if c := e.status("GET", api1+"/projects", viewer, nil); c != 200 {
		t.Fatalf("viewer lists projects = %d", c)
	}
	if c := e.status("POST", api1+"/projects/draft", viewer, map[string]any{"goal": "x"}); c != 403 {
		t.Fatalf("viewer drafting = %d", c)
	}
	if c := e.status("POST", api1+"/artifacts", viewer, map[string]any{"kind": "doc", "title": "x"}); c != 403 {
		t.Fatalf("viewer creating an artifact = %d", c)
	}
	// Member: drafts and launches, but cannot pause, change the budget or decide.
	pid := str(e.json("POST", api1+"/projects/draft", member, map[string]any{"goal": "x", "template_id": "tpl-generic"}, 201), "project_id")
	e.json("POST", api1+"/projects/"+pid+"/launch", member, map[string]any{"approved_budget_usd": 5}, 202)
	for _, c := range []struct{ method, path string }{{"POST", "/projects/" + pid + "/pause"}, {"POST", "/projects/" + pid + "/control"}, {"PUT", "/projects/" + pid + "/budget"}, {"POST", "/approvals/batch"}} {
		if got := e.status(c.method, api1+c.path, member, map[string]any{"action": "pause", "budget_usd": 9}); got != 403 {
			t.Errorf("member %s %s = %d, want 403", c.method, c.path, got)
		}
	}
	e.json("POST", api1+"/projects/"+pid+"/pause", admin, nil, 200)
	e.json("POST", api1+"/projects/"+pid+"/resume", admin, nil, 200)
	e.json("POST", api1+"/projects/"+pid+"/cancel", admin, nil, 200)
	// Another organization sees nothing of it.
	if c := e.status("GET", api1+"/projects/"+pid, rival.AccessToken, nil); c != 404 {
		t.Fatalf("rival reads the project = %d", c)
	}
	if l := e.list(api1+"/projects", rival.AccessToken); len(l) != 0 {
		t.Fatalf("rival lists %d projects", len(l))
	}
	if c := e.status("POST", api1+"/projects/"+pid+"/cancel", rival.AccessToken, nil); c != 404 {
		t.Fatalf("rival cancels = %d", c)
	}

	// Artifacts: member creates and edits; approving needs the approve permission.
	art := e.json("POST", api1+"/artifacts", member, map[string]any{"kind": "doc", "title": "Contrato"}, 201)
	aid := str(art, "id")
	if c := e.status("PATCH", api1+"/artifacts/"+aid, member, map[string]any{"status": "approved"}); c != 403 {
		t.Fatalf("member approving = %d", c)
	}
	if got := e.json("PATCH", api1+"/artifacts/"+aid, admin, map[string]any{"status": "approved"}, 200); got["locked"] != true {
		t.Fatalf("admin approving: %v", got)
	}
	if c := e.status("POST", api1+"/artifacts/"+aid+"/versions", member, map[string]any{"base_version": 1, "content": art["content"]}); c == 201 {
		t.Fatal("a locked artifact accepted a version")
	}
	if c := e.status("PUT", api1+"/artifacts/"+aid+"/attachments/legal", admin, map[string]any{"mode": "approve"}); c != 400 {
		t.Fatalf("approve attachment mode = %d", c)
	}
	if c := e.status("PUT", api1+"/artifacts/"+aid+"/attachments/legal", viewer, map[string]any{"mode": "read"}); c != 403 {
		t.Fatalf("viewer attaching = %d", c)
	}
	if c := e.status("GET", api1+"/artifacts/"+aid, rival.AccessToken, nil); c != 404 {
		t.Fatalf("rival reads the artifact = %d", c)
	}
	if l := e.list(api1+"/artifacts", rival.AccessToken); len(l) != 0 {
		t.Fatalf("rival lists %d artifacts", len(l))
	}
	if c := e.status("POST", api1+"/artifacts/"+aid+"/versions", rival.AccessToken, map[string]any{"base_version": 1, "content": map[string]any{}}); c != 404 {
		t.Fatalf("rival writes = %d", c)
	}
	if c := e.status("GET", api1+"/projects/"+pid+"/workspace", rival.AccessToken, nil); c != 404 {
		t.Fatalf("rival reads the workspace = %d", c)
	}
}

func TestArtifactsOverHTTP(t *testing.T) {
	e := newEnv(t, opts{ws: true})
	if kinds := e.list(api1+"/artifact-kinds?agent_id=accounting", ""); len(kinds) != 9 || kinds[0]["kind"] != "sheet" || kinds[0]["suggested"] != true {
		t.Fatalf("kinds: %v", kinds)
	}
	if tpls := e.list(api1+"/artifact-templates?agent_id=legal&kind=doc", ""); len(tpls) == 0 || tpls[0]["id"] != "service_contract" {
		t.Fatalf("templates: %v", tpls)
	}
	sheet := func(cells string) json.RawMessage {
		return json.RawMessage(`{"schema":"aiw.sheet/1","sheets":[{"id":"s1","name":"Margen","cells":` + cells + `}]}`)
	}
	a := e.json("POST", api1+"/artifacts", "", map[string]any{"kind": "sheet", "title": "Margen", "content": sheet(`{"B2":{"v":10},"B3":{"v":20}}`)}, 201)
	id := str(a, "id")
	if a["head_version"].(float64) != 1 || a["version"].(float64) != 1 || a["content"] == nil {
		t.Fatalf("created: %v", a)
	}
	if c := e.status("POST", api1+"/artifacts", "", map[string]any{"kind": "sheet", "content": json.RawMessage(`{"schema":"aiw.sheet/1","sheets":[{"id":"s1","name":"M","cells":{"A1":{"f":"=HYPERLINK(\"x\")"}}}]}`)}); c != 400 {
		t.Fatalf("dangerous formula = %d", c)
	}
	// The list returns metadata only; the content comes with GET /artifacts/{id}.
	items := e.list(api1+"/artifacts", "")
	if len(items) != 1 || items[0]["content"] != nil {
		t.Fatalf("list: %v", items)
	}
	// Versions: a save, a stale disjoint save (merged) and a stale conflicting one (409 with the unit).
	v2 := e.json("POST", api1+"/artifacts/"+id+"/versions", "", map[string]any{"base_version": 1, "content": sheet(`{"B2":{"v":11},"B3":{"v":20}}`)}, 201)
	if v2["version"].(float64) != 2 || v2["merged"] != false {
		t.Fatalf("v2: %v", v2)
	}
	v3 := e.json("POST", api1+"/artifacts/"+id+"/versions", "", map[string]any{"base_version": 1, "content": sheet(`{"B2":{"v":10},"B3":{"v":21}}`)}, 201)
	if v3["version"].(float64) != 3 || v3["merged"] != true || v3["content"] == nil {
		t.Fatalf("merged save: %v", v3)
	}
	conflict := e.json("POST", api1+"/artifacts/"+id+"/versions", "", map[string]any{"base_version": 1, "content": sheet(`{"B2":{"v":99},"B3":{"v":20}}`)}, 409)
	cs, _ := conflict["conflicts"].([]any)
	if conflict["code"] != "conflict" || conflict["head_version"].(float64) != 3 || len(cs) != 1 || cs[0].(map[string]any)["unit"] != "Margen!B2" {
		t.Fatalf("conflict body: %v", conflict)
	}
	vs := e.list(api1+"/artifacts/"+id+"/versions", "")
	if len(vs) != 3 || vs[0]["version"].(float64) != 3 {
		t.Fatalf("versions: %v", vs)
	}
	if old := e.json("GET", api1+"/artifacts/"+id+"?version=1", "", nil, 200); old["version"].(float64) != 1 {
		t.Fatalf("old version: %v", old["version"])
	}
	restored := e.json("POST", api1+"/artifacts/"+id+"/restore", "", map[string]any{"version": 1}, 200)
	if restored["head_version"].(float64) != 4 {
		t.Fatalf("restore: %v", restored)
	}
	// Attachments: none/read/propose/edit; never approve.
	for _, mode := range []string{"read", "propose", "edit", "none"} {
		got := e.json("PUT", api1+"/artifacts/"+id+"/attachments/accounting", "", map[string]any{"mode": mode}, 200)
		atts, _ := got["attachments"].([]any)
		if (mode == "none") != (len(atts) == 0) {
			t.Fatalf("attachments after %s: %v", mode, atts)
		}
	}
	if c := e.status("PUT", api1+"/artifacts/"+id+"/attachments/accounting", "", map[string]any{"mode": "approve"}); c != 400 {
		t.Fatalf("approve mode = %d", c)
	}
	if c := e.status("PUT", api1+"/artifacts/"+id+"/attachments/ghost", "", map[string]any{"mode": "read"}); c != 404 {
		t.Fatalf("unknown agent = %d", c)
	}
	// Links and refresh.
	b := e.json("POST", api1+"/artifacts", "", map[string]any{"kind": "sheet", "title": "Dep", "content": json.RawMessage(
		`{"schema":"aiw.sheet/1","imports":[{"alias":"M","artifact_id":"` + id + `"}],"sheets":[{"id":"s1","name":"D","cells":{"A1":{"f":"=AIW_REF(\"M\",\"Margen!B2\")","v":0}}}]}`)}, 201)
	links := e.list(api1+"/artifacts/"+str(b, "id")+"/links?direction=both", "")
	if len(links) != 1 || links[0]["relation"] != "source_of" || links[0]["to"] != id {
		t.Fatalf("links: %v", links)
	}
	rf := e.json("POST", api1+"/artifacts/"+str(b, "id")+"/refresh-dependencies", "", nil, 200)
	if rf["refreshed"] != false { // the auto recalculation already ran when the source changed
		if rf["refreshed"] != true {
			t.Fatalf("refresh: %v", rf)
		}
	}
	if full := e.json("GET", api1+"/artifacts/"+str(b, "id"), "", nil, 200); !strings.Contains(full["content"].(map[string]any)["sheets"].([]any)[0].(map[string]any)["cells"].(map[string]any)["A1"].(map[string]any)["f"].(string), "AIW_REF") {
		t.Fatalf("content: %v", full["content"])
	}
	e.json("POST", api1+"/artifacts/"+id+"/links", "", map[string]any{"to": str(b, "id"), "relation": "refers_to"}, 201)
	if c := e.status("POST", api1+"/artifacts/"+id+"/links", "", map[string]any{"to": str(b, "id"), "relation": "refers_to"}); c != 409 {
		t.Fatalf("duplicate link = %d", c)
	}
	// Ask an agent: a real request through the orchestrator.
	ask := e.json("POST", api1+"/artifacts/"+id+"/ask", "", map[string]any{"agent_id": "legal", "mode": "review_my_changes"}, 202)
	if str(ask, "request_id") == "" {
		t.Fatalf("ask: %v", ask)
	}
	e.json("GET", api1+"/requests/"+str(ask, "request_id"), "", nil, 200)
	// Archive (DELETE): the artifact leaves the list but its history stays.
	if c := e.status("DELETE", api1+"/artifacts/"+str(b, "id"), "", nil); c != 204 {
		t.Fatalf("archive = %d", c)
	}
	if l := e.list(api1+"/artifacts", ""); len(l) != 1 {
		t.Fatalf("list after archive: %d", len(l))
	}
	if c := e.status("GET", api1+"/artifacts/nope", "", nil); c != 404 {
		t.Fatalf("unknown artifact = %d", c)
	}
	if c := e.status("POST", api1+"/artifacts/"+id+"/versions", "", []byte(`{"base_version":`)); c != http.StatusBadRequest {
		t.Fatalf("malformed body = %d", c)
	}
}

func TestArtifactExportPermissionsAndTenants(t *testing.T) {
	e := newEnv(t, opts{auth: true, ws: true})
	owner := e.register("owner-x@example.com", "OwnerX")
	viewer := e.member(owner, "viewer-x@example.com", auth.RoleViewer)
	member := e.member(owner, "member-x@example.com", auth.RoleMember)
	rival := e.register("rival-x@example.com", "RivalX")

	art := e.json("POST", api1+"/artifacts", member, map[string]any{"kind": "sheet", "title": "Margen: Q1",
		"content": map[string]any{"schema": "aiw.sheet/1", "sheets": []any{map[string]any{"id": "s1", "name": "Margen", "rows": 10, "cols": 4,
			"cells": map[string]any{"A1": map[string]any{"v": "=1+1"}}}}}}, 201)
	id := str(art, "id")
	path := api1 + "/artifacts/" + id + "/export?format=xlsx"

	resp, body := e.do("GET", path, member, nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "spreadsheetml.sheet") ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") || len(body) < 100 || string(body[:2]) != "PK" {
		t.Fatalf("member export = %d %v", resp.StatusCode, resp.Header)
	}
	if c := e.status("GET", path, "", nil); c != 401 {
		t.Errorf("no token = %d", c)
	}
	if c := e.status("GET", path, viewer, nil); c != 403 {
		t.Errorf("viewer export = %d, want 403", c)
	}
	if c := e.status("GET", path, rival.AccessToken, nil); c != 404 {
		t.Errorf("other tenant export = %d, want 404", c)
	}
	if c := e.status("GET", api1+"/artifacts/"+id+"/export?format=docx", member, nil); c != 400 {
		t.Errorf("sheet as docx = %d, want 400", c)
	}
}

func TestArtifactCommentsAndProposalsOverHTTP(t *testing.T) {
	e := newEnv(t, opts{auth: true, ws: true})
	owner := e.register("owner-p@example.com", "OwnerP")
	viewer := e.member(owner, "viewer-p@example.com", auth.RoleViewer)
	member := e.member(owner, "member-p@example.com", auth.RoleMember)
	admin := e.member(owner, "admin-p@example.com", auth.RoleAdmin)
	rival := e.register("rival-p@example.com", "RivalP")

	sheet := func(v int) map[string]any {
		return map[string]any{"schema": "aiw.sheet/1", "sheets": []any{map[string]any{"id": "s1", "name": "S", "rows": 10, "cols": 4,
			"cells": map[string]any{"A1": map[string]any{"v": v}}}}}
	}
	id := str(e.json("POST", api1+"/artifacts", member, map[string]any{"kind": "sheet", "title": "T", "content": sheet(1)}, 201), "id")
	base := api1 + "/artifacts/" + id

	if c := e.status("POST", base+"/comments", viewer, map[string]any{"body": "x"}); c != 403 {
		t.Errorf("viewer comment = %d", c)
	}
	cm := e.json("POST", base+"/comments", member, map[string]any{"body": "Revisar", "anchor": map[string]any{"sheet": "S", "range": "A1"}}, 201)
	e.json("PATCH", base+"/comments/"+str(cm, "id"), member, map[string]any{"resolved": true}, 200)
	if l := e.list(base+"/comments", viewer); len(l) != 1 {
		t.Fatalf("comments: %v", l)
	}
	if c := e.status("GET", base+"/comments", rival.AccessToken, nil); c != 404 {
		t.Errorf("rival comments = %d", c)
	}

	p := e.json("POST", base+"/proposals", member, map[string]any{"base_version": 1, "content": sheet(7), "summary": "A1 a 7"}, 201)
	pid := str(p, "id")
	if c := e.status("POST", base+"/proposals/"+pid+"/decision", member, map[string]any{"decision": "accept"}); c != 403 {
		t.Errorf("member decision = %d, want 403", c)
	}
	if c := e.status("POST", base+"/proposals/"+pid+"/decision", rival.AccessToken, map[string]any{"decision": "accept"}); c != 404 {
		t.Errorf("rival decision = %d, want 404", c)
	}
	done := e.json("POST", base+"/proposals/"+pid+"/decision", admin, map[string]any{"decision": "accept"}, 200)
	if str(done, "status") != "accepted" {
		t.Fatalf("decision: %v", done)
	}
	if got := e.json("GET", base, viewer, nil, 200); got["head_version"].(float64) != 2 || got["pending_proposals"].(float64) != 0 {
		t.Fatalf("artifact: %v", got)
	}
}
