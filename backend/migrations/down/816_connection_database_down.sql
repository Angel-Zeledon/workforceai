-- Down of 816_connection_database.sql: database sources lose their configuration (host, port, user, ...). Their
-- passwords stay in the vault until the connections are revoked; revoke them first.
ALTER TABLE connections DROP COLUMN IF EXISTS database;
