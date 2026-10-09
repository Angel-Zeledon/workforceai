-- Plan changes (Q2): proposals to change the plan of a running project (add,
-- replace, remove or update tasks). Agents only propose; a human approves via
-- the approvals mechanism (kind plan_change). The proposal is a JSONB document
-- (see internal/projects/planchange.go); status and project are columns for
-- filtering. Keys are (org_id, id). Idempotent. Tenant isolation follows
-- 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS plan_changes (
    id         TEXT NOT NULL,
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'pending',
    data       JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS plan_changes_project_idx ON plan_changes (org_id, project_id, created_at);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['plan_changes']
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
