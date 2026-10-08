-- Web Push subscriptions (browser endpoints of the people who decide approvals).
-- The endpoint is a capability URL: it is stored only to deliver pushes, never
-- returned by the API nor logged. One row per (org, endpoint).
-- Idempotent. Tenant isolation follows 280_artifacts.sql.

CREATE TABLE IF NOT EXISTS push_subscriptions (
    id          TEXT NOT NULL,
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL DEFAULT '',
    endpoint    TEXT NOT NULL,
    p256dh      TEXT NOT NULL,
    auth        TEXT NOT NULL,
    user_agent  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, endpoint)
);

ALTER TABLE push_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE push_subscriptions FORCE  ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON push_subscriptions;
CREATE POLICY tenant_isolation ON push_subscriptions
    USING      (org_id = app_current_org())
    WITH CHECK (org_id = app_current_org());

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON push_subscriptions TO app_user;
    END IF;
END
$$;
