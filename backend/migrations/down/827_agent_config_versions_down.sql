-- Down of 827_agent_config_versions.sql: the configuration history of the agents is lost (the agents keep their
-- current configuration).
DROP TABLE IF EXISTS agent_config_versions;
DROP FUNCTION IF EXISTS agent_config_versions_immutable();
