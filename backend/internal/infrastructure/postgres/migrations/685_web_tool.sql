-- Web tool (docs/architecture/agent-web.md): the per-organization setting (off by default) and the ledger of
-- calls (query or URL only, never page content) behind the daily and per-task quotas and the usage view.
-- Idempotent. Tenant isolation as 202_rls_policies.sql.
CREATE TABLE IF NOT EXISTS web_settings (
    org_id     TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    doc        JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS web_calls (
    org_id   TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id       TEXT NOT NULL,
    agent_id TEXT NOT NULL DEFAULT '',
    task_id  TEXT NOT NULL DEFAULT '',
    action   TEXT NOT NULL,
    target   TEXT NOT NULL DEFAULT '',
    at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS web_calls_at_idx ON web_calls (org_id, at);
CREATE INDEX IF NOT EXISTS web_calls_task_idx ON web_calls (org_id, task_id);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['web_settings', 'web_calls']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I
                 USING      (org_id = app_current_org())
                 WITH CHECK (org_id = app_current_org())', t);
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO app_user', t);
        END IF;
    END LOOP;
END
$$;
