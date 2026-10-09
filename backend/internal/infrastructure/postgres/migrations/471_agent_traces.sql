-- V2 thought audit: one row per runtime call of an agent (redacted, bounded;
-- the prompt is NULL when the organization turned store_prompts off) and the
-- per-organization trace policy. Rows older than TRACE_RETENTION_DAYS (30 by
-- default) are purged by the backend. Idempotent; RLS by org.
CREATE TABLE IF NOT EXISTS agent_traces (
    id         TEXT NOT NULL,
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id   TEXT NOT NULL,
    task_id    TEXT NOT NULL,
    request_id TEXT NOT NULL DEFAULT '',
    attempt    INT  NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    data       JSONB NOT NULL,
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS agent_traces_agent_idx ON agent_traces (org_id, agent_id, created_at DESC);
CREATE INDEX IF NOT EXISTS agent_traces_task_idx ON agent_traces (org_id, task_id, created_at);
CREATE INDEX IF NOT EXISTS agent_traces_created_idx ON agent_traces (org_id, created_at);
CREATE TABLE IF NOT EXISTS trace_settings (
    org_id        TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    store_prompts BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['agent_traces', 'trace_settings']
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
