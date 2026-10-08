package postgres

import (
	"context"

	"aiworkforce/backend/internal/artifacts"
)

// ArtifactStore also implements artifacts.BlobStore (migration 370): uploaded
// PDF bytes in a bytea column, insert-only for app_user, behind RLS by org.
var _ artifacts.BlobStore = (*ArtifactStore)(nil)

func (a *ArtifactStore) PutBlob(ctx context.Context, org string, b artifacts.Blob) error {
	_, err := a.S.execTag(ctx, org, `INSERT INTO artifact_blobs (id, org_id, artifact_id, mime, filename, sha256, size_bytes, data, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		b.ID, org, b.ArtifactID, b.MIME, b.Filename, b.SHA256, b.Size, b.Data, jb(b.CreatedBy), b.CreatedAt)
	return err
}

func scanArtBlob(r scanner) (artifacts.Blob, error) {
	var b artifacts.Blob
	var by []byte
	err := r.Scan(&b.ID, &b.ArtifactID, &b.MIME, &b.Filename, &b.SHA256, &b.Size, &b.Data, &by, &b.CreatedAt)
	unmarshal(by, &b.CreatedBy)
	return b, err
}

func (a *ArtifactStore) GetBlob(ctx context.Context, org, id string) (artifacts.Blob, error) {
	b, err := one(ctx, a.S, org, scanArtBlob, `SELECT id, artifact_id, mime, filename, sha256, size_bytes, data, created_by, created_at
		FROM artifact_blobs WHERE org_id=$1 AND id=$2`, org, id)
	return b, mapErr(err)
}
