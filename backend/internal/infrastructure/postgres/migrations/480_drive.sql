-- The drive (F1): the file system of the organization. Folders (a tree per
-- org), files with immutable versions, read grants for agents and an optional
-- bytea object store for setups without S3.
-- Spec: docs/architecture/drive.md.
--
-- Bytes live in an object store (S3/MinIO, or drive_objects below when no S3 is
-- configured). Postgres keeps metadata, versions and the bounded extracted text
-- that feeds full-text search ('spanish' and 'simple' configurations).
--
-- Nothing is ever hard-deleted by the application: deletes set deleted_at (the
-- trash) and old versions are immutable (append-only for app_user). Paths are
-- unique per org among live folders and (folder, name) among live files, case
-- insensitive. A deleted subtree shares a deleted_batch so restoring the
-- folder brings back exactly what was deleted with it.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS drive_folders (
    org_id        TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id            TEXT NOT NULL,
    parent_id     TEXT,
    name          TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    path          TEXT NOT NULL CHECK (char_length(path) BETWEEN 2 AND 900),
    root_kind     TEXT NOT NULL CHECK (root_kind IN ('projects','clients','agents','shared')),
    ref_id        TEXT NOT NULL DEFAULT '',
    created_by    JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,
    deleted_by    JSONB,
    deleted_batch TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (org_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS drive_folders_live_path_idx ON drive_folders (org_id, lower(path)) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS drive_folders_parent_idx ON drive_folders (org_id, parent_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS drive_folders_ref_idx ON drive_folders (org_id, ref_id) WHERE ref_id <> '';
CREATE INDEX IF NOT EXISTS drive_folders_batch_idx ON drive_folders (org_id, deleted_batch) WHERE deleted_batch <> '';

CREATE TABLE IF NOT EXISTS drive_files (
    org_id          TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id              TEXT NOT NULL,
    folder_id       TEXT NOT NULL,
    name            TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    mime            TEXT NOT NULL,
    size_bytes      BIGINT NOT NULL CHECK (size_bytes >= 0),
    current_version INT NOT NULL CHECK (current_version >= 1),
    sha256          TEXT NOT NULL DEFAULT '',
    tags            JSONB NOT NULL DEFAULT '[]',
    extraction      TEXT NOT NULL DEFAULT 'none',
    search_text     TEXT NOT NULL DEFAULT '' CHECK (char_length(search_text) <= 120000),
    created_by      JSONB NOT NULL,
    updated_by      JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,
    deleted_by      JSONB,
    deleted_batch   TEXT NOT NULL DEFAULT '',
    -- Full-text search over the name and the extracted text of the current version.
    search_es       tsvector GENERATED ALWAYS AS (
        to_tsvector('spanish'::regconfig, regexp_replace(name, '[._-]+', ' ', 'g') || ' ' || search_text)) STORED,
    search_simple   tsvector GENERATED ALWAYS AS (
        to_tsvector('simple'::regconfig, regexp_replace(name, '[._-]+', ' ', 'g') || ' ' || search_text)) STORED,
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, folder_id) REFERENCES drive_folders (org_id, id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS drive_files_live_name_idx ON drive_files (org_id, folder_id, lower(name)) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS drive_files_folder_idx ON drive_files (org_id, folder_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS drive_files_batch_idx ON drive_files (org_id, deleted_batch) WHERE deleted_batch <> '';
CREATE INDEX IF NOT EXISTS drive_files_search_es_idx ON drive_files USING GIN (search_es);
CREATE INDEX IF NOT EXISTS drive_files_search_simple_idx ON drive_files USING GIN (search_simple);

CREATE TABLE IF NOT EXISTS drive_versions (
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    file_id    TEXT NOT NULL,
    version    INT  NOT NULL CHECK (version >= 1),
    object_key TEXT NOT NULL,
    sha256     TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    mime       TEXT NOT NULL,
    author     JSONB NOT NULL,
    message    TEXT NOT NULL DEFAULT '' CHECK (char_length(message) <= 600),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, file_id, version),
    FOREIGN KEY (org_id, file_id) REFERENCES drive_files (org_id, id) ON DELETE CASCADE
);

-- Read grants of agents on folders outside their own areas (writes there always
-- need approval, a grant never lifts that).
CREATE TABLE IF NOT EXISTS drive_grants (
    org_id     TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id         TEXT NOT NULL,
    agent_id   TEXT NOT NULL,
    path       TEXT NOT NULL,
    mode       TEXT NOT NULL DEFAULT 'read' CHECK (mode = 'read'),
    created_by JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ,
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS drive_grants_agent_idx ON drive_grants (org_id, agent_id) WHERE revoked_at IS NULL;

-- Object store fallback (bytea) for deployments without S3-compatible storage.
-- Keys are the object keys of internal/objstore ("orgs/<org>/...").
CREATE TABLE IF NOT EXISTS drive_objects (
    org_id       TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key          TEXT NOT NULL,
    data         BYTEA NOT NULL,
    size_bytes   BIGINT NOT NULL,
    sha256       TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, key),
    CHECK (octet_length(data) = size_bytes)
);

-- Versions are immutable for everybody (cascade from an operator's DDL-level
-- delete of an org or a file is the only way rows disappear).
CREATE OR REPLACE FUNCTION drive_versions_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND pg_trigger_depth() > 1 THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'drive_versions is append-only' USING ERRCODE = 'insufficient_privilege';
END
$$;
DROP TRIGGER IF EXISTS drive_versions_no_update ON drive_versions;
CREATE TRIGGER drive_versions_no_update BEFORE UPDATE OR DELETE ON drive_versions
    FOR EACH ROW EXECUTE FUNCTION drive_versions_append_only();

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['drive_folders', 'drive_files', 'drive_versions', 'drive_grants', 'drive_objects']
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
        GRANT SELECT, INSERT, UPDATE ON drive_folders, drive_files, drive_grants TO app_user;
        REVOKE DELETE ON drive_folders, drive_files, drive_grants FROM app_user;
        GRANT SELECT, INSERT ON drive_versions TO app_user;
        REVOKE UPDATE, DELETE ON drive_versions FROM app_user;
        GRANT SELECT, INSERT, UPDATE, DELETE ON drive_objects TO app_user;
    END IF;
END
$$;
