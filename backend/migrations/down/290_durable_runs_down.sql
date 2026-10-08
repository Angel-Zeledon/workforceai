-- Rollback of 290_durable_runs.sql. In-progress requests stop being resumable.
DROP TABLE IF EXISTS task_checkpoints;
DROP TABLE IF EXISTS request_runs;
