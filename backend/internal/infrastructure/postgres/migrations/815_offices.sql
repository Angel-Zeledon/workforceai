-- E2 "Oficinas" (docs/architecture/equipos-oficinas.md §3, §4, §8; docs/superpowers/plans/2026-11-03-e2-oficinas.md).
--   offices         a department of the org chart with the office turned on (open or private).
--   office_members  who is in each office and who is its responsible (lead).
--   agent_access    the explicit owner (a person or an office) and visibility of an agent; no row = the defaults
--                   (the office of its department, or General, visibility "office": everybody keeps what they see).
--   team_notes      memory of an office, of a person, or of the company (a lead promoted it); soft-deleted.
--   office_layouts  the 3D layout of each office (General keeps office_layout).
-- "General" is virtual (no row). Organizations without rows behave exactly as before.
--
-- Additive and idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS offices (
    org_id        TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    department_id TEXT NOT NULL,
    visibility    TEXT NOT NULL DEFAULT 'open' CHECK (visibility IN ('open', 'private')),
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, department_id)
);

CREATE TABLE IF NOT EXISTS office_members (
    org_id    TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    office_id TEXT NOT NULL,
    user_id   TEXT NOT NULL,
    role      TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('lead', 'member')),
    added_by  TEXT NOT NULL DEFAULT '',
    added_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, office_id, user_id)
);
CREATE INDEX IF NOT EXISTS office_members_user_idx ON office_members (org_id, user_id);

CREATE TABLE IF NOT EXISTS agent_access (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id    TEXT NOT NULL,
    owner_kind  TEXT NOT NULL CHECK (owner_kind IN ('person', 'office')),
    owner_id    TEXT NOT NULL,
    home_office TEXT NOT NULL DEFAULT '',
    visibility  TEXT NOT NULL DEFAULT 'office' CHECK (visibility IN ('private', 'office', 'people', 'directory')),
    shares      JSONB NOT NULL DEFAULT '[]'::jsonb,
    updated_by  TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, agent_id)
);

CREATE TABLE IF NOT EXISTS team_notes (
    id            TEXT PRIMARY KEY,
    org_id        TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    scope         TEXT NOT NULL CHECK (scope IN ('office', 'person', 'company')),
    owner_ref     TEXT NOT NULL DEFAULT '',
    text          TEXT NOT NULL CHECK (char_length(text) BETWEEN 1 AND 1000),
    promoted_from TEXT NOT NULL DEFAULT '',
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,
    deleted_by    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS team_notes_scope_idx ON team_notes (org_id, scope, owner_ref, created_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS office_layouts (
    org_id     TEXT  NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    office_id  TEXT  NOT NULL,
    doc        JSONB NOT NULL,
    version    INT   NOT NULL CHECK (version >= 1),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by TEXT  NOT NULL DEFAULT '',
    PRIMARY KEY (org_id, office_id)
);

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['offices', 'office_members', 'agent_access', 'team_notes', 'office_layouts'] LOOP
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
