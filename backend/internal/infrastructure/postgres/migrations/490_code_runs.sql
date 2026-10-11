-- F3 code sandbox (docs/architecture/code-sandbox.md): approved code runs and the
-- per-organization setting that enables code execution (off by default). A run
-- is stored as a JSON document plus the columns the queries need; the status
-- transitions (awaiting_approval -> running -> finished) are compare-and-set so
-- an approval starts a run at most once. Tenant isolation as 202_rls_policies.sql.
CREATE TABLE IF NOT EXISTS code_runs (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id          TEXT NOT NULL,
    approval_id TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL,
    started_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    doc         JSONB NOT NULL,
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS code_runs_approval_idx ON code_runs (org_id, approval_id);
CREATE INDEX IF NOT EXISTS code_runs_started_idx ON code_runs (org_id, started_at);
CREATE INDEX IF NOT EXISTS code_runs_created_idx ON code_runs (org_id, created_at DESC);

CREATE TABLE IF NOT EXISTS code_settings (
    org_id     TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    doc        JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['code_runs', 'code_settings']
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
