-- Rollback of 481_artifact_blobs_object.sql. Blobs whose bytes live in an
-- external object store (object_key set, data NULL) cannot be represented in
-- the old schema: their rows are removed (the PDF viewer reports the file as
-- unavailable; the bytes stay in the bucket). Blobs with data are kept as is.
-- FORCE RLS applies to the owner too: lift it for this one statement.
ALTER TABLE artifact_blobs NO FORCE ROW LEVEL SECURITY;
DELETE FROM artifact_blobs WHERE data IS NULL;
ALTER TABLE artifact_blobs FORCE ROW LEVEL SECURITY;
ALTER TABLE artifact_blobs DROP CONSTRAINT IF EXISTS artifact_blobs_data_or_object;
ALTER TABLE artifact_blobs DROP COLUMN IF EXISTS object_key;
ALTER TABLE artifact_blobs ALTER COLUMN data SET NOT NULL;
ALTER TABLE artifact_blobs ADD CONSTRAINT artifact_blobs_check CHECK (octet_length(data) = size_bytes);
