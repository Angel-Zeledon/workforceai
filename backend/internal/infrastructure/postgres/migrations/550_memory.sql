-- Long-term memory (M1): a full-text index over what the organization already
-- did, so agents remember past work across weeks and large projects.
-- Spec: docs/architecture/memory.md.
--
--   memory_docs  completed task outputs, reports and the lessons ("notes") an
--                agent declared. Files are NOT copied: the drive already keeps
--                its extracted text, and the new accent-insensitive vector
--                drive_files.search_mem is maintained by Postgres on every
--                write (generated column), so a file is searchable the moment
--                it is saved and can never be stale after a move or a delete.
--
-- Text search: Spanish is primary (stemming + stop words), English words match
-- by exact form. mem_fold() lowercases, removes accents and thousands
-- separators; internal/textfold does the same on the query side, rune for
-- rune, so "planeación" == "planeacion" and "$50,000" == "50000".
--
-- Nothing here is a vector. Embeddings (pgvector) will be a second Retriever
-- behind the same interface (internal/memory), with their own migration.
--
-- Idempotent. Tenant isolation follows 202_rls_policies.sql.

CREATE OR REPLACE FUNCTION mem_fold(t text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT regexp_replace(
        lower(translate(t, 'ÁÀÄÂÃáàäâãÉÈËÊéèëêÍÌÏÎíìïîÓÒÖÔÕóòöôõÚÙÜÛúùüûÑñÇç', 'AAAAAaaaaaEEEEeeeeIIIIiiiiOOOOOoooooUUUUuuuuNnCc')),
        '(\d)[.,](?=\d{3}(?!\d))', '\1', 'g')
$$;

CREATE TABLE IF NOT EXISTS memory_docs (
    org_id        TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id            TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('task','report','note')),
    ref           TEXT NOT NULL,                      -- task:<id> | report:<id> | memory:<id>
    title         TEXT NOT NULL DEFAULT '' CHECK (char_length(title) <= 300),
    body          TEXT NOT NULL DEFAULT '' CHECK (char_length(body) <= 20000),
    agent_id      TEXT NOT NULL DEFAULT '',           -- who produced it
    -- 'org': organization knowledge (task outputs, reports); 'agent': private to agent_id (notes).
    scope         TEXT NOT NULL DEFAULT 'org' CHECK (scope IN ('org','agent')),
    project_id    TEXT NOT NULL DEFAULT '',
    project_name  TEXT NOT NULL DEFAULT '',
    request_id    TEXT NOT NULL DEFAULT '',
    source_ref    TEXT NOT NULL DEFAULT '',           -- where a note came from (task:<id>)
    -- A person deleted it from the agent's memory: kept as a tombstone so a
    -- backfill or a re-index never brings it back.
    suppressed    BOOLEAN NOT NULL DEFAULT false,
    suppressed_by TEXT NOT NULL DEFAULT '',
    suppressed_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    tsv           tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('spanish'::regconfig, mem_fold(title)), 'A') ||
        setweight(to_tsvector('spanish'::regconfig, mem_fold(body)), 'B')) STORED,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, ref)
);
CREATE INDEX IF NOT EXISTS memory_docs_tsv_idx ON memory_docs USING GIN (tsv);
CREATE INDEX IF NOT EXISTS memory_docs_agent_idx ON memory_docs (org_id, agent_id, created_at DESC) WHERE NOT suppressed;

-- Accent-insensitive vector of the drive files (name + extracted text).
ALTER TABLE drive_files ADD COLUMN IF NOT EXISTS search_mem tsvector GENERATED ALWAYS AS (
    to_tsvector('spanish'::regconfig, mem_fold(regexp_replace(name, '[._-]+', ' ', 'g') || ' ' || search_text))) STORED;
CREATE INDEX IF NOT EXISTS drive_files_search_mem_idx ON drive_files USING GIN (search_mem);

DO $$
BEGIN
    ALTER TABLE memory_docs ENABLE ROW LEVEL SECURITY;
    ALTER TABLE memory_docs FORCE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS tenant_isolation ON memory_docs;
    CREATE POLICY tenant_isolation ON memory_docs USING (org_id = app_current_org()) WITH CHECK (org_id = app_current_org());
END
$$;
