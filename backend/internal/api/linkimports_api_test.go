package api_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/linkimports"
	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/vault"
)

type linkRT func(*http.Request) (*http.Response, error)

func (f linkRT) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Import from a link over HTTP: roles (admin-only writes), the sealed header never returned, the link's query
// string hidden from members, run now and its history, tenant isolation.
func TestLinkImportsOverHTTP(t *testing.T) {
	var sentKey string
	kw, err := vault.NewEnvKeyWrapper(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, opts{auth: true, deps: func(d *api.Deps) {
		dsvc := drive.New(drive.Config{Store: drive.NewMemStore(), Objects: objstore.NewMemory(), OrgID: d.Cfg.OrgID})
		d.LinkImports = linkimports.New(linkimports.Config{Store: linkimports.NewMemStore(), Drive: dsvc, OrgID: d.Cfg.OrgID,
			Sealer: vault.New(vault.NewMemRepo(), kw),
			Fetcher: &linkimports.Fetcher{Transport: linkRT(func(r *http.Request) (*http.Response, error) {
				sentKey = r.Header.Get("X-Api-Key")
				body := "sku,qty\nA1,3\n"
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/csv"}}, Body: io.NopCloser(strings.NewReader(body)),
					ContentLength: int64(len(body))}, nil
			})}})
	}})
	owner := e.register("owner-l@example.com", "OwnerL")
	admin := e.member(owner, "admin-l@example.com", auth.RoleAdmin)
	member := e.member(owner, "member-l@example.com", auth.RoleMember)
	viewer := e.member(owner, "viewer-l@example.com", auth.RoleViewer)
	rival := e.register("rival-l@example.com", "RivalL")

	if c := e.status("GET", api1+"/link-imports", "", nil); c != 401 {
		t.Fatalf("anonymous: %d", c)
	}
	body := map[string]any{"name": "Inventario", "url": "https://sheets.example.com/pub?key=tok123&output=csv", "schedule": "daily",
		"header": map[string]any{"name": "X-Api-Key", "value": "sk-live-abcdef-7788"}}
	if c := e.status("POST", api1+"/link-imports", member, body); c != 403 {
		t.Fatalf("a member cannot create imports: %d", c)
	}
	if c := e.status("POST", api1+"/link-imports", admin, map[string]any{"name": "x", "url": "https://127.0.0.1/x.csv"}); c != 400 {
		t.Fatalf("a private address is refused at creation: %d", c)
	}
	created := e.json("POST", api1+"/link-imports", admin, body, 201)
	raw, _ := json.Marshal(created)
	if strings.Contains(string(raw), "sk-live") || created["header_set"] != true || created["header_hint"] != "7788" {
		t.Fatalf("the header value is never returned: %s", raw)
	}
	id := created["id"].(string)

	// Members read (link query hidden); viewers do not.
	if c := e.status("GET", api1+"/link-imports", viewer, nil); c != 403 {
		t.Fatalf("viewer: %d", c)
	}
	got := e.json("GET", api1+"/link-imports/"+id, member, nil, 200)
	if strings.Contains(got["url"].(string), "tok123") || got["host"] != "sheets.example.com" {
		t.Fatalf("a member does not see the link's query string: %v", got["url"])
	}
	if full := e.json("GET", api1+"/link-imports/"+id, admin, nil, 200); !strings.Contains(full["url"].(string), "tok123") {
		t.Fatalf("an admin sees the full link: %v", full["url"])
	}
	listed := e.json("GET", api1+"/link-imports", owner.AccessToken, nil, 200)
	raw, _ = json.Marshal(listed)
	if len(listed["imports"].([]any)) != 1 || strings.Contains(string(raw), "sk-live") || strings.Contains(string(raw), "header_sealed") {
		t.Fatalf("list: %s", raw)
	}

	// Run now (admins only) and the history.
	if c := e.status("POST", api1+"/link-imports/"+id+"/run", member, nil); c != 403 {
		t.Fatalf("a member cannot run imports: %d", c)
	}
	res := e.json("POST", api1+"/link-imports/"+id+"/run", admin, nil, 200)
	run := res["run"].(map[string]any)
	if run["status"] != "ok" || !strings.HasPrefix(run["path"].(string), "/Shared/Imports/Links/inventario/") || sentKey != "sk-live-abcdef-7788" {
		t.Fatalf("run: %v (key sent: %q)", run, sentKey)
	}
	res = e.json("POST", api1+"/link-imports/"+id+"/run", admin, nil, 200)
	if res["run"].(map[string]any)["status"] != "unchanged" {
		t.Fatalf("same content: %v", res["run"])
	}
	if runs := e.list(api1+"/link-imports/"+id+"/runs", member); len(runs) != 2 || runs[0]["status"] != "unchanged" {
		t.Fatalf("history: %v", runs)
	}

	// Update: rename the header keeps the sealed value; pause.
	upd := e.json("PUT", api1+"/link-imports/"+id, admin, map[string]any{"enabled": false, "header": map[string]any{"name": "Authorization"}}, 200)
	if upd["enabled"] != false || upd["header_name"] != "Authorization" || upd["header_set"] != true {
		t.Fatalf("update: %v", upd)
	}
	if c := e.status("PUT", api1+"/link-imports/"+id, member, map[string]any{"enabled": true}); c != 403 {
		t.Fatalf("a member cannot edit: %d", c)
	}

	// Tenant isolation.
	if got := e.json("GET", api1+"/link-imports", rival.AccessToken, nil, 200); len(got["imports"].([]any)) != 0 {
		t.Fatalf("rival org sees imports: %v", got)
	}
	for _, m := range []string{"GET", "PUT", "DELETE"} {
		if c := e.status(m, api1+"/link-imports/"+id, rival.AccessToken, map[string]any{"name": "x"}); c != 404 {
			t.Fatalf("rival %s: %d", m, c)
		}
	}
	if c := e.status("POST", api1+"/link-imports/"+id+"/run", rival.AccessToken, nil); c != 404 {
		t.Fatalf("rival run: %d", c)
	}

	if c := e.status("DELETE", api1+"/link-imports/"+id, member, nil); c != 403 {
		t.Fatalf("a member cannot delete: %d", c)
	}
	if c := e.status("DELETE", api1+"/link-imports/"+id, admin, nil); c != 204 {
		t.Fatalf("delete: %d", c)
	}
	if c := e.status("GET", api1+"/link-imports/"+id, admin, nil); c != 404 {
		t.Fatalf("deleted: %d", c)
	}
}
