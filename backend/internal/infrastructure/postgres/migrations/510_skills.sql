-- Skill library (S1): reusable know-how for agents in the open Agent Skills
-- format. Metadata and the version manifest live here; the files of every
-- version live in the drive under /Shared/Skills/<name>/v<n>/ (no second blob
-- store). Spec: docs/architecture/skills.md.
--
-- A version is immutable (append-only, like drive_versions). A skill is only
-- ever soft-deleted (deleted_at); creating the same name again brings it back
-- with a new version. A skill never grants tools, connections or approvals.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS skills (
    org_id             TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id                 TEXT NOT NULL,
    name               TEXT NOT NULL CHECK (name ~ '^[a-z0-9-]{1,64}$'),
    description        TEXT NOT NULL CHECK (char_length(description) BETWEEN 1 AND 1024),
    status             TEXT NOT NULL CHECK (status IN ('draft','in_review','active','disabled')),
    source             TEXT NOT NULL CHECK (source IN ('created','imported','from_work')),
    builtin            BOOLEAN NOT NULL DEFAULT false,
    current_version    INT NOT NULL CHECK (current_version >= 1),
    active_version     INT NOT NULL DEFAULT 0 CHECK (active_version >= 0),
    active_description TEXT NOT NULL DEFAULT '',
    created_by         TEXT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_by        TEXT NOT NULL DEFAULT '',
    reviewed_at        TIMESTAMPTZ,
    review_note        TEXT NOT NULL DEFAULT '' CHECK (char_length(review_note) <= 600),
    deleted_at         TIMESTAMPTZ,
    PRIMARY KEY (org_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS skills_live_name_idx ON skills (org_id, lower(name)) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS skills_name_idx ON skills (org_id, lower(name));

CREATE TABLE IF NOT EXISTS skill_versions (
    org_id        TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    skill_id      TEXT NOT NULL,
    version       INT  NOT NULL CHECK (version >= 1),
    author        TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    changelog     TEXT NOT NULL DEFAULT '' CHECK (char_length(changelog) <= 600),
    description   TEXT NOT NULL,
    allowed_tools JSONB NOT NULL DEFAULT '[]',
    files         JSONB NOT NULL DEFAULT '[]',
    warnings      JSONB NOT NULL DEFAULT '[]',
    source        TEXT NOT NULL DEFAULT 'created',
    restored_from INT NOT NULL DEFAULT 0,
    bytes         BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (org_id, skill_id, version),
    FOREIGN KEY (org_id, skill_id) REFERENCES skills (org_id, id) ON DELETE CASCADE
);

-- Assignments: which agent has which skill (role defaults live in the role templates).
CREATE TABLE IF NOT EXISTS skill_assignments (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id    TEXT NOT NULL,
    skill_id    TEXT NOT NULL,
    assigned_by TEXT NOT NULL,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, agent_id, skill_id),
    FOREIGN KEY (org_id, skill_id) REFERENCES skills (org_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS skill_assignments_skill_idx ON skill_assignments (org_id, skill_id);

-- Versions are immutable (a cascade from an operator's delete of an org or skill is the only way rows disappear).
CREATE OR REPLACE FUNCTION skill_versions_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND pg_trigger_depth() > 1 THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'skill_versions is append-only' USING ERRCODE = 'insufficient_privilege';
END
$$;
DROP TRIGGER IF EXISTS skill_versions_no_update ON skill_versions;
CREATE TRIGGER skill_versions_no_update BEFORE UPDATE OR DELETE ON skill_versions
    FOR EACH ROW EXECUTE FUNCTION skill_versions_append_only();

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['skills', 'skill_versions', 'skill_assignments']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE  ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I
                 USING      (org_id = app_current_org())
                 WITH CHECK (org_id = app_current_org())', t);
    END LOOP;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_user') THEN
        GRANT SELECT, INSERT, UPDATE ON skills TO app_user;
        REVOKE DELETE ON skills FROM app_user;
        GRANT SELECT, INSERT ON skill_versions TO app_user;
        REVOKE UPDATE, DELETE ON skill_versions FROM app_user;
        GRANT SELECT, INSERT, DELETE ON skill_assignments TO app_user;
    END IF;
END
$$;
