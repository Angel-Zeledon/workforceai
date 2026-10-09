package objstore_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/objstore/objstoretest"
)

func TestMemoryContract(t *testing.T) { objstoretest.Run(t, objstore.NewMemory(), "org-a") }

// A fake S3 server (path style) checks the wire format: signed requests, the
// key encoding and streaming of the body.
func TestS3AgainstFakeServer(t *testing.T) {
	store := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/") {
			http.Error(w, "<Error><Code>AccessDenied</Code></Error>", 403)
			return
		}
		p := r.URL.EscapedPath()
		switch r.Method {
		case "PUT":
			b, err := io.ReadAll(r.Body)
			if err != nil { // truncated upload: S3 keeps nothing
				return
			}
			store[p] = b
		case "GET", "HEAD":
			b, ok := store[p]
			if !ok {
				http.Error(w, "<Error><Code>NoSuchKey</Code></Error>", 404)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(b)))
			if r.Method == "GET" {
				w.Write(b)
			}
		case "DELETE":
			delete(store, p)
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()
	s, err := objstore.NewS3(objstore.S3Config{Endpoint: srv.URL, Bucket: "bk", AccessKey: "AK", SecretKey: "SK", UsePathStyle: true, Prefix: "pre"})
	if err != nil {
		t.Fatal(err)
	}
	objstoretest.Run(t, s, "org-a")
	if _, ok := store["/bk/pre/orgs/org-a/drive/f1/v2%3Dx%40y"]; !ok {
		t.Fatalf("keys stored: %v", func() []string {
			var k []string
			for x := range store {
				k = append(k, x)
			}
			return k
		}())
	}
}

// Real MinIO: S3_TEST_ENDPOINT=http://localhost:19000 (see docs/architecture/drive.md).
func TestS3AgainstMinIO(t *testing.T) {
	ep := os.Getenv("S3_TEST_ENDPOINT")
	if ep == "" {
		t.Skip("S3_TEST_ENDPOINT not set")
	}
	s, err := objstore.NewS3(objstore.S3Config{Endpoint: ep, Bucket: "wfai-test", AccessKey: envOr("S3_TEST_ACCESS_KEY", "minioadmin"),
		SecretKey: envOr("S3_TEST_SECRET_KEY", "minioadmin"), UsePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	objstoretest.Run(t, s, "org-a")
	// With a digest the server verifies the body: a wrong digest is refused.
	_, err = s.Put(context.Background(), objstore.OrgKey("o", "bad-digest"), strings.NewReader("abc"), 3, objstore.PutOptions{SHA256: strings.Repeat("0", 64)})
	if err == nil {
		t.Fatal("a body that does not match the signed digest must be refused")
	}
	if _, err := s.Put(context.Background(), objstore.OrgKey("o", "good-digest"), strings.NewReader("abc"), 3, objstore.PutOptions{SHA256: objstore.SumBytes([]byte("abc"))}); err != nil {
		t.Fatal(err)
	}
	hi, err := s.Head(context.Background(), objstore.OrgKey("o", "good-digest"))
	if err != nil || hi.SHA256 != objstore.SumBytes([]byte("abc")) {
		t.Fatalf("head sha: %v %+v", err, hi)
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
