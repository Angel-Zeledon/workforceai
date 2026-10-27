-- Rollback of 804_link_imports.sql. Every link import stops running at once; its configuration (including the
-- sealed auth header, which cannot be recovered) and its run history (metadata) are lost. The files the imports
-- already wrote stay in the drive, attributed to their link_import principal.
DROP TABLE IF EXISTS link_import_runs;
DROP TABLE IF EXISTS link_imports;
