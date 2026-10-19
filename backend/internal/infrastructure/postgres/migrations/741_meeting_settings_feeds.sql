-- Meeting settings and calendar feeds (docs/architecture/meetings.md).
--
-- meeting_settings: one document per organization (default video provider, Jitsi base URL, the
--   calendar connection meetings are written to). No row = defaults.
-- calendar_feeds: ICS subscription tokens, one or more per person. The public token is
--   "<org id>.<random>"; only the SHA-256 of the random part is stored (the token is shown once).
--   A feed URL is a capability: revoking the row kills it. The public endpoint reads the org from
--   the token and then works inside a normal org-scoped transaction (RLS applies).
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS meeting_settings (
    org_id     TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    doc        JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS calendar_feeds (
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id         TEXT NOT NULL,
    user_id    TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    label      TEXT NOT NULL DEFAULT '' CHECK (char_length(label) <= 80),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ,
    PRIMARY KEY (org_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS calendar_feeds_token_idx ON calendar_feeds (org_id, token_hash);
CREATE INDEX IF NOT EXISTS calendar_feeds_user_idx ON calendar_feeds (org_id, user_id);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['meeting_settings', 'calendar_feeds']
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
        GRANT SELECT, INSERT, UPDATE ON meeting_settings, calendar_feeds TO app_user;
    END IF;
END
$$;
