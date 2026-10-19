-- Approvals from outside the app (docs/architecture/approvals-notify.md).
--
-- notify_settings: notification channels of an organization (one row): e-mail
--   recipients, a Slack and a Microsoft Teams incoming-webhook URL, and the
--   lowest risk that is notified. Webhook URLs are validated against a host
--   allow-list by the application before they are stored.
-- notify_token_use: the replay ledger of the signed approval links. A link is
--   single-use: the first INSERT of (org_id, jti) wins, every later one fails
--   on the primary key. Rows are only needed until the token expires.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS notify_settings (
    org_id         TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    email_enabled  BOOLEAN NOT NULL DEFAULT FALSE,
    email_to       TEXT NOT NULL DEFAULT '' CHECK (char_length(email_to) <= 2000),
    slack_webhook  TEXT NOT NULL DEFAULT '' CHECK (char_length(slack_webhook) <= 500),
    teams_webhook  TEXT NOT NULL DEFAULT '' CHECK (char_length(teams_webhook) <= 500),
    min_risk       TEXT NOT NULL DEFAULT 'low' CHECK (min_risk IN ('low','medium','high')),
    links_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
    locale         TEXT NOT NULL DEFAULT 'es' CHECK (locale IN ('es','en','pt')),
    updated_by     TEXT NOT NULL DEFAULT '',
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS notify_token_use (
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    jti        TEXT NOT NULL CHECK (char_length(jti) BETWEEN 8 AND 64),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, jti)
);
CREATE INDEX IF NOT EXISTS notify_token_use_exp_idx ON notify_token_use (expires_at);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['notify_settings', 'notify_token_use']
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
        GRANT SELECT, INSERT, UPDATE ON notify_settings TO app_user;
        GRANT SELECT, INSERT, DELETE ON notify_token_use TO app_user;
    END IF;
END
$$;
