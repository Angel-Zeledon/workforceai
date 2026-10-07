-- Projects: whole workflows with parallel work (objectives, workflow groups,
-- nodes), launched as ONE orchestrator request. The live state of a launched
-- project is derived from its tasks/approvals at read time, so only the plan, the
-- budget and the gate decisions are stored here (data JSONB, see internal/projects).
-- Spec: docs/architecture/workflow-visualization.md (Appendix A), 08-api.md.
--
-- Ids repeat across organizations (templates, tests), so keys are (org_id, id).
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS projects (
    id         TEXT NOT NULL,
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    status     TEXT NOT NULL DEFAULT 'draft',
    request_id TEXT NOT NULL DEFAULT '',
    data       JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS projects_org_created_idx ON projects (org_id, created_at DESC);
CREATE INDEX IF NOT EXISTS projects_request_idx     ON projects (org_id, request_id) WHERE request_id <> '';

-- Templates saved from a project (the built-in ones live in code).
CREATE TABLE IF NOT EXISTS project_templates (
    id         TEXT NOT NULL,
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    version    INT  NOT NULL DEFAULT 1,
    data       JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['projects', 'project_templates']
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
