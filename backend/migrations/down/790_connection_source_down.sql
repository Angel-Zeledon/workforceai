-- Rollback of 790_connection_source.sql. Generic sources lose their configuration (their API keys stay in the vault).
ALTER TABLE connections DROP COLUMN IF EXISTS source;
