-- Puestos dentro del departamento (docs/architecture/positions.md): a position belongs to one department (office),
-- may report to another position of the same department, and carries a package: skills (ids of the skill library) and
-- a library (drive folders with a level none | read | edit). An agent holds at most one position; office_id of the
-- assignment is the department it got it in (an agent moved elsewhere loses it).
--
-- skills and library are JSON arrays validated by the service (positions.Service.normalize): skills of the company or
-- of that department, folders inside the department, /Shared or shared with it. No foreign key to offices (a closed
-- office keeps its rows, like skills.office_id); reports_to points inside the same organization.
--
-- Additive and idempotent. Tenant isolation follows 202_rls_policies.sql. Down:
-- backend/migrations/down/830_office_positions_down.sql.

CREATE TABLE IF NOT EXISTS office_positions (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id          TEXT NOT NULL,
    office_id   TEXT NOT NULL,
    name        TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    reports_to  TEXT NOT NULL DEFAULT '',
    skills      JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(skills) = 'array'),
    library     JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(library) = 'array'),
    template    TEXT NOT NULL DEFAULT '' CHECK (template IN ('', 'manager', 'assistant', 'specialist')),
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by  TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    CHECK (reports_to <> id)
);
CREATE INDEX IF NOT EXISTS office_positions_office_idx ON office_positions (org_id, office_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS office_positions_name_idx ON office_positions (org_id, office_id, lower(name));

CREATE TABLE IF NOT EXISTS agent_positions (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id    TEXT NOT NULL,
    position_id TEXT NOT NULL,
    office_id   TEXT NOT NULL,
    assigned_by TEXT NOT NULL DEFAULT '',
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, agent_id),
    FOREIGN KEY (org_id, position_id) REFERENCES office_positions (org_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS agent_positions_position_idx ON agent_positions (org_id, position_id);

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['office_positions', 'agent_positions'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org())', t);
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO app_user', t);
        END IF;
    END LOOP;
END
$$;
