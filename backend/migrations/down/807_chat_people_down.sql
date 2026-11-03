-- Reverts 807_chat_people.sql. The private 1:1 threads ('agent:<agent>@<user>') and their messages stay in
-- the tables: an older binary does not list them (they are not 'agent:<id>'), nothing is lost.
DELETE FROM chat_reads WHERE user_id <> '';
ALTER TABLE chat_reads DROP CONSTRAINT IF EXISTS chat_reads_pkey;
ALTER TABLE chat_reads ADD CONSTRAINT chat_reads_pkey PRIMARY KEY (org_id, key);
ALTER TABLE chat_reads DROP COLUMN IF EXISTS user_id;
DROP INDEX IF EXISTS conversations_owner_idx;
DROP INDEX IF EXISTS messages_owner_idx;
ALTER TABLE conversations DROP COLUMN IF EXISTS owner_id;
ALTER TABLE messages DROP COLUMN IF EXISTS owner_id;
ALTER TABLE messages DROP COLUMN IF EXISTS author_name;
ALTER TABLE messages DROP COLUMN IF EXISTS author_id;
