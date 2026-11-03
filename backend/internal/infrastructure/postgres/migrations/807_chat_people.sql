-- E0 "saber quién es quién" (docs/architecture/equipos-oficinas.md sec. 11, chat-history.md):
-- every chat message knows its author, each person has a PRIVATE thread with each agent, and read
-- markers are per person.
--
--   * messages.author_id / author_name: the person who wrote a user message ('' for agents, the system
--     and the history written before this migration).
--   * messages.owner_id / conversations.owner_id: '' = shared (the office channel, request conversations
--     and the 1:1 history written before this migration, kept as read-only "earlier shared history");
--     a user id = the private 1:1 thread of that person (stored conversation id 'agent:<agent>@<user>').
--   * chat_reads.user_id: read markers per person. The rows written before ('' user) stay as the
--     organization-wide baseline: a person's marker is the newest of their own and that baseline.
--
-- Additive and idempotent: an older binary keeps working (it ignores the new columns and keeps
-- writing '' / org-wide markers).

ALTER TABLE messages      ADD COLUMN IF NOT EXISTS author_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE messages      ADD COLUMN IF NOT EXISTS author_name TEXT NOT NULL DEFAULT '';
ALTER TABLE messages      ADD COLUMN IF NOT EXISTS owner_id    TEXT NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS owner_id    TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS messages_owner_idx      ON messages (org_id, owner_id, ts);
CREATE INDEX IF NOT EXISTS conversations_owner_idx ON conversations (org_id, owner_id);

ALTER TABLE chat_reads ADD COLUMN IF NOT EXISTS user_id TEXT NOT NULL DEFAULT '';
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'chat_reads'::regclass AND contype = 'p'
               AND conname = 'chat_reads_pkey'
               AND array_length(conkey, 1) = 2) THEN
        ALTER TABLE chat_reads DROP CONSTRAINT chat_reads_pkey;
        ALTER TABLE chat_reads ADD CONSTRAINT chat_reads_pkey PRIMARY KEY (org_id, user_id, key);
    END IF;
END
$$;
