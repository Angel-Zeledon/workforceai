package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/portal"
	"aiworkforce/backend/internal/projects"
)

func attachPortal(d *api.Deps, rec *application.Recorder) {
	ps, as := d.Projects, d.Artifacts
	d.Portal = portal.New(portal.Config{Store: portal.NewMemStore(), OrgID: d.Cfg.OrgID,
		Project: func(ctx context.Context, org, id string) (projects.Detail, error) {
			return ps.Get(application.WithOrg(ctx, org), id)
		},
		Deliverables: func(ctx context.Context, org, pid string) ([]artifacts.Meta, error) {
			return as.List(application.WithOrg(ctx, org), artifacts.Filter{ProjectID: pid})
		},
		Audit: func(ctx context.Context, org, actor, action, id string, det map[string]any) {
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: "portal_link", EntityID: id, Details: det})
		}})
	d.PortalLimiter = auth.NewMemoryLimiter()
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for x := 0; x < 40; x++ {
		for y := 0; y < 20; y++ {
			img.Set(x, y, color.RGBA{R: 10, G: 95, B: 255, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// B13: share links over HTTP, the public page and its security headers.
func TestClientPortalOverHTTP(t *testing.T) {
	e := newEnv(t, opts{auth: true, portal: true})
	owner := e.register("owner-portal@example.com", "PortalCo")
	admin := e.member(owner, "admin-portal@example.com", auth.RoleAdmin)
	member := e.member(owner, "member-portal@example.com", auth.RoleMember)
	rival := e.register("rival-portal@example.com", "RivalPortal")

	const goal = "SECRET-GOAL do the monthly close for López"
	pid := str(e.json("POST", api1+"/projects/draft", owner.AccessToken, map[string]any{"goal": goal, "template_id": "tpl-generic"}, 201), "project_id")
	links := api1 + "/projects/" + pid + "/portal-links"

	// A deliverable of the project, approved by a human.
	art := e.json("POST", api1+"/artifacts", owner.AccessToken, map[string]any{"kind": "doc", "title": "Informe para el cliente"}, 201)
	artID := str(art, "id")
	e.json("PATCH", api1+"/artifacts/"+artID, owner.AccessToken, map[string]any{"project_id": pid, "status": "approved"}, 200)

	// Only owners/admins manage links.
	if s := e.status("POST", links, member, map[string]any{}); s != 403 {
		t.Fatalf("member create = %d", s)
	}
	if s := e.status("GET", links, member, nil); s != 403 {
		t.Fatalf("member list = %d", s)
	}
	if s := e.status("GET", api1+"/projects/"+pid+"/portal-links", rival.AccessToken, nil); s != 404 {
		t.Fatalf("another organization sees nothing = %d", s)
	}
	if s := e.status("POST", links, admin, map[string]any{"deliverable_ids": []string{"not-of-this-project"}}); s != 400 {
		t.Fatalf("foreign deliverable = %d", s)
	}
	cands := e.list(links+"/candidates", admin)
	if len(cands) != 1 || cands[0]["id"] != artID || cands[0]["completed"] != true {
		t.Fatalf("candidates: %v", cands)
	}

	created := e.json("POST", links, admin, map[string]any{"label": "Ferretería López", "title": "Cierre de octubre", "expires_in_days": 30, "deliverable_ids": []string{artID}}, 201)
	token, linkID := str(created, "token"), str(created, "id")
	if len(token) != 43 || created["status"] != "active" || created["token_hash"] != nil {
		t.Fatalf("created: %v", created)
	}
	for _, l := range e.list(links, admin) {
		if _, has := l["token"]; has {
			t.Fatal("the token is only returned at creation")
		}
	}

	// Branding.
	e.json("PUT", api1+"/portal/branding", admin, map[string]any{"display_name": "Despacho Pérez", "accent_color": "#0B5FFF"}, 200)
	if s := e.status("PUT", api1+"/portal/branding", member, map[string]any{"display_name": "x"}); s != 403 {
		t.Fatalf("member branding = %d", s)
	}
	if s := e.status("PUT", api1+"/portal/branding", admin, map[string]any{"accent_color": "red"}); s != 400 {
		t.Fatalf("bad color = %d", s)
	}
	resp, raw := e.do("PUT", api1+"/portal/branding/logo", admin, tinyPNG(t), map[string]string{"Content-Type": "image/png"})
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"has_logo":true`) {
		t.Fatalf("logo upload = %d %s", resp.StatusCode, raw)
	}
	if resp, _ := e.do("PUT", api1+"/portal/branding/logo", admin, []byte("<svg onload=alert(1)/>"), map[string]string{"Content-Type": "image/svg+xml"}); resp.StatusCode != 400 {
		t.Fatalf("svg logo = %d", resp.StatusCode)
	}

	// The public page: no session, token in the body, strict headers.
	view := func(tok string) (*http.Response, []byte) {
		return e.do("POST", api1+"/public/portal/view", "", map[string]string{"token": tok}, nil)
	}
	resp, raw = view(token)
	if resp.StatusCode != 200 {
		t.Fatalf("public view = %d %s", resp.StatusCode, raw)
	}
	for h, want := range map[string]string{"X-Robots-Tag": "noindex", "Referrer-Policy": "no-referrer", "Cache-Control": "no-store",
		"Content-Security-Policy": "default-src 'none'", "X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff"} {
		if !strings.Contains(resp.Header.Get(h), want) {
			t.Errorf("header %s = %q, want %q", h, resp.Header.Get(h), want)
		}
	}
	// Note: task titles are shown as written in the plan; the generic template embeds the request text in
	// them, so the goal itself is not on the forbidden list here (the firm previews the page before sharing).
	doc := string(raw)
	for _, bad := range []string{pid, owner.OrgID, owner.User.ID, artID, linkID, "Ferretería", "cost", "budget", "agent", "approval", token} {
		if strings.Contains(doc, bad) {
			t.Errorf("the public view leaks %q: %s", bad, doc)
		}
	}
	var v map[string]any
	_ = json.Unmarshal(raw, &v)
	firm := v["firm"].(map[string]any)
	if firm["name"] != "Despacho Pérez" || firm["accent_color"] != "#0b5fff" || firm["has_logo"] != true {
		t.Fatalf("firm: %v", firm)
	}
	if p := v["project"].(map[string]any); p["status"] != "planning" || p["title"] != "Cierre de octubre" || p["title_key"] != nil || p["tasks_total"].(float64) == 0 {
		t.Fatalf("project: %v", p)
	}
	if d := v["deliverables"].([]any); len(d) != 1 || d[0].(map[string]any)["title"] != "Informe para el cliente" {
		t.Fatalf("deliverables: %v", d)
	}
	resp, raw = e.do("POST", api1+"/public/portal/logo", "", map[string]string{"token": token}, nil)
	if ct := resp.Header.Get("Content-Type"); resp.StatusCode != 200 || (ct != "image/png" && ct != "image/jpeg") || len(raw) == 0 {
		t.Fatalf("public logo = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	// A GET with the token in the URL is not a route (tokens never travel in URLs).
	if resp, _ := e.do("GET", api1+"/public/portal/view?token="+token, "", nil, nil); resp.StatusCode == 200 {
		t.Fatal("GET must not serve the view")
	}
	if resp, raw := view("garbage"); resp.StatusCode != 404 || !strings.Contains(string(raw), "link_unavailable") {
		t.Fatalf("garbage token = %d %s", resp.StatusCode, raw)
	}

	// Revocation.
	if s := e.status("DELETE", links+"/"+linkID, member, nil); s != 403 {
		t.Fatalf("member revoke = %d", s)
	}
	if s := e.status("DELETE", api1+"/projects/"+pid+"/portal-links/"+linkID, rival.AccessToken, nil); s != 404 {
		t.Fatalf("rival revoke = %d", s)
	}
	if r := e.json("DELETE", links+"/"+linkID, admin, nil, 200); r["status"] != "revoked" {
		t.Fatalf("revoke: %v", r)
	}
	if resp, raw := view(token); resp.StatusCode != 404 || !strings.Contains(string(raw), "link_unavailable") {
		t.Fatalf("revoked token = %d %s", resp.StatusCode, raw)
	}

	// Audit: creation, first view and revocation, never the token.
	_, raw = e.do("GET", "/api/v1/audit?limit=200", owner.AccessToken, nil, nil)
	for _, a := range []string{"portal_link.created", "portal_link.first_viewed", "portal_link.revoked", "portal_branding.updated", "portal_branding.logo_set"} {
		if !strings.Contains(string(raw), a) {
			t.Errorf("audit lacks %s", a)
		}
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("the token reached the audit trail")
	}
}

func TestClientPortalPublicEndpointIsRateLimited(t *testing.T) {
	e := newEnv(t, opts{portal: true})
	limited := false
	for i := 0; i < 80; i++ {
		resp, _ := e.do("POST", api1+"/public/portal/view", "", map[string]string{"token": "x"}, nil)
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("the public endpoint must be rate limited")
	}
}
