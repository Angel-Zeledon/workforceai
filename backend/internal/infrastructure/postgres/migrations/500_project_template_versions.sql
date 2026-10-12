-- Customizable work templates (G2): the history of an organization template.
-- project_templates (270) keeps the current content of each template as JSONB;
-- every save appends the full content here, so a user can list the versions and
-- restore one. The history goes away with the template (ON DELETE CASCADE).
-- Spec: docs/architecture/08-api.md (project templates), docs/plans/large-workflows.md (G2).
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS project_template_versions (
    org_id      TEXT NOT NULL,
    template_id TEXT NOT NULL,
    version     INT  NOT NULL CHECK (version >= 1),
    saved_by    TEXT NOT NULL DEFAULT '',
    note        TEXT NOT NULL DEFAULT '',
    data        JSONB NOT NULL,
    saved_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, template_id, version),
    FOREIGN KEY (org_id, template_id) REFERENCES project_templates (org_id, id) ON DELETE CASCADE
);

DO $$
BEGIN
    ALTER TABLE project_template_versions ENABLE ROW LEVEL SECURITY;
    ALTER TABLE project_template_versions FORCE  ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON project_template_versions;
    CREATE POLICY tenant_isolation ON project_template_versions
        USING      (org_id = app_current_org())
        WITH CHECK (org_id = app_current_org());
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON project_template_versions TO app_user;
    END IF;
END
$$;
