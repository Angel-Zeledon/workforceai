-- Rollback of 260_chat.sql. The chat conversations (ids repeated per
-- organization) are removed first so the single-column primary key can return.
DELETE FROM messages      WHERE conversation_id = 'office' OR conversation_id LIKE 'agent:%';
DELETE FROM conversations WHERE id = 'office' OR id LIKE 'agent:%';
DROP INDEX IF EXISTS messages_turn_idx;
ALTER TABLE tasks    DROP COLUMN IF EXISTS assigned_reason;
ALTER TABLE messages DROP COLUMN IF EXISTS request_id;
ALTER TABLE messages DROP COLUMN IF EXISTS reply_to;
ALTER TABLE messages DROP COLUMN IF EXISTS turn_id;
ALTER TABLE conversations DROP CONSTRAINT IF EXISTS conversations_pkey;
ALTER TABLE conversations ADD PRIMARY KEY (id);
