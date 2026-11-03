-- Reverts 808_usage_person.sql.
DROP INDEX IF EXISTS usage_entries_org_user_idx;
ALTER TABLE usage_entries DROP COLUMN IF EXISTS user_id;
