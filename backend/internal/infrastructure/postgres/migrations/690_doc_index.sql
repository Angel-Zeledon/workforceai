-- Passage index of the drive (capability 6, "big files"; docs/architecture/agent-search.md).
--
-- doc_chunks: passages of the CURRENT version of live files, with their page
--   (from "[page N]" markers), line, character offsets, a combined tsvector
--   (the chunk's language stemming + 'simple', computed at insert) under a GIN
--   index, a content hash (embeddings are reused across versions) and an
--   optional embedding as real[] (plain postgres:16, no pgvector: the cosine is
--   computed in the backend over a prefiltered candidate set).
-- doc_index_state: one row per file: which version is indexed and how
--   (pending / indexing / indexed / skipped / failed + reason); claimed by the
--   workers with FOR UPDATE SKIP LOCKED and a lease (several replicas).
-- doc_index_settings: indexing on/off per organization (no row = on).
-- doc_embed_usage: embedding tokens per organization and UTC day (daily cap).
--
-- The index is derived data: rebuilt from the drive at any time, so the
-- application role may delete it. Searches join drive_files / drive_folders so
-- a file in the trash or an old version is never a hit even before the worker
-- cleans up. There is deliberately no foreign key to drive_files (drive rows
-- are never hard-deleted by the application, the reconcile removes orphans,
-- and the drive migrations stay independently reversible). Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE TABLE IF NOT EXISTS doc_chunks (
    org_id       TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    file_id      TEXT NOT NULL,
    version      INT  NOT NULL CHECK (version >= 1),
    chunk_idx    INT  NOT NULL CHECK (chunk_idx >= 0),
    page         INT CHECK (page IS NULL OR page >= 1),
    line_no      INT,
    start_off    INT  NOT NULL CHECK (start_off >= 0),
    end_off      INT  NOT NULL CHECK (end_off >= start_off),
    heading      TEXT NOT NULL DEFAULT '' CHECK (char_length(heading) <= 300),
    text         TEXT NOT NULL CHECK (char_length(text) <= 4000),
    lang         TEXT NOT NULL DEFAULT 'simple' CHECK (lang IN ('simple','spanish','english','portuguese')),
    tsv          tsvector NOT NULL,
    content_hash TEXT NOT NULL,
    embedding    real[],
    embed_model  TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, file_id, version, chunk_idx),
    CHECK (embedding IS NULL OR cardinality(embedding) BETWEEN 1 AND 8192)
);
CREATE INDEX IF NOT EXISTS doc_chunks_tsv_idx ON doc_chunks USING GIN (tsv);
CREATE INDEX IF NOT EXISTS doc_chunks_file_idx ON doc_chunks (org_id, file_id, version);
CREATE INDEX IF NOT EXISTS doc_chunks_embedded_idx ON doc_chunks (org_id, embed_model) WHERE embedding IS NOT NULL;

CREATE TABLE IF NOT EXISTS doc_index_state (
    org_id      TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    file_id     TEXT NOT NULL,
    version     INT  NOT NULL CHECK (version >= 1),
    status      TEXT NOT NULL CHECK (status IN ('pending','indexing','indexed','skipped','failed')),
    reason      TEXT NOT NULL DEFAULT '' CHECK (char_length(reason) <= 60),
    chunks      INT  NOT NULL DEFAULT 0,
    embedded    INT  NOT NULL DEFAULT 0,
    embed_model TEXT NOT NULL DEFAULT '',
    attempts    INT  NOT NULL DEFAULT 0,
    lease_until TIMESTAMPTZ,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, file_id)
);
CREATE INDEX IF NOT EXISTS doc_index_state_work_idx ON doc_index_state (org_id, updated_at) WHERE status IN ('pending','indexing');

CREATE TABLE IF NOT EXISTS doc_index_settings (
    org_id     TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS doc_embed_usage (
    org_id   TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    day      DATE NOT NULL,
    tokens   BIGINT NOT NULL DEFAULT 0,
    calls    INT NOT NULL DEFAULT 0,
    cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
    PRIMARY KEY (org_id, day)
);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['doc_chunks', 'doc_index_state', 'doc_index_settings', 'doc_embed_usage']
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
        GRANT SELECT, INSERT, UPDATE, DELETE ON doc_chunks, doc_index_state TO app_user;
        GRANT SELECT, INSERT, UPDATE ON doc_index_settings, doc_embed_usage TO app_user;
        REVOKE DELETE ON doc_index_settings, doc_embed_usage FROM app_user;
    END IF;
END
$$;
