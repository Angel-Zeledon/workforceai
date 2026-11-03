-- Batches: one project template launched for many customers (docs/superpowers/specs/2026-11-03-proyectos-por-
-- cliente-y-ayuda-design.md part 1). Each customer gets its own normal project (projects.data.batch_id points back
-- here); the batch keeps the list of customers, their place in the queue and the pace (how many run at once). Like
-- projects (270_projects.sql) the batch is a JSONB document. Additive and idempotent; tenant isolation follows
-- 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS project_batches (
    id         TEXT NOT NULL,
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    status     TEXT NOT NULL DEFAULT 'running',
    data       JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS project_batches_created_idx ON project_batches (org_id, created_at DESC);

DO $$
BEGIN
    ALTER TABLE project_batches ENABLE ROW LEVEL SECURITY;
    ALTER TABLE project_batches FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON project_batches;
    CREATE POLICY tenant_isolation ON project_batches USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON project_batches TO app_user;
    END IF;
END
$$;
