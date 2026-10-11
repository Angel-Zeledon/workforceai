-- V2 agent work profile: current profile per agent plus an immutable version
-- history (author, diff, snapshot). Humans edit it; restoring a version appends
-- a new one. Idempotent; tenant isolation follows 202_rls_policies.sql.
CREATE TABLE IF NOT EXISTS agent_profiles (
    org_id       TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id     TEXT NOT NULL,
    instructions TEXT NOT NULL DEFAULT '',
    creativity   TEXT NOT NULL DEFAULT 'medium',
    skills       JSONB NOT NULL DEFAULT '[]',
    tone         TEXT NOT NULL DEFAULT '',
    version      INT  NOT NULL DEFAULT 0,
    updated_by   TEXT NOT NULL DEFAULT '',
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, agent_id)
);
CREATE TABLE IF NOT EXISTS agent_profile_versions (
    org_id        TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id      TEXT NOT NULL,
    version       INT  NOT NULL,
    author        TEXT NOT NULL DEFAULT '',
    restored_from INT,
    diff          JSONB NOT NULL DEFAULT '{}',
    snapshot      JSONB NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, agent_id, version)
);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['agent_profiles', 'agent_profile_versions']
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
