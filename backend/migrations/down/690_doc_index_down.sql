-- Rollback of 690_doc_index.sql. The passage index is derived data (rebuilt from the drive); the files are untouched.
DROP TABLE IF EXISTS doc_chunks;
DROP TABLE IF EXISTS doc_index_state;
DROP TABLE IF EXISTS doc_index_settings;
DROP TABLE IF EXISTS doc_embed_usage;
