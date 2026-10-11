// Package objstoretest holds the contract every objstore.Store must pass.
package objstoretest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"aiworkforce/backend/internal/objstore"
)

// contract runs the behaviour every Store must have.
// Run runs the behaviour every objstore.Store must have. org must be an
// organization id the store accepts (the Postgres store needs a real one).
func Run(t *testing.T, s objstore.Store, org string) {
	t.Helper()
	ctx := context.Background()
	key := objstore.OrgKey(org, "drive", "f1", "v1")
	data := bytes.Repeat([]byte("hola mundo "), 1000)

	info, err := s.Put(ctx, key, bytes.NewReader(data), int64(len(data)), objstore.PutOptions{ContentType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(data)) || info.SHA256 != objstore.SumBytes(data) {
		t.Fatalf("put info: %+v", info)
	}
	rc, gi, err := s.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, data) || gi.Size != int64(len(data)) {
		t.Fatalf("get mismatch: %d bytes, info %+v", len(got), gi)
	}
	hi, err := s.Head(ctx, key)
	if err != nil || hi.Size != int64(len(data)) {
		t.Fatalf("head: %v %+v", err, hi)
	}
	// Unknown size is buffered (bounded).
	k2 := objstore.OrgKey(org, "drive", "f1", "v2=x@y")
	if _, err := s.Put(ctx, k2, strings.NewReader("abc"), -1, objstore.PutOptions{MaxBytes: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, objstore.OrgKey(org, "big"), strings.NewReader(strings.Repeat("x", 50)), -1, objstore.PutOptions{MaxBytes: 10}); !errors.Is(err, objstore.ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	// A body shorter than the declared size is refused and nothing is kept.
	k3 := objstore.OrgKey(org, "short")
	if _, err := s.Put(ctx, k3, strings.NewReader("abc"), 10, objstore.PutOptions{}); err == nil {
		t.Fatal("short body must fail")
	}
	if _, err := s.Head(ctx, k3); !errors.Is(err, objstore.ErrNotFound) {
		t.Fatalf("failed put left an object: %v", err)
	}
	// Overwrite, delete, idempotent delete, missing.
	if _, err := s.Put(ctx, key, strings.NewReader("new"), 3, objstore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	rc, _, _ = s.Get(ctx, key)
	got, _ = io.ReadAll(rc)
	rc.Close()
	if string(got) != "new" {
		t.Fatalf("overwrite: %q", got)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if _, _, err := s.Get(ctx, key); !errors.Is(err, objstore.ErrNotFound) {
		t.Fatalf("get deleted: %v", err)
	}
	if _, err := s.Head(ctx, key); !errors.Is(err, objstore.ErrNotFound) {
		t.Fatalf("head deleted: %v", err)
	}
	// Bad keys never reach the backend.
	for _, bad := range []string{"", "/abs", "a/../b", "a//b", "a/./b", "a b", "a/", strings.Repeat("k", 400)} {
		if _, err := s.Put(ctx, bad, strings.NewReader("x"), 1, objstore.PutOptions{}); !errors.Is(err, objstore.ErrBadKey) {
			t.Fatalf("key %q accepted: %v", bad, err)
		}
		if _, _, err := s.Get(ctx, bad); !errors.Is(err, objstore.ErrBadKey) {
			t.Fatalf("get key %q: %v", bad, err)
		}
	}
	// A cancelled context stops a put.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Put(cctx, objstore.OrgKey(org, "c"), strings.NewReader("x"), 1, objstore.PutOptions{}); err == nil {
		t.Fatal("cancelled context must fail")
	}
}
