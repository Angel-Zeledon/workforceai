-- Rollback of 480_drive.sql. Drops every folder, file, version, grant and the
-- bytea objects. Content kept in an external object store is NOT touched: it
-- stays in the bucket, unreferenced (delete the "orgs/<org>/drive/" prefix by hand).
DROP TRIGGER IF EXISTS drive_versions_no_update ON drive_versions;
DROP TABLE IF EXISTS drive_objects;
DROP TABLE IF EXISTS drive_grants;
DROP TABLE IF EXISTS drive_versions;
DROP TABLE IF EXISTS drive_files;
DROP TABLE IF EXISTS drive_folders;
DROP FUNCTION IF EXISTS drive_versions_append_only();
