package api_test

import (
	"context"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/projects"
)

// The share links of a project of a private office are that office's: an admin who is not in it (org:manage is all
// the portal asks) neither lists, creates, edits nor revokes them, nor lists what it could share; every route
// answers 404 as for a project that does not exist. An admin of the office, and everybody for General's projects,
// are not affected.
func TestOfficeLeakPortalLinksAPI(t *testing.T) {
	var ps *projects.Service
	e, owner, _, _, _, _, _, org := officesWorldSealed(t, func(x *api.Deps) {
		attachPortal(x, x.Rec)
		ps = x.Projects
	})
	boss := e.member(&auth.Session{AccessToken: owner, OrgID: org, User: auth.UserView{ID: e.userID(owner)}}, "boss-pt@example.com", auth.RoleAdmin)
	legal := e.json("POST", api1+"/org/departments", owner, map[string]any{"name": "Legal", "head": map[string]any{"kind": "person", "id": e.userID(boss)}}, 201)["id"].(string)
	e.json("POST", api1+"/offices", owner, map[string]any{"department_id": legal}, 201)
	e.json("PUT", api1+"/offices/"+legal, owner, map[string]any{"visibility": "private"}, 200)

	ctx := application.WithOrg(context.Background(), org)
	mk := func(office string) string {
		t.Helper()
		rec, err := ps.CreateDraft(application.WithRequestOffice(ctx, office), projects.NewProject{Goal: "SECRETO cierre del mes", TemplateID: "tpl-generic"})
		if err != nil {
			t.Fatal(err)
		}
		return rec.ID
	}
	secret, open := mk(legal), mk("")

	links := func(id string) string { return api1 + "/projects/" + id + "/portal-links" }
	// the admin of the office manages the links (so the 404s below are not for nothing)
	made := e.json("POST", links(secret), boss, map[string]any{"label": "cliente"}, 201)
	lid, _ := made["id"].(string)
	if lid == "" {
		t.Fatalf("no link id: %v", made)
	}
	if s := e.status("GET", links(secret), boss, nil); s != 200 {
		t.Fatalf("the admin of the office lists its links = %d", s)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", links(secret), nil}, {"POST", links(secret), map[string]any{"label": "x"}}, {"GET", links(secret) + "/candidates", nil},
		{"PUT", links(secret) + "/" + lid, map[string]any{"label": "x"}}, {"DELETE", links(secret) + "/" + lid, nil}} {
		if s := e.status(c.method, c.path, owner, c.body); s != 404 {
			t.Fatalf("admin not in the office: %s %s = %d, want 404", c.method, c.path, s)
		}
	}
	// General's project is not sealed
	for _, p := range []string{links(open), links(open) + "/candidates"} {
		if s := e.status("GET", p, owner, nil); s != 200 {
			t.Fatalf("GET %s = %d", p, s)
		}
	}
	if s := e.status("POST", links(open), owner, map[string]any{"label": "x"}); s != 201 {
		t.Fatalf("POST a link of General's project = %d", s)
	}
}
