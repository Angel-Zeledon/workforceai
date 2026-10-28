-- Two-way Slack channel for humans (docs/architecture/channels.md, internal/slackapp).
--
-- slack_app_settings   the organization's own Slack app (bring-your-own). The signing secret and the bot token are
--                      stored ONLY sealed with the vault (internal/vault Seal, purposes slackapp-signing-secret /
--                      slackapp-bot-token); there is no plaintext column. team_id is the workspace of the bot token:
--                      inbound requests of any other workspace are refused.
-- slack_user_links     Slack user (team_id, slack_user_id) -> platform user, created by a one-time code issued in the
--                      app. A link never carries a role: the current membership role is read on every decision.
-- slack_link_codes     one-time link codes (hash only), 10-minute validity.
-- slack_interactions   dedupe of button presses (a retried or double-clicked interaction decides once), pruned daily.
-- slack_approval_messages  where an approval request was posted, to update the message when the approval moves.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.
CREATE TABLE IF NOT EXISTS slack_app_settings (
    org_id             TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    enabled            BOOLEAN NOT NULL DEFAULT false,
    signing_secret_enc BYTEA,
    bot_token_enc      BYTEA,
    token_last4        TEXT NOT NULL DEFAULT '',
    team_id            TEXT NOT NULL DEFAULT '',
    team_name          TEXT NOT NULL DEFAULT '',
    bot_user_id        TEXT NOT NULL DEFAULT '',
    channel_id         TEXT NOT NULL DEFAULT '',
    min_risk           TEXT NOT NULL DEFAULT 'medium' CHECK (min_risk IN ('low', 'medium', 'high')),
    locale             TEXT NOT NULL DEFAULT 'es' CHECK (locale IN ('es', 'en', 'pt')),
    email_matching     BOOLEAN NOT NULL DEFAULT true,
    high_risk_in_app   BOOLEAN NOT NULL DEFAULT true,
    verified_at        TIMESTAMPTZ,
    updated_by         TEXT NOT NULL DEFAULT '',
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS slack_user_links (
    org_id        TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    team_id       TEXT NOT NULL,
    slack_user_id TEXT NOT NULL,
    user_id       TEXT NOT NULL,
    method        TEXT NOT NULL DEFAULT 'code' CHECK (method IN ('code', 'email')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, team_id, slack_user_id)
);
CREATE INDEX IF NOT EXISTS slack_user_links_user_idx ON slack_user_links (org_id, user_id);

CREATE TABLE IF NOT EXISTS slack_link_codes (
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    user_id    TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, code_hash)
);

CREATE TABLE IF NOT EXISTS slack_interactions (
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    dedupe_key TEXT NOT NULL,
    outcome    TEXT NOT NULL DEFAULT 'processing',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, dedupe_key)
);

CREATE TABLE IF NOT EXISTS slack_approval_messages (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    approval_id TEXT NOT NULL,
    channel_id  TEXT NOT NULL,
    message_ts  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, approval_id)
);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['slack_app_settings', 'slack_user_links', 'slack_link_codes', 'slack_interactions', 'slack_approval_messages']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org())', t);
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO app_user', t);
        END IF;
    END LOOP;
END
$$;
