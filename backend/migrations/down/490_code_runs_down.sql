-- Rollback of 490_code_runs.sql (run history and the code execution setting are dropped;
-- with no setting, code execution is off).
DROP TABLE IF EXISTS code_settings;
DROP TABLE IF EXISTS code_runs;
