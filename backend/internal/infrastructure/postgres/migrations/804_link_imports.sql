-- Link imports (docs/architecture/link-imports.md, internal/linkimports): "import from a link". An administrator
-- pastes a URL that returns data and the platform downloads it into the drive as a versioned file, once or on a
-- schedule. Each run writes a new version only when the content changed, attributed to the import (principal kind
-- "link_import").
--
-- link_imports      one import: the link, destination folder, optional customer, schedule and the outcome of the
--                   last run. header_sealed is the optional auth header value sealed with the organization's data
--                   key (vault Seal, CONNECTIONS_KEK); only its name and last 4 characters are ever shown.
--                   next_run_at is claimed with a compare-and-set before a scheduled run, so two schedulers never
--                   run the same period twice.
-- link_import_runs  metadata of every run (status ok / unchanged / error / skipped, code, size, sha256, file id),
--                   never the content. The store keeps the last 200 runs per import.
--
-- Tenant isolation follows 202_rls_policies.sql (org_id = app_current_org()). The scheduler lists due imports
-- organization by organization (no cross-tenant query). Idempotent.

CREATE TABLE IF NOT EXISTS link_imports (
    org_id        TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id            TEXT NOT NULL,
    name          TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    slug          TEXT NOT NULL CHECK (char_length(slug) BETWEEN 1 AND 60),
    url           TEXT NOT NULL CHECK (char_length(url) BETWEEN 8 AND 2048),
    folder        TEXT NOT NULL CHECK (char_length(folder) BETWEEN 1 AND 900),
    customer_id   TEXT NOT NULL DEFAULT '',
    file_stem     TEXT NOT NULL DEFAULT '' CHECK (char_length(file_stem) <= 80),
    schedule      TEXT NOT NULL DEFAULT 'once' CHECK (schedule IN ('once', 'hourly', 'daily', 'weekly')),
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    header_name   TEXT NOT NULL DEFAULT '' CHECK (char_length(header_name) <= 64),
    header_sealed BYTEA,
    header_hint   TEXT NOT NULL DEFAULT '' CHECK (char_length(header_hint) <= 4),
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    next_run_at   TIMESTAMPTZ,
    last_run_at   TIMESTAMPTZ,
    last_status   TEXT NOT NULL DEFAULT '',
    last_code     TEXT NOT NULL DEFAULT '',
    last_hash     TEXT NOT NULL DEFAULT '',
    last_file_id  TEXT NOT NULL DEFAULT '',
    last_path     TEXT NOT NULL DEFAULT '',
    last_version  INTEGER NOT NULL DEFAULT 0,
    last_data_at  TIMESTAMPTZ,
    run_count     BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (org_id, id)
);
CREATE INDEX IF NOT EXISTS link_imports_created_idx ON link_imports (org_id, created_at DESC);
CREATE INDEX IF NOT EXISTS link_imports_due_idx ON link_imports (org_id, next_run_at) WHERE enabled AND schedule <> 'once';

CREATE TABLE IF NOT EXISTS link_import_runs (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    import_id   TEXT NOT NULL,
    id          TEXT NOT NULL,
    trigger     TEXT NOT NULL CHECK (trigger IN ('manual', 'schedule')),
    actor       TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL CHECK (status IN ('ok', 'unchanged', 'error', 'skipped')),
    code        TEXT NOT NULL DEFAULT '',
    http_status INTEGER NOT NULL DEFAULT 0,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    duration_ms BIGINT NOT NULL DEFAULT 0,
    size_bytes  BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    mime        TEXT NOT NULL DEFAULT '',
    sha256      TEXT NOT NULL DEFAULT '',
    file_id     TEXT NOT NULL DEFAULT '',
    path        TEXT NOT NULL DEFAULT '',
    version     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, import_id) REFERENCES link_imports (org_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS link_import_runs_recent_idx ON link_import_runs (org_id, import_id, started_at DESC);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['link_imports', 'link_import_runs']
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
        -- DELETE: an administrator deletes an import (its runs go with it); old runs are pruned.
        GRANT SELECT, INSERT, UPDATE, DELETE ON link_imports TO app_user;
        GRANT SELECT, INSERT, UPDATE, DELETE ON link_import_runs TO app_user;
    END IF;
END
$$;
