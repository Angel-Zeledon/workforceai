-- Down of 817_drive_offices_root.sql: remove the office folders first, or the old check cannot be restored.
DELETE FROM drive_folders WHERE root_kind = 'offices';
ALTER TABLE drive_folders DROP CONSTRAINT IF EXISTS drive_folders_root_kind_check;
ALTER TABLE drive_folders ADD CONSTRAINT drive_folders_root_kind_check CHECK (root_kind IN ('projects','clients','agents','shared'));
