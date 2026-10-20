-- Rollback of 786_memory_embeddings.sql. The memory goes back to keyword
-- search only; embeddings are derived data and are rebuilt on the next write
-- or backfill once the migration is applied again.
DROP INDEX IF EXISTS memory_docs_unembedded_idx;
DROP INDEX IF EXISTS memory_docs_embedded_idx;
ALTER TABLE memory_docs DROP CONSTRAINT IF EXISTS memory_docs_embedding_dim;
ALTER TABLE memory_docs DROP COLUMN IF EXISTS embed_hash;
ALTER TABLE memory_docs DROP COLUMN IF EXISTS embed_model;
ALTER TABLE memory_docs DROP COLUMN IF EXISTS embedding;
