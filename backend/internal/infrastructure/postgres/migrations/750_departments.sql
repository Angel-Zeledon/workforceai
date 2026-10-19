-- Corporate organization model, part 1: departments (docs/architecture/organization.md).
--
-- departments: a tree per organization (parent_id, validated for cycles by the application), with a head
--   (a person = user id, or an agent id; head_kind '' = none) and a cost center code.
-- agents.department: a nullable plain-string TAG holding the department NAME. The application keeps it in sync
--   with the department the agent belongs to (agent_jobs.department_id); other features treat it as a label.
--   The column is added idempotently because another stream may add it too; its rollback never drops it.
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

ALTER TABLE agents ADD COLUMN IF NOT EXISTS department TEXT;

CREATE TABLE IF NOT EXISTS departments (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id          TEXT NOT NULL CHECK (char_length(id) BETWEEN 1 AND 64),
    name        TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    parent_id   TEXT NOT NULL DEFAULT '',
    head_kind   TEXT NOT NULL DEFAULT '' CHECK (head_kind IN ('', 'person', 'agent')),
    head_id     TEXT NOT NULL DEFAULT '',
    cost_center TEXT NOT NULL DEFAULT '' CHECK (char_length(cost_center) <= 32),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS departments_name_uq ON departments (org_id, lower(name));

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['departments']
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
