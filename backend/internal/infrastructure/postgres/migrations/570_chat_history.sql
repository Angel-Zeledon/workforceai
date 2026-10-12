-- Chat history (E1): the user comes back to the chats with the office ("General")
-- and with each agent. Spec: docs/architecture/chat-history.md.
--
-- There is no second copy of any message: the General stream and the thread of
-- an agent are VIEWS over `messages`. This migration only adds
--   * conversations.archived_at / title_custom: archive and rename,
--   * chat_reads: read markers (org-wide: 'thread:<general|agent id>', 'conv:<id>'),
--   * indexes for the "messages this agent sent or received" lookups.
-- Existing history is seeded as already read so nobody opens to a wall of badges.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql (RLS by org_id).

ALTER TABLE conversations ADD COLUMN IF NOT EXISTS archived_at  TIMESTAMPTZ;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS title_custom BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS chat_reads (
    org_id  TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key     TEXT NOT NULL,
    read_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, key)
);

CREATE INDEX IF NOT EXISTS messages_from_idx ON messages (org_id, from_id, ts);
CREATE INDEX IF NOT EXISTS messages_to_idx   ON messages (org_id, to_id, ts);
CREATE INDEX IF NOT EXISTS conversations_updated_idx ON conversations (org_id, last_message_at DESC);

INSERT INTO chat_reads (org_id, key, read_at)
    SELECT org_id, 'conv:' || id, now() FROM conversations
    ON CONFLICT DO NOTHING;
INSERT INTO chat_reads (org_id, key, read_at)
    SELECT DISTINCT org_id, 'thread:general', now() FROM conversations
    ON CONFLICT DO NOTHING;
INSERT INTO chat_reads (org_id, key, read_at)
    SELECT org_id, 'thread:' || id, now() FROM agents
    ON CONFLICT DO NOTHING;

DO $$
BEGIN
    ALTER TABLE chat_reads ENABLE ROW LEVEL SECURITY;
    ALTER TABLE chat_reads FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON chat_reads;
    CREATE POLICY tenant_isolation ON chat_reads USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON chat_reads TO app_user;
    END IF;
END
$$;
