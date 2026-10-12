-- Rollback of 550_memory.sql. The memory index is dropped (it can be rebuilt
-- from the tasks and reports); the accent-insensitive file vector goes too.
DROP INDEX IF EXISTS drive_files_search_mem_idx;
ALTER TABLE drive_files DROP COLUMN IF EXISTS search_mem;
DROP TABLE IF EXISTS memory_docs;
DROP FUNCTION IF EXISTS mem_fold(text);
