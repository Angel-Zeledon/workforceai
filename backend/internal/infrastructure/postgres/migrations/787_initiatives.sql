-- Proactive employees (B2, docs/architecture/initiative.md): suggestions raised by agents and the per-organization
-- initiative budget.
--
-- A suggestion is only a notification the humans accept or dismiss. Accepting submits a normal request, so approvals,
-- spend caps, kill switch, read-only mode, pause and audit still apply. Rows are never deleted by the application:
-- accepted and dismissed ones are the history and count against the daily / monthly budget.
-- signal + signal_key identify the subject of the suggestion; the same pair is not raised again inside the dedupe window.
-- agent_daily holds the per-agent daily overrides as {"agent-id": n}.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.
CREATE TABLE IF NOT EXISTS initiatives (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id          TEXT NOT NULL,
    agent_id    TEXT NOT NULL,
    signal      TEXT NOT NULL,
    signal_key  TEXT NOT NULL DEFAULT '',
    title       TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    prompt      TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'dismissed')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    resolved_by TEXT NOT NULL DEFAULT '',
    request_id  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS initiatives_recent_idx ON initiatives (org_id, created_at DESC);
CREATE INDEX IF NOT EXISTS initiatives_signal_idx ON initiatives (org_id, signal, signal_key, created_at DESC);

CREATE TABLE IF NOT EXISTS initiative_settings (
    org_id        TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    enabled       BOOLEAN NOT NULL DEFAULT true,
    daily_limit   INT NOT NULL DEFAULT 3 CHECK (daily_limit >= 0),
    monthly_limit INT NOT NULL DEFAULT 30 CHECK (monthly_limit >= 0),
    agent_daily   JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
    ALTER TABLE initiatives ENABLE ROW LEVEL SECURITY;
    ALTER TABLE initiatives FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON initiatives;
    CREATE POLICY tenant_isolation ON initiatives USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    ALTER TABLE initiative_settings ENABLE ROW LEVEL SECURITY;
    ALTER TABLE initiative_settings FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON initiative_settings;
    CREATE POLICY tenant_isolation ON initiative_settings USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON initiatives TO app_user;
        REVOKE DELETE ON initiatives FROM app_user;
        GRANT SELECT, INSERT, UPDATE, DELETE ON initiative_settings TO app_user;
    END IF;
END
$$;
