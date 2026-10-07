-- Rollback of 241_controls.sql (kill switch, read-only mode, agent pause, settings).
DROP TABLE IF EXISTS agent_controls;
DROP TABLE IF EXISTS org_controls;
