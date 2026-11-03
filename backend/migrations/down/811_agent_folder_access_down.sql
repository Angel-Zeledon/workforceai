-- Down of 811_agent_folder_access.sql: back to read-only grants. Rules with another level go first (a
-- Ninguno or an Editar cannot be expressed as a read grant: they are deleted).
DELETE FROM drive_grants WHERE mode <> 'read';
DROP INDEX IF EXISTS drive_grants_agent_path_idx;
DROP INDEX IF EXISTS drive_files_recent_idx;
ALTER TABLE drive_grants DROP CONSTRAINT IF EXISTS drive_grants_mode_check;
ALTER TABLE drive_grants ADD CONSTRAINT drive_grants_mode_check CHECK (mode = 'read');
