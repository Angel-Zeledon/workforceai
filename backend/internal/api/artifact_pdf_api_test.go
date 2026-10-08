package api_test

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/auth"
)

// A7: PDF upload (POST /artifacts/{id}/pdf) and download (GET /artifact-blobs/{id}).

const tinyPDF = "%PDF-1.4\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj\n" +
	"3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >> endobj\ntrailer << /Root 1 0 R >>\n%%EOF\n"

func pdfHdr() map[string]string { return map[string]string{"Content-Type": "application/pdf"} }

func TestArtifactPDFUploadAndDownloadOverHTTP(t *testing.T) {
	var ro atomic.Bool
	gate := func(context.Context, string) error {
		if ro.Load() {
			return artifacts.ErrReadOnly
		}
		return nil
	}
	e := newEnv(t, opts{auth: true, ws: true, writeGate: gate})
	owner := e.register("owner-pdf@example.com", "OwnerPDF")
	member := e.member(owner, "member-pdf@example.com", auth.RoleMember)
	viewer := e.member(owner, "viewer-pdf@example.com", auth.RoleViewer)
	rival := e.register("rival-pdf@example.com", "RivalPDF")

	art := e.json("POST", api1+"/artifacts", member, map[string]any{"kind": "pdf", "title": "Contrato", "template_id": ""}, 201)
	id := str(art, "id")
	up := api1 + "/artifacts/" + id + "/pdf?filename=" + "..%2F..%2Fcontrato%22x.pdf"

	// Content-type and magic bytes are both checked.
	if r, b := e.do("POST", up, member, []byte(tinyPDF), map[string]string{"Content-Type": "text/plain"}); r.StatusCode != 415 {
		t.Fatalf("text/plain = %d %s", r.StatusCode, b)
	}
	if r, b := e.do("POST", up, member, []byte("<html>not a pdf</html>"), pdfHdr()); r.StatusCode != 400 || !strings.Contains(string(b), "not_a_pdf") {
		t.Fatalf("html as pdf = %d %s", r.StatusCode, b)
	}
	if r, _ := e.do("POST", up, member, []byte{}, pdfHdr()); r.StatusCode != 400 {
		t.Fatalf("empty = %d", r.StatusCode)
	}
	big := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("0"), artifacts.MaxPDFBytes)...)
	if r, _ := e.do("POST", up, member, big, pdfHdr()); r.StatusCode != 413 {
		t.Fatalf("oversized = %d", r.StatusCode)
	}
	// Permissions and tenants.
	if r, _ := e.do("POST", up, viewer, []byte(tinyPDF), pdfHdr()); r.StatusCode != 403 {
		t.Fatalf("viewer upload = %d", r.StatusCode)
	}
	if r, _ := e.do("POST", up, rival.AccessToken, []byte(tinyPDF), pdfHdr()); r.StatusCode != 404 {
		t.Fatalf("other tenant upload = %d", r.StatusCode)
	}
	// Read-only mode blocks the upload.
	ro.Store(true)
	if r, b := e.do("POST", up, member, []byte(tinyPDF), pdfHdr()); r.StatusCode != 423 || !strings.Contains(string(b), "read_only_mode") {
		t.Fatalf("read-only upload = %d %s", r.StatusCode, b)
	}
	ro.Store(false)

	r, b := e.do("POST", up, member, []byte(tinyPDF), pdfHdr())
	if r.StatusCode != 201 {
		t.Fatalf("upload = %d %s", r.StatusCode, b)
	}
	got := e.json("GET", api1+"/artifacts/"+id, member, nil, 200)
	content, _ := got["content"].(map[string]any)
	blobID, _ := content["blob_id"].(string)
	if !strings.HasPrefix(blobID, "blob_") || content["pages"].(float64) != 1 || got["head_version"].(float64) != 2 {
		t.Fatalf("artifact after upload: %v", got)
	}
	if fn, _ := content["filename"].(string); strings.ContainsAny(fn, `/"\`) || !strings.HasSuffix(fn, ".pdf") {
		t.Fatalf("filename not sanitized: %q", fn)
	}

	dl := api1 + "/artifact-blobs/" + blobID
	r, body := e.do("GET", dl, viewer, nil, nil)
	if r.StatusCode != 200 || string(body) != tinyPDF || r.Header.Get("Content-Type") != "application/pdf" ||
		r.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(r.Header.Get("Content-Disposition"), "inline") ||
		!strings.Contains(r.Header.Get("Cache-Control"), "no-store") || !strings.Contains(r.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("viewer download = %d %v", r.StatusCode, r.Header)
	}
	if r, _ := e.do("GET", dl+"?download=1", viewer, nil, nil); !strings.HasPrefix(r.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download disposition = %v", r.Header.Get("Content-Disposition"))
	}
	if c := e.status("GET", dl, "", nil); c != 401 {
		t.Errorf("no token = %d", c)
	}
	if c := e.status("GET", dl, rival.AccessToken, nil); c != 404 {
		t.Errorf("other tenant download = %d, want 404", c)
	}
	if c := e.status("GET", api1+"/artifact-blobs/..%2Fetc", member, nil); c != 404 {
		t.Errorf("malformed id = %d, want 404", c)
	}
	// A doc artifact does not accept a PDF.
	doc := e.json("POST", api1+"/artifacts", member, map[string]any{"kind": "doc", "title": "Nota"}, 201)
	if r, _ := e.do("POST", api1+"/artifacts/"+str(doc, "id")+"/pdf", member, []byte(tinyPDF), pdfHdr()); r.StatusCode != 400 {
		t.Errorf("pdf into doc = %d", r.StatusCode)
	}
	// Saving pdf content with an arbitrary blob reference is refused.
	if c := e.status("POST", api1+"/artifacts/"+id+"/versions", member, map[string]any{"base_version": 2,
		"content": map[string]any{"schema": "aiw.pdf/1", "blob_id": "../../secret", "pages": 1, "annotations": []any{}}}); c != 400 {
		t.Errorf("bad blob_id = %d, want 400", c)
	}
}
