package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"aiworkforce/backend/internal/artifacts"
	"aiworkforce/backend/internal/domain"
	"aiworkforce/backend/internal/objstore"
)

// ArtifactStore also implements artifacts.BlobStore (migrations 370 and 481):
// uploaded PDF files, insert-only for app_user, behind RLS by org.
//
// Where the bytes live depends on ArtifactStore.Objects:
//   - nil: in a bytea column of the row (migration 370; the default, no extra
//     service needed);
//   - set (S3-compatible storage configured): in the object store under
//     "orgs/<org>/pdf/<blob id>", and the row only holds the metadata and the
//     object key (migration 481).
//
// Rows written before the object store was configured keep their bytea and are
// still read from it, so switching storage never orphans an old PDF.
var _ artifacts.BlobStore = (*ArtifactStore)(nil)

func blobKey(org, id string) string { return objstore.OrgKey(org, "pdf", id) }

func (a *ArtifactStore) PutBlob(ctx context.Context, org string, b artifacts.Blob) error {
	if a.Objects == nil {
		_, err := a.S.execTag(ctx, org, `INSERT INTO artifact_blobs (id, org_id, artifact_id, mime, filename, sha256, size_bytes, data, created_by, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			b.ID, org, b.ArtifactID, b.MIME, b.Filename, b.SHA256, b.Size, b.Data, jb(b.CreatedBy), b.CreatedAt)
		return err
	}
	key := blobKey(org, b.ID)
	if _, err := a.Objects.Put(ctx, key, bytes.NewReader(b.Data), int64(len(b.Data)), objstore.PutOptions{ContentType: b.MIME, SHA256: b.SHA256, MaxBytes: artifacts.MaxPDFBytes}); err != nil {
		return fmt.Errorf("store pdf object: %w", err)
	}
	_, err := a.S.execTag(ctx, org, `INSERT INTO artifact_blobs (id, org_id, artifact_id, mime, filename, sha256, size_bytes, data, object_key, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NULL,$8,$9,$10)`,
		b.ID, org, b.ArtifactID, b.MIME, b.Filename, b.SHA256, b.Size, key, jb(b.CreatedBy), b.CreatedAt)
	if err != nil {
		_ = a.Objects.Delete(ctx, key) // nothing points at it
	}
	return err
}

func scanArtBlob(r scanner) (artifacts.Blob, error) {
	var b artifacts.Blob
	var by []byte
	var key *string
	err := r.Scan(&b.ID, &b.ArtifactID, &b.MIME, &b.Filename, &b.SHA256, &b.Size, &b.Data, &key, &by, &b.CreatedAt)
	unmarshal(by, &b.CreatedBy)
	if key != nil {
		b.Data = nil
		b.ObjectKey = *key
	}
	return b, err
}

func (a *ArtifactStore) GetBlob(ctx context.Context, org, id string) (artifacts.Blob, error) {
	b, err := one(ctx, a.S, org, scanArtBlob, `SELECT id, artifact_id, mime, filename, sha256, size_bytes, data, object_key, created_by, created_at
		FROM artifact_blobs WHERE org_id=$1 AND id=$2`, org, id)
	if err != nil {
		return b, mapErr(err)
	}
	if b.ObjectKey != "" {
		if a.Objects == nil {
			return artifacts.Blob{}, errors.New("this PDF is stored in the object store, which is not configured")
		}
		rc, _, err := a.Objects.Get(ctx, b.ObjectKey)
		if errors.Is(err, objstore.ErrNotFound) {
			return artifacts.Blob{}, domain.ErrNotFound
		}
		if err != nil {
			return artifacts.Blob{}, err
		}
		defer rc.Close()
		if b.Data, err = objstore.ReadAllBounded(rc, artifacts.MaxPDFBytes); err != nil {
			return artifacts.Blob{}, err
		}
	}
	return b, nil
}
