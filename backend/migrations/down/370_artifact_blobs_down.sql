-- Rollback of 370_artifact_blobs.sql. Drops every uploaded PDF; pdf artifacts
-- keep their blob_id, and the viewer reports the file as unavailable.
DROP TABLE IF EXISTS artifact_blobs;
