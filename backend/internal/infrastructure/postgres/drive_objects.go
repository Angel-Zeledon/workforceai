package postgres

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/jackc/pgx/v5"

	"aiworkforce/backend/internal/objstore"
)

// ObjectStore is the objstore.Store fallback that keeps the bytes in Postgres
// (drive_objects, bytea, migration 480) for deployments without S3-compatible
// storage. The org comes from the key ("orgs/<org>/..."), so RLS still applies.
// Fine for the default 25 MB cap and small installations; configure S3 for
// anything bigger (see docs/architecture/drive.md).
type ObjectStore struct {
	S *Store
	// MaxBytes caps one object (default 64 MB).
	MaxBytes int64
}

var _ objstore.Store = (*ObjectStore)(nil)

func (o *ObjectStore) max() int64 {
	if o.MaxBytes > 0 {
		return o.MaxBytes
	}
	return 64 << 20
}

func orgOfKey(key string) (string, error) {
	if err := objstore.ValidKey(key); err != nil {
		return "", err
	}
	org := objstore.OrgOf(key)
	if org == "" {
		return "", objstore.ErrBadKey
	}
	return org, nil
}

func (o *ObjectStore) Put(ctx context.Context, key string, r io.Reader, size int64, opt objstore.PutOptions) (objstore.Info, error) {
	org, err := orgOfKey(key)
	if err != nil {
		return objstore.Info{}, err
	}
	limit := o.max()
	if opt.MaxBytes > 0 {
		limit = min(limit, opt.MaxBytes)
	}
	if size >= 0 {
		limit = min(limit, size)
	}
	b, err := objstore.ReadAllBounded(r, limit)
	if err != nil {
		return objstore.Info{}, err
	}
	if size >= 0 && int64(len(b)) != size {
		return objstore.Info{}, io.ErrUnexpectedEOF
	}
	sum := objstore.SumBytes(b)
	if opt.SHA256 != "" && opt.SHA256 != sum {
		return objstore.Info{}, errors.New("objstore: body does not match the given sha256")
	}
	err = o.S.exec(ctx, org, `INSERT INTO drive_objects (org_id, key, data, size_bytes, sha256, content_type) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (org_id, key) DO UPDATE SET data=EXCLUDED.data, size_bytes=EXCLUDED.size_bytes, sha256=EXCLUDED.sha256, content_type=EXCLUDED.content_type`,
		org, key, b, len(b), sum, opt.ContentType)
	if err != nil {
		return objstore.Info{}, err
	}
	return objstore.Info{Key: key, Size: int64(len(b)), SHA256: sum, ContentType: opt.ContentType}, nil
}

func (o *ObjectStore) Get(ctx context.Context, key string) (io.ReadCloser, objstore.Info, error) {
	org, err := orgOfKey(key)
	if err != nil {
		return nil, objstore.Info{}, err
	}
	var data []byte
	var info objstore.Info
	err = o.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT data, size_bytes, sha256, content_type, created_at FROM drive_objects WHERE org_id=$1 AND key=$2`, org, key).
			Scan(&data, &info.Size, &info.SHA256, &info.ContentType, &info.ModifiedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, objstore.Info{}, objstore.ErrNotFound
	}
	if err != nil {
		return nil, objstore.Info{}, err
	}
	info.Key = key
	return io.NopCloser(bytes.NewReader(data)), info, nil
}

func (o *ObjectStore) Head(ctx context.Context, key string) (objstore.Info, error) {
	org, err := orgOfKey(key)
	if err != nil {
		return objstore.Info{}, err
	}
	info := objstore.Info{Key: key}
	err = o.S.WithOrgTx(ctx, org, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT size_bytes, sha256, content_type, created_at FROM drive_objects WHERE org_id=$1 AND key=$2`, org, key).
			Scan(&info.Size, &info.SHA256, &info.ContentType, &info.ModifiedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return objstore.Info{}, objstore.ErrNotFound
	}
	return info, err
}

func (o *ObjectStore) Delete(ctx context.Context, key string) error {
	org, err := orgOfKey(key)
	if err != nil {
		return err
	}
	return o.S.exec(ctx, org, `DELETE FROM drive_objects WHERE org_id=$1 AND key=$2`, org, key)
}
