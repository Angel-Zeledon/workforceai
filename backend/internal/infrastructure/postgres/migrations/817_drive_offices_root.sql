-- Company edition (D1): the drive gets a fourth well-known root, /Offices/<office>, the sealed folder of each office.
-- drive_folders.root_kind accepts 'offices'. Additive and idempotent. Down: backend/migrations/down/817_drive_offices_root_down.sql.

ALTER TABLE drive_folders DROP CONSTRAINT IF EXISTS drive_folders_root_kind_check;
ALTER TABLE drive_folders ADD CONSTRAINT drive_folders_root_kind_check CHECK (root_kind IN ('projects','clients','agents','shared','offices'));
