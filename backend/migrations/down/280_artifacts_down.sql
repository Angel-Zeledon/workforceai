-- Rollback of 280_artifacts.sql. Drops every artifact, version and link.
DROP TRIGGER IF EXISTS artifact_versions_no_update ON artifact_versions;
DROP TABLE IF EXISTS artifact_links;
DROP TABLE IF EXISTS artifact_versions;
DROP TABLE IF EXISTS artifacts;
DROP FUNCTION IF EXISTS artifact_versions_append_only();
