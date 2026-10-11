-- PDF blobs in an object store (F1): when S3-compatible storage is configured
-- new uploads keep their bytes in the object store and the row only holds the
-- metadata and the object key. Rows written before (bytea in `data`) stay
-- readable and valid. Rows stay immutable (insert-only for app_user).
-- Spec: docs/architecture/drive.md ("PDF blobs").

ALTER TABLE artifact_blobs ALTER COLUMN data DROP NOT NULL;
ALTER TABLE artifact_blobs ADD COLUMN IF NOT EXISTS object_key TEXT;
ALTER TABLE artifact_blobs DROP CONSTRAINT IF EXISTS artifact_blobs_check;
ALTER TABLE artifact_blobs DROP CONSTRAINT IF EXISTS artifact_blobs_data_or_object;
ALTER TABLE artifact_blobs ADD CONSTRAINT artifact_blobs_data_or_object CHECK (
    (data IS NOT NULL AND object_key IS NULL AND octet_length(data) = size_bytes)
 OR (data IS NULL AND object_key IS NOT NULL));
