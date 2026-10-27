package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"aiworkforce/backend/internal/api"
	"aiworkforce/backend/internal/application"
	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/inboxes"
)

// inboxOpts mounts the data inboxes. gate is what the kill switch / read-only gate answers (mutable by the test).
type inboxOpts struct {
	mu    sync.Mutex
	gate  error
	drive *drive.Service // set by attachInboxes
}

func (o *inboxOpts) setGate(err error) { o.mu.Lock(); o.gate = err; o.mu.Unlock() }

func attachInboxes(d *api.Deps, rec *application.Recorder, o *inboxOpts) {
	o.drive = d.Drive
	d.Inboxes = inboxes.New(inboxes.Config{Store: inboxes.NewMemStore(), Drive: d.Drive, OrgID: d.Cfg.OrgID,
		Gate: func(context.Context, string) error { o.mu.Lock(); defer o.mu.Unlock(); return o.gate },
		Audit: func(ctx context.Context, org, actor, action, id string, det map[string]any) {
			rec.Audit(application.WithOrg(ctx, org), domain.AuditLog{Actor: actor, Action: action, Entity: "data_inbox", EntityID: id, Details: det})
		},
		Emit: func(ctx context.Context, org, typ string, p map[string]any) {
			rec.Emit(application.WithOrg(ctx, org), application.Action{Type: typ, Entity: "data_inbox", Payload: p, SkipAudit: true})
		}})
	d.InboxLimiter = auth.NewMemoryLimiter()
	d.InboxBaseURL = "https://app.example.com/api/v1"
}

// push sends a raw body to the public endpoint with the given headers.
func (e *env) push(id, token, contentType string, body []byte, hdr map[string]string) (int, map[string]any, http.Header) {
	e.t.Helper()
	h := map[string]string{"Content-Type": contentType}
	for k, v := range hdr {
		h[k] = v
	}
	resp, raw := e.do("POST", api1+"/inbox/"+id, token, body, h)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, resp.Header
}

func TestDataInboxesOverHTTP(t *testing.T) {
	ib := &inboxOpts{}
	e := newEnv(t, opts{auth: true, inboxes: ib})
	owner := e.register("owner-inbox@example.com", "InboxCo")
	admin := e.member(owner, "admin-inbox@example.com", auth.RoleAdmin)
	member := e.member(owner, "member-inbox@example.com", auth.RoleMember)
	rival := e.register("rival-inbox@example.com", "RivalInbox")
	base := api1 + "/data-inboxes"

	// Only owners/admins manage inboxes.
	if s := e.status("POST", base, member, map[string]any{"name": "x"}); s != 403 {
		t.Fatalf("member create = %d", s)
	}
	if s := e.status("GET", base, member, nil); s != 403 {
		t.Fatalf("member list = %d", s)
	}
	if s := e.status("POST", base, admin, map[string]any{"name": "x", "folder": "/Private/x"}); s != 400 {
		t.Fatalf("folder outside /Shared = %d", s)
	}

	c := e.json("POST", base, admin, map[string]any{"name": "Pedidos ERP", "allowed": []string{"json", "csv"}, "max_bytes": 2048, "rate_per_min": 3}, 201)
	id, token := str(c, "id"), str(c, "token")
	if !strings.HasPrefix(token, inboxes.TokenPrefix) || c["token_hash"] != nil || c["status"] != "active" {
		t.Fatalf("created: %v", c)
	}
	if str(c, "push_url") != "https://app.example.com/api/v1/inbox/"+id || str(c, "push_path") != "/api/v1/inbox/"+id {
		t.Fatalf("urls: %v %v", c["push_url"], c["push_path"])
	}
	if str(c, "folder_path") != "/Shared/Imports/pedidos-erp" {
		t.Fatalf("folder: %v", c["folder_path"])
	}
	// The token is never listed again.
	resp, raw := e.do("GET", base, admin, nil, nil)
	if resp.StatusCode != 200 || bytes.Contains(raw, []byte(token)) || bytes.Contains(raw, []byte("token_hash")) {
		t.Fatalf("list leaks the token: %d %s", resp.StatusCode, raw)
	}
	if s := e.status("GET", base+"/"+id, rival.AccessToken, nil); s != 404 {
		t.Fatalf("another organization sees nothing = %d", s)
	}

	// Public endpoint: header only, never a session.
	if s, out, h := e.push(id, "", "application/json", []byte(`{"a":1}`), nil); s != 401 || out["code"] != "unauthorized" || h.Get("WWW-Authenticate") == "" {
		t.Fatalf("no token = %d %v", s, out)
	}
	if s, out, _ := e.push(id, owner.AccessToken, "application/json", []byte(`{"a":1}`), nil); s != 401 || out["code"] != "unauthorized" {
		t.Fatalf("a user session is not an inbox token = %d %v", s, out)
	}
	resp, raw = e.do("POST", api1+"/inbox/"+id+"?token="+token, "", []byte(`{"a":1}`), nil)
	if resp.StatusCode != 400 || !bytes.Contains(raw, []byte("token_in_query")) {
		t.Fatalf("token in the query = %d %s", resp.StatusCode, raw)
	}
	if s, _, _ := e.push("ibx_other", token, "application/json", []byte(`{"a":1}`), nil); s != 401 {
		t.Fatalf("token of another inbox id = %d", s)
	}

	// A JSON push becomes a file authored by the inbox.
	s, out, h := e.push(id, token, "application/json", []byte(`{"order":42}`), map[string]string{"X-Filename": "orders.json"})
	if s != 201 || h.Get("X-RateLimit-Limit") != "3" || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("json push = %d %v %v", s, out, h)
	}
	files, _ := out["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files: %v", out)
	}
	f0 := files[0].(map[string]any)
	if f0["path"] != "/Shared/Imports/pedidos-erp/orders.json" || f0["kind"] != "json" {
		t.Fatalf("file: %v", f0)
	}
	df, err := ib.drive.FileByPath(application.WithOrg(context.Background(), owner.OrgID), "/Shared/Imports/pedidos-erp/orders.json")
	if err != nil || df.CreatedBy.Kind != drive.PrincipalInbox || df.CreatedBy.ID != id {
		t.Fatalf("drive file: %+v %v", df.CreatedBy, err)
	}

	// Content kinds and media types.
	if s, out, _ := e.push(id, token, "application/pdf", []byte("%PDF-1.4 x"), nil); s != 415 || out["code"] != "kind_not_allowed" {
		t.Fatalf("pdf on a json/csv inbox = %d %v", s, out)
	}
	if s, out, _ := e.push(id, token, "text/html", []byte("<b>x</b>"), nil); s != 415 || out["code"] != "unsupported_media_type" {
		t.Fatalf("html = %d %v", s, out)
	}

	// Rate limit per inbox: 3 per minute (the json, pdf and html pushes above count).
	if s, out, h := e.push(id, token, "text/csv", []byte("a,b\n1,2\n"), nil); s != 429 || out["code"] != "rate_limited" || h.Get("Retry-After") == "" {
		t.Fatalf("4th push in the minute = %d %v", s, out)
	}

	// Rotation: the old token stops at once, the new one works (a fresh inbox id keeps the rate window apart).
	r := e.json("POST", base+"/"+id+"/rotate", admin, nil, 200)
	if str(r, "token") == "" || str(r, "token") == token {
		t.Fatalf("rotate: %v", r)
	}
	if s, _, _ := e.push(id, token, "application/json", []byte(`{}`), nil); s != 401 {
		t.Fatalf("old token after rotation = %d", s)
	}

	// Update and pushes listing.
	u := e.json("PUT", base+"/"+id, admin, map[string]any{"name": "Pedidos ERP v2", "allowed": []string{"json"}, "max_bytes": 4096, "rate_per_min": 10}, 200)
	if u["name"] != "Pedidos ERP v2" || u["max_bytes"] != float64(4096) {
		t.Fatalf("update: %v", u)
	}
	pushes := e.list(base+"/"+id+"/pushes", admin)
	if len(pushes) != 1 || pushes[0]["size"] != float64(len(`{"order":42}`)) {
		t.Fatalf("pushes: %v", pushes)
	}
	if s := e.status("GET", base+"/"+id+"/pushes", member, nil); s != 403 {
		t.Fatalf("member pushes = %d", s)
	}

	// Revocation: the token is dead, the file stays.
	v := e.json("DELETE", base+"/"+id, admin, nil, 200)
	if v["status"] != "revoked" {
		t.Fatalf("revoke: %v", v)
	}
	if s, _, _ := e.push(id, str(r, "token"), "application/json", []byte(`{}`), nil); s != 401 {
		t.Fatalf("revoked = %d", s)
	}
	if s := e.status("POST", base+"/"+id+"/rotate", admin, nil); s != 409 {
		t.Fatalf("rotate a revoked inbox = %d", s)
	}
}

func TestDataInboxPushLimitsAndControls(t *testing.T) {
	ib := &inboxOpts{}
	e := newEnv(t, opts{inboxes: ib}) // open demo: admin routes without login, the push still needs its token
	c := e.json("POST", api1+"/data-inboxes", "", map[string]any{"name": "Exports", "allowed": []string{"json", "csv", "files"}, "max_bytes": 1024, "rate_per_min": 100}, 201)
	id, token := str(c, "id"), str(c, "token")
	if s, _, _ := e.push(id, "", "application/json", []byte(`{}`), nil); s != 401 {
		t.Fatalf("demo push without token = %d", s)
	}

	// Declared Content-Length above the cap: refused before reading.
	big := bytes.Repeat([]byte("a,b\n"), 40<<10) // 160 KiB > 1 KiB + multipart overhead
	if s, out, _ := e.push(id, token, "text/csv", big, nil); s != 413 || out["code"] != "payload_too_large" {
		t.Fatalf("declared too large = %d %v", s, out)
	}
	// Within the HTTP cap but over the inbox payload cap.
	if s, out, _ := e.push(id, token, "text/csv", bytes.Repeat([]byte("a,b\n"), 600), nil); s != 413 || out["code"] != "payload_too_large" {
		t.Fatalf("payload too large = %d %v", s, out)
	}
	// Chunked body (no Content-Length) over the cap.
	req, _ := http.NewRequest("POST", e.srv.URL+api1+"/inbox/"+id, io2Reader(big))
	req.Header.Set("Content-Type", "text/csv")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := ioReadAll(resp)
	if resp.StatusCode != 413 || !bytes.Contains(raw, []byte("payload_too_large")) {
		t.Fatalf("chunked too large = %d %s", resp.StatusCode, raw)
	}
	if s, out, _ := e.push(id, token, "application/json", []byte(`{not json`), nil); s != 400 || out["code"] != "bad_payload" {
		t.Fatalf("bad json = %d %v", s, out)
	}

	// Idempotency: the same key replays the first result.
	k := map[string]string{"X-Idempotency-Key": "run-2026-10-27"}
	s1, first, _ := e.push(id, token, "text/csv", []byte("a,b\n1,2\n"), k)
	s2, again, _ := e.push(id, token, "text/csv", []byte("a,b\n1,2\n"), k)
	if s1 != 201 || s2 != 200 || again["replayed"] != true || again["push_id"] != first["push_id"] {
		t.Fatalf("idempotency: %d %v / %d %v", s1, first, s2, again)
	}

	// Read-only mode and the kill switch refuse pushes.
	ib.setGate(inboxes.ErrReadOnly)
	if s, out, _ := e.push(id, token, "application/json", []byte(`{}`), nil); s != 423 || out["code"] != "read_only_mode" {
		t.Fatalf("read-only = %d %v", s, out)
	}
	ib.setGate(inboxes.ErrKillSwitch)
	if s, out, _ := e.push(id, token, "application/json", []byte(`{}`), nil); s != 503 || out["code"] != "kill_switch_active" {
		t.Fatalf("kill switch = %d %v", s, out)
	}
	ib.setGate(nil)
	if s, _, _ := e.push(id, token, "application/json", []byte(`{"ok":true}`), nil); s != 201 {
		t.Fatalf("after the gate reopens = %d", s)
	}
}

// io2Reader hides the length of a body so the client sends it chunked.
func io2Reader(b []byte) io.Reader { return struct{ io.Reader }{bytes.NewReader(b)} }

func ioReadAll(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
