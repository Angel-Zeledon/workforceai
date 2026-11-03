package api_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"strings"
	"testing"

	"aiworkforce/backend/internal/auth"
)

func multipartBody(t *testing.T, files map[string]string, order []string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("note", "fields that are not files are ignored")
	for _, name := range order {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
		h.Set("Content-Type", "application/octet-stream")
		pw, err := mw.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = pw.Write([]byte(files[name]))
	}
	_ = mw.Close()
	return buf.Bytes(), mw.FormDataContentType()
}

// Several files in one multipart request, each streamed; folder uploads keep their sub-paths; one bad
// file does not undo the others; viewers cannot upload.
func TestDriveMultipartUpload(t *testing.T) {
	e := newEnv(t, opts{auth: true, drive: true})
	owner := e.register("owner-mp@example.com", "OwnerMP")
	viewer := e.member(owner, "viewer-mp@example.com", auth.RoleViewer)
	files := map[string]string{"a.md": "# A\n", "sub/b.csv": "x,y\n1,2\n", "bad.pdf": "not a pdf", "../escape.txt": "nope", "virus.exe": "MZ"}
	body, ct := multipartBody(t, files, []string{"a.md", "sub/b.csv", "bad.pdf", "../escape.txt", "virus.exe"})
	u := api1 + "/drive/upload?folder=" + url.QueryEscape("/Shared/Lote")

	if r, _ := e.do("POST", u, viewer, body, map[string]string{"Content-Type": ct}); r.StatusCode != 403 {
		t.Fatalf("viewer upload = %d", r.StatusCode)
	}
	r, b := e.do("POST", u, owner.AccessToken, body, map[string]string{"Content-Type": ct})
	if r.StatusCode != 200 {
		t.Fatalf("multipart = %d %s", r.StatusCode, b)
	}
	var out struct {
		Results []struct {
			Name   string `json:"name"`
			Path   string `json:"path"`
			Status int    `json:"status"`
			Code   string `json:"code"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &out); err != nil || len(out.Results) != 5 {
		t.Fatalf("results: %s", b)
	}
	want := []struct {
		path   string
		status int
	}{{"/Shared/Lote/a.md", 201}, {"/Shared/Lote/sub/b.csv", 201}, {"/Shared/Lote/bad.pdf", 400}, {"", 400}, {"/Shared/Lote/virus.exe", 415}}
	for i, w := range want {
		got := out.Results[i]
		if got.Status != w.status || (w.path != "" && got.Path != w.path) {
			t.Fatalf("result %d = %+v, want %+v", i, got, w)
		}
	}
	l := e.json("GET", api1+"/drive/list?path="+url.QueryEscape("/Shared/Lote/sub"), owner.AccessToken, nil, 200)
	if fs, _ := l["files"].([]any); len(fs) != 1 {
		t.Fatalf("sub folder: %v", l)
	}
	if r, b := e.do("GET", api1+"/drive/list?path="+url.QueryEscape("/Shared/escape.txt"), owner.AccessToken, nil, nil); r.StatusCode == 200 && strings.Contains(string(b), "escape") {
		t.Fatalf("traversal escaped the folder: %s", b)
	}
	// usage tells the client the streamed cap
	if us := e.json("GET", api1+"/drive/usage", owner.AccessToken, nil, 200); us["max_upload_bytes"] == nil {
		t.Fatalf("usage: %v", us)
	}
	// bad requests
	if r, _ := e.do("POST", api1+"/drive/upload", owner.AccessToken, body, map[string]string{"Content-Type": ct}); r.StatusCode != 400 {
		t.Fatalf("no folder = %d", r.StatusCode)
	}
	if r, _ := e.do("POST", u, owner.AccessToken, []byte("{}"), map[string]string{"Content-Type": "application/json"}); r.StatusCode != 400 {
		t.Fatalf("not multipart = %d", r.StatusCode)
	}
}

// A raw upload that announces more than the cap is refused before its body is read.
func TestDriveUploadAnnouncedTooLarge(t *testing.T) {
	e := newEnv(t, opts{auth: true, drive: true})
	owner := e.register("owner-big@example.com", "OwnerBig")
	us := e.json("GET", api1+"/drive/usage", owner.AccessToken, nil, 200)
	limit := int64(us["max_upload_bytes"].(float64))
	big := bytes.Repeat([]byte("a"), int(limit)+1)
	r, b := e.do("PUT", api1+"/drive/upload?path="+url.QueryEscape("/Shared/big.txt"), owner.AccessToken, big, map[string]string{"Content-Type": "text/plain"})
	if r.StatusCode != 413 || !strings.Contains(string(b), "file_too_large") {
		t.Fatalf("too large = %d %s", r.StatusCode, b)
	}
}
