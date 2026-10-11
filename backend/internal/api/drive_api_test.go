package api_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/drive"
)

// F1: the drive over HTTP, as people use it (agents only reach it through fs.*).
func TestDriveOverHTTP(t *testing.T) {
	var ro atomic.Bool
	gate := func(context.Context, string) error {
		if ro.Load() {
			return drive.ErrReadOnly
		}
		return nil
	}
	e := newEnv(t, opts{auth: true, drive: true, writeGate: gate})
	owner := e.register("owner-drive@example.com", "OwnerDrive")
	member := e.member(owner, "member-drive@example.com", auth.RoleMember)
	viewer := e.member(owner, "viewer-drive@example.com", auth.RoleViewer)
	rival := e.register("rival-drive@example.com", "RivalDrive")
	put := func(path string, extra string) string {
		return api1 + "/drive/upload?path=" + url.QueryEscape(path) + extra
	}
	text := map[string]string{"Content-Type": "text/markdown"}

	// Upload creates the file (201) and its folders.
	r, b := e.do("PUT", put("/Shared/Plan/plan.md", "&message=first"), member, []byte("# Plan\nlinea uno\n"), text)
	if r.StatusCode != 201 {
		t.Fatalf("upload = %d %s", r.StatusCode, b)
	}
	var w struct {
		File    drive.File    `json:"file"`
		Version drive.Version `json:"version"`
	}
	if err := json.Unmarshal(b, &w); err != nil || w.File.ID == "" || w.Version.Version != 1 {
		t.Fatalf("upload body: %s", b)
	}
	id := w.File.ID
	files := api1 + "/drive/files/" + id

	// Listing and reading.
	l := e.json("GET", api1+"/drive/list?path="+url.QueryEscape("/Shared/Plan"), viewer, nil, 200)
	if fs, _ := l["files"].([]any); len(fs) != 1 {
		t.Fatalf("list: %v", l)
	}
	if r, b := e.do("GET", files+"/content", viewer, nil, nil); r.StatusCode != 200 || string(b) != "# Plan\nlinea uno\n" ||
		r.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(r.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("content = %d %q %v", r.StatusCode, b, r.Header)
	}
	if tx := e.json("GET", files+"/text", viewer, nil, 200); !strings.Contains(str(tx, "text"), "linea uno") {
		t.Fatalf("text: %v", tx)
	}

	// New version with optimistic locking: a stale expected_version is a conflict.
	if r, b := e.do("PUT", files+"/content?expected_version=1&message=second", member, []byte("# Plan\nlinea dos\n"), text); r.StatusCode != 200 {
		t.Fatalf("new version = %d %s", r.StatusCode, b)
	}
	if r, b := e.do("PUT", files+"/content?expected_version=1", member, []byte("# Plan\nlinea tres\n"), text); r.StatusCode != 409 ||
		!strings.Contains(string(b), "version_conflict") || !strings.Contains(string(b), `"current_version":2`) {
		t.Fatalf("stale write = %d %s", r.StatusCode, b)
	}
	if vs := e.json("GET", files+"/versions", viewer, nil, 200); len(vs["versions"].([]any)) != 2 {
		t.Fatalf("versions: %v", vs)
	}
	if d := e.json("GET", files+"/diff?from=1&to=2", viewer, nil, 200); !strings.Contains(mustJSON(t, d), "linea dos") {
		t.Fatalf("diff: %v", d)
	}
	// Restoring version 1 adds version 3 equal to it: history is never rewritten.
	e.json("POST", files+"/versions/1/restore", member, nil, 200)
	if r, b := e.do("GET", files+"/content", viewer, nil, nil); string(b) != "# Plan\nlinea uno\n" {
		t.Fatalf("after restore = %d %q", r.StatusCode, b)
	}

	// Search finds it by content (Spanish stemming).
	if s := e.json("GET", api1+"/drive/search?q=linea", viewer, nil, 200); !strings.Contains(mustJSON(t, s), "plan.md") {
		t.Fatalf("search: %v", s)
	}

	// Permissions: viewers read only, members cannot delete, admins (owner) can; trash restores.
	if r, _ := e.do("PUT", put("/Shared/x.md", ""), viewer, []byte("x"), text); r.StatusCode != 403 {
		t.Fatalf("viewer upload = %d", r.StatusCode)
	}
	if r, _ := e.do("DELETE", files, member, nil, nil); r.StatusCode != 403 {
		t.Fatalf("member delete = %d", r.StatusCode)
	}
	if r, b := e.do("DELETE", files, owner.AccessToken, nil, nil); r.StatusCode != 204 {
		t.Fatalf("owner delete = %d %s", r.StatusCode, b)
	}
	if tr := e.json("GET", api1+"/drive/trash", viewer, nil, 200); !strings.Contains(mustJSON(t, tr), id) {
		t.Fatalf("trash: %v", tr)
	}
	e.json("POST", files+"/restore", member, nil, 200)

	// Tenants are isolated.
	if r, _ := e.do("GET", files, rival.AccessToken, nil, nil); r.StatusCode != 404 {
		t.Fatalf("other tenant read = %d", r.StatusCode)
	}
	// Path traversal is refused.
	if r, b := e.do("PUT", put("/Shared/../../etc/passwd", ""), member, []byte("x"), text); r.StatusCode != 400 {
		t.Fatalf("traversal = %d %s", r.StatusCode, b)
	}
	// Read-only mode blocks every change (423) but not reading.
	ro.Store(true)
	if r, b := e.do("PUT", put("/Shared/y.md", ""), member, []byte("y"), text); r.StatusCode != 423 || !strings.Contains(string(b), "read_only_mode") {
		t.Fatalf("read-only upload = %d %s", r.StatusCode, b)
	}
	e.json("GET", files, viewer, nil, 200)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
