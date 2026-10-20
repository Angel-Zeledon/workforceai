-- Search by meaning for the long-term memory (docs/architecture/04-memoria.md).
--
-- Same approach as the drive passage index (690_doc_index.sql): plain
-- postgres:16 has no pgvector, so the embedding is a real[] and the cosine is
-- computed in the backend over a prefiltered candidate set.
--
--   embedding   NULL until embedded (no provider, daily cap reached, provider
--               failure): the document stays searchable by keyword.
--   embed_model model of the embedding; a query only compares vectors of its own model.
--   embed_hash  sha256 of the embedded text (title + body): an unchanged
--               document keeps its vector on re-index, a changed one drops it.
--
-- Forward-only and idempotent. The table keeps its Row-Level Security policy
-- (550_memory.sql); adding columns does not touch it.

ALTER TABLE memory_docs ADD COLUMN IF NOT EXISTS embedding    real[];
ALTER TABLE memory_docs ADD COLUMN IF NOT EXISTS embed_model  TEXT NOT NULL DEFAULT '';
ALTER TABLE memory_docs ADD COLUMN IF NOT EXISTS embed_hash   TEXT NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'memory_docs_embedding_dim' AND conrelid = 'memory_docs'::regclass) THEN
        ALTER TABLE memory_docs ADD CONSTRAINT memory_docs_embedding_dim
            CHECK (embedding IS NULL OR cardinality(embedding) BETWEEN 1 AND 8192);
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS memory_docs_embedded_idx ON memory_docs (org_id, embed_model, updated_at DESC)
    WHERE embedding IS NOT NULL AND NOT suppressed;
CREATE INDEX IF NOT EXISTS memory_docs_unembedded_idx ON memory_docs (org_id, updated_at DESC)
    WHERE embedding IS NULL AND NOT suppressed;
