-- Rollback of 230_org_config.sql (drops organization settings, agent tone overrides and schedules).
DROP TABLE IF EXISTS schedules;
DROP TABLE IF EXISTS agent_settings;
DROP TABLE IF EXISTS org_settings;
