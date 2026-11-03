package objstore

import (
	"context"
	"errors"
	"io"
)

// Tiered writes to Primary and reads from Primary first, then from Fallback
// when Primary does not have the key. It is what keeps a deployment that moves
// from the Postgres object store (bytea) to S3 working without downtime: new
// objects go to S3 at once, old ones stay readable from Postgres until
// `server ctl objects-to-s3` has copied them (docs/architecture/drive.md).
type Tiered struct {
	Primary  Store
	Fallback Store // read-only for the application; nil = Primary only
}

var _ Store = (*Tiered)(nil)

// Put implements Store: only the primary store receives new objects.
func (t *Tiered) Put(ctx context.Context, key string, r io.Reader, size int64, o PutOptions) (Info, error) {
	return t.Primary.Put(ctx, key, r, size, o)
}

// Get implements Store.
func (t *Tiered) Get(ctx context.Context, key string) (io.ReadCloser, Info, error) {
	rc, info, err := t.Primary.Get(ctx, key)
	if errors.Is(err, ErrNotFound) && t.Fallback != nil {
		return t.Fallback.Get(ctx, key)
	}
	return rc, info, err
}

// Head implements Store.
func (t *Tiered) Head(ctx context.Context, key string) (Info, error) {
	info, err := t.Primary.Head(ctx, key)
	if errors.Is(err, ErrNotFound) && t.Fallback != nil {
		return t.Fallback.Head(ctx, key)
	}
	return info, err
}

// Delete implements Store: the object may live in either store.
func (t *Tiered) Delete(ctx context.Context, key string) error {
	err := t.Primary.Delete(ctx, key)
	if t.Fallback != nil {
		if ferr := t.Fallback.Delete(ctx, key); ferr != nil && !errors.Is(ferr, ErrNotFound) && err == nil {
			err = ferr
		}
	}
	return err
}
