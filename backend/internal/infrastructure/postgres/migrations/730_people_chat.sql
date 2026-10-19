-- Person-to-person and group chat (docs/architecture/presence.md, "Chat between people").
--
-- pc_channels: a channel of the organization. kind 'org' is the single all-hands channel (scope_key 'general'),
--   'department' and 'project' are channels tied to a department name / project id (scope_key), 'group' is a
--   private group, 'dm' is a 1:1 conversation between two PEOPLE (scope_key = the sorted pair "userA|userB", unique,
--   so opening a DM twice returns the same channel). last_seq is the per-channel message counter: unread counts are
--   last_seq - last_read_seq, no counting of rows.
-- pc_members: who belongs to a channel. A member is a user or an agent (agents can be members and be mentioned);
--   last_read_seq is that member's read marker. Membership of the 'org' channel is implicit for every person of the
--   organization (a row is created on first access to hold the read marker).
-- pc_messages: the messages, ordered by (channel, seq). mentions is [{"kind":"user|agent","id":...}] resolved by the
--   backend against the channel members; the text is stored as written.
--
-- The two people of a DM must belong to the organization (checked by the service). Isolation follows
-- 202_rls_policies.sql: every table is under RLS by org_id. Idempotent.

CREATE TABLE IF NOT EXISTS pc_channels (
    org_id          TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id              TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('org','department','project','group','dm')),
    name            TEXT NOT NULL DEFAULT '' CHECK (char_length(name) <= 80),
    scope_key       TEXT NOT NULL DEFAULT '' CHECK (char_length(scope_key) <= 200),
    created_by      TEXT NOT NULL DEFAULT '',
    last_seq        BIGINT NOT NULL DEFAULT 0 CHECK (last_seq >= 0),
    last_message_at TIMESTAMPTZ,
    archived        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS pc_channels_scope_idx ON pc_channels (org_id, kind, scope_key)
    WHERE kind IN ('org','dm','department','project') AND scope_key <> '';

CREATE TABLE IF NOT EXISTS pc_members (
    org_id        TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    channel_id    TEXT NOT NULL,
    member_kind   TEXT NOT NULL CHECK (member_kind IN ('user','agent')),
    member_id     TEXT NOT NULL CHECK (char_length(member_id) BETWEEN 1 AND 200),
    role          TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner','member')),
    last_read_seq BIGINT NOT NULL DEFAULT 0 CHECK (last_read_seq >= 0),
    joined_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, channel_id, member_kind, member_id),
    FOREIGN KEY (org_id, channel_id) REFERENCES pc_channels (org_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS pc_members_member_idx ON pc_members (org_id, member_kind, member_id);

CREATE TABLE IF NOT EXISTS pc_messages (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id          TEXT NOT NULL,
    channel_id  TEXT NOT NULL,
    seq         BIGINT NOT NULL CHECK (seq >= 1),
    author_kind TEXT NOT NULL CHECK (author_kind IN ('user','agent')),
    author_id   TEXT NOT NULL,
    body        TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 4000),
    mentions    JSONB NOT NULL DEFAULT '[]',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, channel_id, seq),
    FOREIGN KEY (org_id, channel_id) REFERENCES pc_channels (org_id, id) ON DELETE CASCADE
);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['pc_channels', 'pc_members', 'pc_messages']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I
                 USING      (org_id = app_current_org())
                 WITH CHECK (org_id = app_current_org())', t);
    END LOOP;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON pc_channels, pc_members TO app_user;
        -- messages are append-only for the application: no edit, no delete
        GRANT SELECT, INSERT ON pc_messages TO app_user;
    END IF;
END
$$;
