-- Read-only database sources (provider "database"): a connection carries WHERE the database is and AS WHOM we log in
-- (engine, host, port, database, user, sslmode, an optional description and an optional list of schemas the agents
-- may see). It is admin-written organization configuration and holds NO secret: the password stays in the vault
-- (credentials table, sealed with CONNECTIONS_KEK) like every other credential. NULL for every other provider.
-- Spec: docs/architecture/database-sources.md.
--
-- Additive and idempotent. The connections table already has org RLS (240_connections.sql). Down:
-- backend/migrations/down/816_connection_database_down.sql.

ALTER TABLE connections ADD COLUMN IF NOT EXISTS database JSONB;
