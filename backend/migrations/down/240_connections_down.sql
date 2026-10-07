-- Rollback of 240_connections.sql. DESTROYS every stored credential, grant,
-- usage row and held send. Take a backup first.
DROP TABLE IF EXISTS connection_holds;
DROP TABLE IF EXISTS oauth_states;
DROP TABLE IF EXISTS connection_usage;
DROP TABLE IF EXISTS connection_grants;
DROP TABLE IF EXISTS credentials;
DROP TABLE IF EXISTS connections;
DROP TABLE IF EXISTS org_data_keys;
