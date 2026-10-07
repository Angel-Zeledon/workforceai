-- Agent workspaces: artifacts (sheet, doc, table, board, chart, pdf, form, inbox,
-- agenda) with immutable versions and links between artifacts.
-- Spec: docs/architecture/agent-workspaces.md sec. 5 and 7, 08-api.md.
--
-- artifacts holds the head (meta + canonical aiw.<kind>/1 content). Every change
-- appends a row to artifact_versions, which is append-only for the application
-- role (no UPDATE/DELETE) and a trigger keeps it so for everybody. A save only
-- succeeds if the head version is the one the writer based its change on
-- (optimistic concurrency, enforced in the UPDATE ... WHERE head_version = $n).
-- artifact_links are the references between artifacts (embeds, source_of,
-- derived_from, refers_to); `auto` links are derived from the content on every
-- save, synced_version is the source version the dependent last recalculated against.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS artifacts (
    id                TEXT NOT NULL,
    org_id            TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind              TEXT NOT NULL CHECK (kind IN ('sheet','doc','table','board','chart','pdf','form','inbox','agenda')),
    title             TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','in_review','approved','sent','archived')),
    head_version      INT  NOT NULL DEFAULT 1,
    created_by        JSONB NOT NULL,
    last_author       JSONB NOT NULL,
    task_id           TEXT,
    request_id        TEXT,
    customer_id       TEXT,
    project_id        TEXT,
    deliverable_id    TEXT,
    progress          INT  NOT NULL DEFAULT 100,
    build_state       TEXT NOT NULL DEFAULT 'done' CHECK (build_state IN ('queued','building','ready_for_review','done','blocked')),
    locale            TEXT NOT NULL DEFAULT 'es',
    pending_proposals INT  NOT NULL DEFAULT 0,
    tainted           BOOLEAN NOT NULL DEFAULT false,
    locked            BOOLEAN NOT NULL DEFAULT false,
    size_bytes        INT  NOT NULL DEFAULT 0,
    attachments       JSONB NOT NULL DEFAULT '[]',
    content           JSONB NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS artifacts_org_updated_idx ON artifacts (org_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS artifacts_org_project_idx ON artifacts (org_id, project_id) WHERE project_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS artifact_versions (
    org_id       TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    artifact_id  TEXT NOT NULL,
    version      INT  NOT NULL,
    base_version INT,
    author       JSONB NOT NULL,
    source       TEXT NOT NULL,   -- create|human_edit|agent_task|rebase|restore|recalc|accept_proposal|import
    summary      TEXT NOT NULL DEFAULT '',
    task_id      TEXT,
    content_hash TEXT NOT NULL DEFAULT '',
    content      JSONB NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, artifact_id, version),
    FOREIGN KEY (org_id, artifact_id) REFERENCES artifacts (org_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS artifact_links (
    id             TEXT NOT NULL,
    org_id         TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    from_id        TEXT NOT NULL,
    to_id          TEXT NOT NULL,
    relation       TEXT NOT NULL CHECK (relation IN ('embeds','source_of','derived_from','refers_to')),
    anchor         JSONB,
    alias          TEXT,
    auto           BOOLEAN NOT NULL DEFAULT false,
    synced_version INT NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS artifact_links_from_idx ON artifact_links (org_id, from_id);
CREATE INDEX IF NOT EXISTS artifact_links_to_idx   ON artifact_links (org_id, to_id);

-- Versions are immutable: nobody updates or deletes them (an artifact is
-- archived, never deleted, so the cascade of ON DELETE is not reachable from
-- the application role, which has no DELETE on artifacts either).
CREATE OR REPLACE FUNCTION artifact_versions_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND pg_trigger_depth() > 1 THEN
        RETURN OLD; -- cascade from a deleted artifact (an operator's DDL-level act)
    END IF;
    RAISE EXCEPTION 'artifact_versions is append-only' USING ERRCODE = 'insufficient_privilege';
END
$$;

DROP TRIGGER IF EXISTS artifact_versions_no_update ON artifact_versions;
CREATE TRIGGER artifact_versions_no_update BEFORE UPDATE OR DELETE ON artifact_versions
    FOR EACH ROW EXECUTE FUNCTION artifact_versions_append_only();

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['artifacts', 'artifact_versions', 'artifact_links']
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
        GRANT SELECT, INSERT, UPDATE ON artifacts TO app_user;
        GRANT SELECT, INSERT, UPDATE, DELETE ON artifact_links TO app_user;
        GRANT SELECT, INSERT ON artifact_versions TO app_user;
        REVOKE DELETE ON artifacts FROM app_user;
        REVOKE UPDATE, DELETE ON artifact_versions FROM app_user;
    END IF;
END
$$;
