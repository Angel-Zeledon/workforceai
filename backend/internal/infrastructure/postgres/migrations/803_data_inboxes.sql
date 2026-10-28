-- Data inboxes (docs/architecture/data-inboxes.md, internal/inboxes): a generic inbound endpoint any software
-- can push data to. Each push becomes a file in the drive, attributed to the inbox (principal kind "inbox").
--
-- data_inboxes        one inbox: destination folder, optional customer, accepted kinds, limits. Only the SHA-256
--                     of the token is stored (the plaintext is shown once, at creation and at each rotation).
--                     Revocation is a timestamp, so history is kept (no DELETE for the app role).
-- data_inbox_pushes   metadata of every push (size, written file ids and paths), never the content. idem_key is the
--                     SHA-256 of the caller's X-Idempotency-Key; the unique index makes a duplicate push return the
--                     first result.
--
-- Tenant isolation follows 202_rls_policies.sql with one addition, like 798_client_portal.sql: the public endpoint
-- runs before the tenant is known, so a read-only policy lets a transaction that set app.inbox_hash see the single
-- inbox with that hash and nothing else. Knowing the hash requires knowing the 256-bit token. Everything after that
-- lookup (the push, the drive write) runs in a normal org-scoped transaction. Idempotent.

CREATE TABLE IF NOT EXISTS data_inboxes (
    org_id       TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id           TEXT NOT NULL,
    name         TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    slug         TEXT NOT NULL CHECK (char_length(slug) BETWEEN 1 AND 60),
    folder       TEXT NOT NULL CHECK (char_length(folder) BETWEEN 1 AND 900),
    customer_id  TEXT NOT NULL DEFAULT '',
    allowed      JSONB NOT NULL DEFAULT '["json","csv"]',
    max_bytes    BIGINT NOT NULL CHECK (max_bytes > 0),
    rate_per_min INTEGER NOT NULL DEFAULT 30 CHECK (rate_per_min BETWEEN 1 AND 600),
    token_hash   TEXT NOT NULL UNIQUE CHECK (char_length(token_hash) = 64),
    token_hint   TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    rotated_at   TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    revoked_by   TEXT NOT NULL DEFAULT '',
    last_push_at TIMESTAMPTZ,
    push_count   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS data_inboxes_created_idx ON data_inboxes (org_id, created_at DESC);

CREATE TABLE IF NOT EXISTS data_inbox_pushes (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    inbox_id    TEXT NOT NULL,
    id          TEXT NOT NULL,
    idem_key    TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done')),
    size_bytes  BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    files       JSONB NOT NULL DEFAULT '[]',
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, inbox_id) REFERENCES data_inboxes (org_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS data_inbox_pushes_recent_idx ON data_inbox_pushes (org_id, inbox_id, received_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS data_inbox_pushes_idem_idx ON data_inbox_pushes (org_id, inbox_id, idem_key) WHERE idem_key <> '';

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['data_inboxes', 'data_inbox_pushes']
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
        GRANT SELECT, INSERT, UPDATE ON data_inboxes TO app_user;
        REVOKE DELETE ON data_inboxes FROM app_user;
        -- DELETE only releases a pending push that failed (its idempotency key may be retried)
        GRANT SELECT, INSERT, UPDATE, DELETE ON data_inbox_pushes TO app_user;
    END IF;
END
$$;

-- Token lookup: read-only, and only the row whose hash the transaction announced.
DROP POLICY IF EXISTS inbox_lookup ON data_inboxes;
CREATE POLICY inbox_lookup ON data_inboxes FOR SELECT
    USING (token_hash = NULLIF(current_setting('app.inbox_hash', true), ''));
