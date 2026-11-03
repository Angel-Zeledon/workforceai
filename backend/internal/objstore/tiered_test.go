package objstore_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"aiworkforce/backend/internal/objstore"
	"aiworkforce/backend/internal/objstore/objstoretest"
)

func TestTieredContract(t *testing.T) {
	objstoretest.Run(t, &objstore.Tiered{Primary: objstore.NewMemory(), Fallback: objstore.NewMemory()}, "org-a")
}

// An object that only the old store (Postgres) has is still read; new objects go to the new one (S3).
func TestTieredReadsOldObjectsAndWritesNewOnes(t *testing.T) {
	ctx := context.Background()
	oldStore, newStore := objstore.NewMemory(), objstore.NewMemory()
	oldKey, newKey := objstore.OrgKey("o", "drive", "f1", "v1"), objstore.OrgKey("o", "drive", "f1", "v2")
	if _, err := oldStore.Put(ctx, oldKey, bytes.NewReader([]byte("old")), 3, objstore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	tr := &objstore.Tiered{Primary: newStore, Fallback: oldStore}
	rc, info, err := tr.Get(ctx, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "old" || info.Size != 3 {
		t.Fatalf("fallback read: %q %+v", b, info)
	}
	if _, err := tr.Put(ctx, newKey, bytes.NewReader([]byte("new!")), 4, objstore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := newStore.Head(ctx, newKey); err != nil {
		t.Fatalf("new object must be in the primary store: %v", err)
	}
	if _, err := oldStore.Head(ctx, newKey); !errors.Is(err, objstore.ErrNotFound) {
		t.Fatalf("new object must not be in the fallback: %v", err)
	}
	if err := tr.Delete(ctx, oldKey); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Head(ctx, oldKey); !errors.Is(err, objstore.ErrNotFound) {
		t.Fatalf("deleted from both: %v", err)
	}
}
