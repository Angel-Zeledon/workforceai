package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/projects"
)

// Integration tests of migrations 270 (projects) and 280 (artifacts); see
// store_rls_test.go for how to run them (TEST_DATABASE_URL).

func TestProjectStoreRoundTripAndTenantIsolation(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	ps := &ProjectStore{S: env.Store}

	now := time.Now().UTC()
	rec := projects.Record{ID: "prj-1", OrgID: "org-a", Name: "Cierre", Goal: "cierre", Status: projects.StatusDraft, Control: projects.ControlActive,
		Nodes: []projects.NodeDef{{ID: "prj-1:n1", Key: "n1", Kind: projects.KindTask, Title: "Uno", DependsOn: []string{}}}, CreatedAt: now, RequestID: "req-1"}
	if err := ps.Put(ctx, rec); err != nil {
		t.Fatal(err)
	}
	rec.Status = projects.StatusRunning
	if err := ps.Put(ctx, rec); err != nil { // upsert
		t.Fatal(err)
	}
	got, err := ps.Get(ctx, "org-a", "prj-1")
	if err != nil || got.Status != projects.StatusRunning || len(got.Nodes) != 1 || got.Nodes[0].Title != "Uno" {
		t.Fatalf("round trip: %v %+v", err, got)
	}
	if by, err := ps.ByRequest(ctx, "org-a", "req-1"); err != nil || by.ID != "prj-1" {
		t.Fatalf("by request: %v", err)
	}
	if _, err := ps.Get(ctx, "org-b", "prj-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant read: %v", err)
	}
	if l, _ := ps.List(ctx, "org-b"); len(l) != 0 {
		t.Fatalf("cross-tenant list: %v", l)
	}
	// The same id in another organization is a different project.
	other := rec
	other.OrgID, other.Name = "org-b", "Otro"
	if err := ps.Put(ctx, other); err != nil {
		t.Fatal(err)
	}
	if a, _ := ps.Get(ctx, "org-a", "prj-1"); a.Name != "Cierre" {
		t.Fatal("a write of org-b changed org-a")
	}
	tpl := projects.Template{ID: "tpl-x", Key: "custom_1", Version: 1, Name: "X", Params: []projects.TemplateParam{}}
	if err := ps.PutTemplate(ctx, "org-a", tpl); err != nil {
		t.Fatal(err)
	}
	if l, _ := ps.ListTemplates(ctx, "org-a"); len(l) != 1 {
		t.Fatalf("templates: %v", l)
	}
	if l, _ := ps.ListTemplates(ctx, "org-b"); len(l) != 0 {
		t.Fatalf("templates of another org: %v", l)
	}
}

func artifactFixture(org, id string) (artifacts.Stored, artifacts.Version) {
	now := time.Now().UTC()
	content := []byte(`{"schema":"aiw.doc/1","doc":{"type":"doc","content":[]}}`)
	m := artifacts.Meta{ID: id, Kind: artifacts.KindDoc, Title: "Doc", Status: "draft", HeadVersion: 1, CreatedBy: artifacts.ActorRef{Kind: "user", ID: "u1"},
		LastAuthor: artifacts.ActorRef{Kind: "user", ID: "u1"}, Progress: 100, BuildState: "done", Locale: "es", Attachments: []artifacts.Attachment{{AgentID: "legal", Mode: "read"}},
		CreatedAt: now, UpdatedAt: now, SizeBytes: len(content)}
	return artifacts.Stored{Meta: m, Content: content}, artifacts.Version{VersionInfo: artifacts.VersionInfo{ArtifactID: id, Version: 1, Author: m.CreatedBy, Source: "create", CreatedAt: now}, Content: content}
}

func TestArtifactStoreVersionsCompareAndSwapAndIsolation(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	as := &ArtifactStore{S: env.Store}

	st, v1 := artifactFixture("org-a", "art_1")
	if err := as.Create(ctx, "org-a", st, v1); err != nil {
		t.Fatal(err)
	}
	got, err := as.Get(ctx, "org-a", "art_1")
	if err != nil || got.Meta.Title != "Doc" || got.Meta.Attachments[0].AgentID != "legal" || got.Meta.Kind != artifacts.KindDoc {
		t.Fatalf("round trip: %v %+v", err, got.Meta)
	}
	// Commit: compare-and-swap on the head version.
	st.Meta.HeadVersion = 2
	v2 := v1
	v2.Version = 2
	if err := as.Commit(ctx, "org-a", st, v2, 1); err != nil {
		t.Fatal(err)
	}
	st.Meta.HeadVersion = 3
	v3 := v1
	v3.Version = 3
	if err := as.Commit(ctx, "org-a", st, v3, 1); !errors.Is(err, domain.ErrConflict) { // stale expected head
		t.Fatalf("stale commit must conflict: %v", err)
	}
	if vs, _ := as.ListVersions(ctx, "org-a", "art_1"); len(vs) != 2 || vs[0].Version != 2 {
		t.Fatalf("versions: %+v", vs)
	}
	// Versions are immutable for the application role.
	if err := as.S.exec(ctx, "org-a", `UPDATE artifact_versions SET summary='x' WHERE artifact_id='art_1'`); err == nil {
		t.Fatal("artifact_versions must be append-only")
	}
	// Tenant isolation.
	if _, err := as.Get(ctx, "org-b", "art_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant read: %v", err)
	}
	if err := as.UpdateMeta(ctx, "org-b", st.Meta); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant update: %v", err)
	}
	if l, _ := as.List(ctx, "org-b"); len(l) != 0 {
		t.Fatalf("cross-tenant list: %v", l)
	}
	// Links keep their synced version when the auto links are replaced.
	l := artifacts.Link{ID: "lnk_1", From: "art_1", To: "art_2", Relation: "source_of", SyncedVersion: 4, Auto: true}
	if err := as.ReplaceAutoLinks(ctx, "org-a", "art_1", []artifacts.Link{l}); err != nil {
		t.Fatal(err)
	}
	l.ID, l.SyncedVersion = "lnk_2", 0
	if err := as.ReplaceAutoLinks(ctx, "org-a", "art_1", []artifacts.Link{l}); err != nil {
		t.Fatal(err)
	}
	links, _ := as.ListLinks(ctx, "org-a", "art_1")
	if len(links) != 1 || links[0].SyncedVersion != 4 {
		t.Fatalf("links: %+v", links)
	}
	if l, _ := as.ListLinks(ctx, "org-b", "art_1"); len(l) != 0 {
		t.Fatal("links leaked across tenants")
	}
}

// Migration 500: template history, delete with cascade, and isolation.
func TestProjectTemplateVersionsDeleteAndIsolation(t *testing.T) {
	env := testStore(t)
	ctx := context.Background()
	seedOrg(t, env.Store, "org-a")
	seedOrg(t, env.Store, "org-b")
	ps := &ProjectStore{S: env.Store}
	now := time.Now().UTC()

	tpl := projects.Template{ID: "tpl-h", Key: "custom_h", Version: 1, Name: "H", Params: []projects.TemplateParam{{Key: "c", Type: "list", ItemName: "country", Default: "A, B", Max: 5}},
		Objectives: []projects.TemplateObjective{{Key: "o1", Title: "O", Workflows: []projects.TemplateWorkflow{{Key: "w1", Title: "{country}", Repeat: []string{"c"}, Nodes: []projects.TemplateNode{{Key: "n1", Title: "T", Agent: "legal"}}}}}},
		CreatedBy:  "u1", CreatedAt: &now}
	if err := ps.PutTemplate(ctx, "org-a", tpl); err != nil {
		t.Fatal(err)
	}
	got, err := ps.GetTemplate(ctx, "org-a", "tpl-h")
	if err != nil || got.Objectives[0].Workflows[0].Repeat[0] != "c" || got.Params[0].Type != "list" || got.CreatedBy != "u1" {
		t.Fatalf("get: %v %+v", err, got)
	}
	if _, err := ps.GetTemplate(ctx, "org-b", "tpl-h"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant get: %v", err)
	}
	for v := 1; v <= 3; v++ {
		cp := tpl
		cp.Version = v
		cp.Name = "H" + string(rune('0'+v))
		if err := ps.PutTemplateVersion(ctx, "org-a", projects.TemplateVersion{TemplateID: "tpl-h", Version: v, Name: cp.Name, SavedBy: "u1", SavedAt: now, Note: "n", Template: &cp}); err != nil {
			t.Fatal(err)
		}
	}
	// idempotent by version
	if err := ps.PutTemplateVersion(ctx, "org-a", projects.TemplateVersion{TemplateID: "tpl-h", Version: 2, SavedAt: now, Template: &tpl}); err != nil {
		t.Fatal(err)
	}
	vs, err := ps.ListTemplateVersions(ctx, "org-a", "tpl-h")
	if err != nil || len(vs) != 3 || vs[0].Version != 3 || vs[2].Version != 1 || vs[0].Template == nil || vs[0].Template.Name != "H3" || vs[0].Note != "n" {
		t.Fatalf("versions: %v %+v", err, vs)
	}
	if vs, _ := ps.ListTemplateVersions(ctx, "org-b", "tpl-h"); len(vs) != 0 {
		t.Fatalf("another org sees the history: %v", vs)
	}
	// org-b cannot write a version for org-a's template (RLS + foreign key)
	if err := ps.PutTemplateVersion(ctx, "org-b", projects.TemplateVersion{TemplateID: "tpl-h", Version: 9, SavedAt: now, Template: &tpl}); err == nil {
		t.Fatal("a version for a template of another organization was accepted")
	}
	if err := ps.DeleteTemplate(ctx, "org-b", "tpl-h"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant delete: %v", err)
	}
	if err := ps.DeleteTemplate(ctx, "org-a", "tpl-h"); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.GetTemplate(ctx, "org-a", "tpl-h"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
	if vs, _ := ps.ListTemplateVersions(ctx, "org-a", "tpl-h"); len(vs) != 0 {
		t.Fatalf("the history must go with the template: %v", vs)
	}
	if err := ps.DeleteTemplate(ctx, "org-a", "tpl-h"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}
