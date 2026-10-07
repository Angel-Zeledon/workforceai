-- Cost ledger and budget caps (improvements #2/#3 of docs/competitive-analysis.md).
--
-- usage_entries: one row per runtime call (run-task / consult / synthesize) with
--   the agent charged, so spend can be broken down by agent, task, tool and
--   operation, and an agent's period spend can be summed.
-- budget_caps: hard caps. scope = 'agent' (per-period cap of one agent) or
--   'request' (cap of one request). 'org' stays in organizations.budget_usd.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS usage_entries (
    id            TEXT PRIMARY KEY,
    org_id        TEXT NOT NULL REFERENCES organizations(id),
    request_id    TEXT NOT NULL,
    task_id       TEXT NOT NULL DEFAULT '',
    agent_id      TEXT NOT NULL,
    kind          TEXT NOT NULL,
    model         TEXT NOT NULL DEFAULT '',
    input_tokens  INT NOT NULL DEFAULT 0,
    output_tokens INT NOT NULL DEFAULT 0,
    cost_usd      DOUBLE PRECISION NOT NULL DEFAULT 0,
    tools         JSONB NOT NULL DEFAULT '[]',
    ts            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS usage_entries_org_ts_idx ON usage_entries (org_id, ts DESC);
CREATE INDEX IF NOT EXISTS usage_entries_org_req_idx ON usage_entries (org_id, request_id);
CREATE INDEX IF NOT EXISTS usage_entries_org_agent_idx ON usage_entries (org_id, agent_id, ts DESC);

CREATE TABLE IF NOT EXISTS budget_caps (
    org_id     TEXT NOT NULL REFERENCES organizations(id),
    scope      TEXT NOT NULL CHECK (scope IN ('agent', 'request')),
    scope_id   TEXT NOT NULL,
    cap_usd    DOUBLE PRECISION NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, scope, scope_id)
);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['usage_entries', 'budget_caps']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I
                 USING      (org_id = app_current_org())
                 WITH CHECK (org_id = app_current_org())', t);
        -- 203_app_role.sql only grants on tables that existed when it ran.
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO app_user', t);
        END IF;
    END LOOP;
END
$$;
