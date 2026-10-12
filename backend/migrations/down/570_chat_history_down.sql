-- Rollback of 570_chat_history.sql. Messages are untouched; read markers, the
-- archive flag and custom titles are dropped.
DROP TABLE IF EXISTS chat_reads;
DROP INDEX IF EXISTS messages_from_idx;
DROP INDEX IF EXISTS messages_to_idx;
DROP INDEX IF EXISTS conversations_updated_idx;
ALTER TABLE conversations DROP COLUMN IF EXISTS archived_at;
ALTER TABLE conversations DROP COLUMN IF EXISTS title_custom;
